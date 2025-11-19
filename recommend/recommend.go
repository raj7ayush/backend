package recommend

import (
	model "api-recommender/api-parser"
	llm "api-recommender/llm_provider"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/tmc/langchaingo/llms"
)

type Selection struct {
	APIIndex   int   `json:"api_index"`
	FieldIndex []int `json:"field_index"`
}

// Recommend1 is the updated version that supports event payloads for async requests
func Recommend1(ctx context.Context, apis []model.APIDoc, user string, queryInfo *QueryInfo) (model.APIDoc, []model.APIField, string, string, error) {
	llm, err := llm.NewGroqLLM()
	if err != nil {
		return model.APIDoc{}, nil, "", "", err
	}

	apiSummaries := make([]string, len(apis))
	for i, a := range apis {
		apiSummaries[i] = fmt.Sprintf("[%d] %s %s - %s", i, a.Method, a.Path, a.Description)
	}

	// Build enhanced user request with usecase and operation context
	enhancedUserRequest := user
	if queryInfo != nil {
		if queryInfo.UseCase != "" {
			enhancedUserRequest = fmt.Sprintf("%s (usecase: %s)", user, queryInfo.UseCase)
		}
		if queryInfo.Operation != "" {
			operationMap := map[string]string{
				"create": "req issue",
				"burn":   "req manage",
				"trade":  "req settle",
			}
			if apiType, ok := operationMap[queryInfo.Operation]; ok {
				enhancedUserRequest = fmt.Sprintf("%s (operation: %s, API type: %s)", enhancedUserRequest, queryInfo.Operation, apiType)
			}
		}
	}

	pickPrompt := fmt.Sprintf(`You are selecting the best API for the user's request in the UMI project.

APIs:
%s

User request: %q

IMPORTANT: 
- If user mentions "create" or "issue" operation → look for APIs with "req issue" or "issue" in name/path
- If user mentions "burn" or "manage" operation → look for APIs with "req manage" or "manage" in name/path
- If user mentions "trade" or "settle" operation → look for APIs with "req settle" or "settle" in name/path
- If usecase is mentioned (insurance, fd, gold bond, etc.), consider APIs relevant to that usecase

Return ONLY valid JSON with shape: {"api_index": <int>}
`, strings.Join(apiSummaries, "\n"), enhancedUserRequest)

	apiJSON, err := llms.GenerateFromSinglePrompt(ctx, llm, pickPrompt,
		llms.WithTemperature(0.0))
	if err != nil {
		return model.APIDoc{}, nil, "", "", err
	}

	var step1 struct {
		APIIndex int `json:"api_index"`
	}
	if err := json.Unmarshal([]byte(extractJSON(apiJSON)), &step1); err != nil {
		return model.APIDoc{}, nil, "", "", fmt.Errorf("parse API index: %w; raw=%s", err, apiJSON)
	}
	if step1.APIIndex < 0 || step1.APIIndex >= len(apis) {
		return model.APIDoc{}, nil, "", "", errors.New("api_index out of range")
	}
	chosen := apis[step1.APIIndex]

	fieldSummaries := make([]string, len(chosen.Fields))
	for i, f := range chosen.Fields {
		fieldSummaries[i] = fmt.Sprintf("[%d] %s (%s) - %s", i, f.Name, f.Type, f.Description)
	}

	fieldsPrompt := fmt.Sprintf(`For the chosen API %q %s:

Fields:
%s

User request: %q

Return ONLY valid JSON with shape: {"field_index": [<int>, ...]}
`, chosen.Name, chosen.Path, strings.Join(fieldSummaries, "\n"), user)

	fieldsJSON, err := llms.GenerateFromSinglePrompt(ctx, llm, fieldsPrompt,
		llms.WithTemperature(0.0))
	if err != nil {
		return model.APIDoc{}, nil, "", "", err
	}

	var step2 Selection
	if err := json.Unmarshal([]byte(extractJSON(fieldsJSON)), &step2); err != nil {
		return model.APIDoc{}, nil, "", "", fmt.Errorf("parse field_index: %w; raw=%s", err, fieldsJSON)
	}

	var picked []model.APIField
	for _, idx := range step2.FieldIndex {
		if idx >= 0 && idx < len(chosen.Fields) {
			picked = append(picked, chosen.Fields[idx])
		}
	}

	// Build field list for request payload (exclude event fields)
	requestFieldsList := ""
	if queryInfo != nil && len(queryInfo.FieldNames) > 0 {
		// If usecase is specified, mention it in the prompt
		usecaseContext := ""
		if queryInfo.UseCase != "" {
			usecaseContext = fmt.Sprintf(" (for %s usecase", queryInfo.UseCase)
			if queryInfo.Operation != "" {
				usecaseContext += fmt.Sprintf(" - %s operation", queryInfo.Operation)
			}
			usecaseContext += ")"
		}
		requestFieldsList = fmt.Sprintf("\n\n### CRITICAL: Fields for REQUEST PAYLOAD ONLY%s\nUse ONLY these fields in the request payload: %s\nDO NOT include any event-related fields (id, type, eventType, timestamp, etc.) in the request payload.\nEvent fields will be handled separately in the event payload.", usecaseContext, strings.Join(queryInfo.FieldNames, ", "))
	}

	// Warn if event fields are present (they should not be in request payload)
	eventFieldsWarning := ""
	if queryInfo != nil && len(queryInfo.EventFields) > 0 {
		eventFieldsWarning = fmt.Sprintf("\n\n### CRITICAL: DO NOT INCLUDE EVENT FIELDS IN REQUEST PAYLOAD\nThe following fields are for EVENT payload ONLY (not request payload): %s\nThese fields should NOT appear in the request payload you generate.", strings.Join(queryInfo.EventFields, ", "))
	}

	payloadPrompt := fmt.Sprintf(`
You are a senior Go developer responsible for generating a precise, valid sample request payload for an API.

### User Instruction
%q
%s%s

### API Specification
The request model is defined in Go as:
%s

The selected API endpoint is: "%s %s"

---

### RULES TO FOLLOW STRICTLY
1. **Format Handling**
   - If the user explicitly requests **XML**, return a valid XML payload using field names as XML tags.
   - If the user explicitly requests **JSON**, return a valid JSON payload.
   - If no format is mentioned, default to JSON.
   - The **data content** (fields, structure, and values) must remain identical between JSON and XML forms.
     - Only representation (syntax) changes, not content.
     - For example:
       - JSON: '{ "context": { "isAsync": true } }'
       - XML: '<context><isAsync>true</isAsync></context>'

2. **Field population logic - REQUEST PAYLOAD ONLY - STRICT RULES**
   - ONLY include fields that were explicitly mentioned by the user for the REQUEST payload.
   - DO NOT create or add fields on your own. Only use fields the user provided.
   - Populate only those fields explicitly mentioned by the user that exist *exactly* in the TokenizedAsset struct (or other relevant structs in the request model).
   - DO NOT include any event-related fields (id, type, eventType, timestamp, etc.) in the request payload.
   - CRITICAL: If user provides a field that does NOT exist in the TokenizedAsset struct (like purity, quantity, price, type if they're not in the struct), put it in meta.details as a key-value pair: {"name": "<field>", "value": "<dummy_value>"}
   - Field names are case-insensitive but must match the struct definition exactly.
   - Follow the Go struct hierarchy strictly - only use fields that exist in the struct definitions provided.
   - If the user provides no fields, return an empty payload (no payload at all).

3. **Tokenized Asset Rules**
   - If user asks to *create*, *lock*, or *burn* an asset:
     - Populate inside 'payload -> tokenizedAsset'
     - For example, if user says "create asset with toWalletAddress and fromWalletAddress", then include:
       "payload": {
         "tokenizedAsset": [
           {
             "meta": {
               "toWalletAddress": "sampletowalletaddress",
               "fromWalletAddress": "dummyvalue"
             }
           }
         ]
       }

4. **Event Payload Rules - DO NOT APPLY TO REQUEST PAYLOAD**
   - Event payload is generated SEPARATELY and should NOT be included in the request payload.
   - DO NOT populate event fields in the request payload.
   - Event fields (id, type, eventType, timestamp, etc.) are handled in a separate event payload generation step.

5. **Hierarchy Rules**
   - Respect nesting levels such as context → payload → tokenizedAsset → meta, etc.
   - Never flatten or skip nesting.
   - Maintain proper nesting order:
     
     {
       "context": {
         "requestId": "dummy",
         "meta": { ... }
       },
       "source": [
         {"id": "..."}
       ],
       "destination": [
         {"id": "..."}
       ],
       "payload": {
         "tokenizedAsset": [
           {"id": "..."}
         ]
       }
     }
     
   - Never move or flatten fields outside their parent objects.

6. **Private vs Public Data**
   - If the user mentions private data:
     - Include both 'source' and 'destination' blocks.
     - Include an "id" field inside each.
   - If the user mentions public data:
     - Do **not** include source or destination.

7. **Unknown Fields Handling - CRITICAL**
   - If the user provides a field that does NOT exist in the TokenizedAsset struct (check the struct definition carefully):
     - Put it in meta.details as: { "name": "<field>", "value": "<dummy_value>" }
   - Examples of fields that might NOT be in TokenizedAsset: purity, quantity, price (if not in struct), startYear, endYear, policyNumber, etc.
   - Example for unknown fields:
     {
       "payload": {
         "tokenizedAsset": [
           {
             "meta": {
               "details": [
                 { "name": "purity", "value": "24k" },
                 { "name": "quantity", "value": "100" }
               ]
             }
           }
         ]
       }
     }
   - ONLY fields that exist in TokenizedAsset struct should be at the tokenizedAsset level directly.


8. **If the user provides no field**
   - Return nothing (no payload at all).

9. **Context Flags**
   - If user mentions "UMI compliant" → set 'isUMICompliant': true in context'.
   - If user mentions "async" → set 'isAsync': true 'in context, else false'.
   - If not mentioned, omit these fields entirely.

---

### OUTPUT
Generate only the REQUEST payload (JSON or XML as per user request). 
- Include ONLY the fields specified for the request payload.
- DO NOT include any event fields.
- Do not add explanations, notes, or comments. Just return the payload.
`, user, requestFieldsList, eventFieldsWarning, getRequestModelSnippet(), chosen.Method, chosen.Path)

	payloadResp, err := llms.GenerateFromSinglePrompt(ctx, llm, payloadPrompt,
		llms.WithTemperature(0.2))
	if err != nil {
		return chosen, picked, "", "", err
	}

	samplePayload := strings.TrimSpace(payloadResp)

	// Generate event payload if async is true
	var eventPayload string
	if queryInfo != nil && queryInfo.IsAsync != nil && *queryInfo.IsAsync && len(queryInfo.EventFields) > 0 {
		eventPayload, err = generateEventPayload(ctx, llm, queryInfo.EventFields)
		if err != nil {
			// Don't fail if event payload generation fails, just log it
			eventPayload = ""
		}
	}

	return chosen, picked, samplePayload, eventPayload, nil
}

// generateEventPayload generates event payload based on provided event fields
func generateEventPayload(ctx context.Context, llm llms.Model, eventFields []string) (string, error) {
	fieldsStr := strings.Join(eventFields, ", ")

	eventPrompt := fmt.Sprintf(`Generate a JSON payload for an Event struct with the following fields: %s

Event struct definition:
type Event struct {
	Id                string "json:\"id,omitempty\" xml:\"id,attr,omitempty\""
	Type              string "json:\"type,omitempty\" xml:\"type,attr,omitempty\""
	EventType         string "json:\"eventType,omitempty\" xml:\"eventType,attr,omitempty\""
	Category          string "json:\"category,omitempty\" xml:\"category,attr,omitempty\""
	Timestamp         string "json:\"timestamp,omitempty\" xml:\"timestamp,attr,omitempty\""
	CreationTimestamp string "json:\"creationTimestamp,omitempty\" xml:\"creationTimestamp,attr,omitempty\""
	Status            string "json:\"status,omitempty\" xml:\"status,attr,omitempty\""
	Description       string "json:\"description,omitempty\" xml:\"description,attr,omitempty\""
	Source            string "json:\"source,omitempty\" xml:\"source,attr,omitempty\""
	Destination       string "json:\"destination,omitempty\" xml:\"destination,attr,omitempty\""
	Data              string "json:\"data,omitempty\" xml:\"data,attr,omitempty\""
	Meta              *Meta  "json:\"meta,omitempty\" xml:\"Meta,omitempty\""
}

Rules:
- Only include the fields mentioned: %s
- Use dummy values for the fields
- Return ONLY valid JSON for the event payload
- The event should be wrapped in: {"payload": {"event": [<event object>]}}

Return ONLY the JSON payload, no explanations.`, fieldsStr, fieldsStr)

	response, err := llms.GenerateFromSinglePrompt(ctx, llm, eventPrompt, llms.WithTemperature(0.2))
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(response), nil
}

func getRequestModelSnippet() string {
	return `
type Request struct {
	XmlName     xml.Name
	XmlNs       string               "xml:\"xmlns:token,attr\""
	Source      []BusinessIdentifier "json:\"source,omitempty\" xml:\"Source>BusinessIdentifiers>BusinessIdentifier,omitempty\""
	Destination []BusinessIdentifier "json:\"destination,omitempty\" xml:\"Destination>BusinessIdentifiers>BusinessIdentifier,omitempty\""
	Context     Context              "json:\"context,omitempty\" xml:\"Context,omitempty\""
	Payload     Payload              "json:\"payload,omitempty\" xml:\"Payload,omitempty\""
	Signature   string               "json:\"signature,omitempty\" xml:\"signature,attr,omitempty\""
}

type BusinessIdentifier struct {
	Type        string    "json:\"type,omitempty\" xml:\"type,attr,omitempty\""
	Id          string    "json:\"id,omitempty\" xml:\"id,attr,omitempty\""
	PublicKey   string    "json:\"publicKey,omitempty\" xml:\"publicKey,attr,omitempty\""
	Signature   string    "json:\"signature,omitempty\" xml:\"signature,attr,omitempty\""
	CallbackUrl string    "json:\"callbackUrl,omitempty\" xml:\"callbackUrl,attr,omitempty\""
	Account     []Account "json:\"account,omitempty\" xml:\"Accounts>Account,omitempty\""
	Meta        Meta      "json:\"meta,omitempty\" xml:\"Meta,omitempty\""
}

type Account struct {
	Type    string "json:\"type,omitempty\" xml:\"type,attr,omitempty\""
	Address string "json:\"address,omitempty\" xml:\"address,attr,omitempty\""
	VPA     string "json:\"vpa,omitempty\" xml:\"vpa,attr,omitempty\""
}

type Context struct {
	RequestId         string "json:\"requestId,omitempty\" xml:\"requestId,attr,omitempty\""
	MsgId             string "json:\"msgId,omitempty\" xml:\"msgId,attr,omitempty\""
	IsAsync           bool   "json:\"isAsync,omitempty\" xml:\"isAsync,attr,omitempty\""
	IsUMICompliant    bool   "json:\"isUMICompliant,omitempty\" xml:\"isUMICompliant,attr,omitempty\""
	IdempotencyKey    string "json:\"idempotencyKey,omitempty\" xml:\"idempotencyKey,attr,omitempty\""
	NetworkId         string "json:\"networkId,omitempty\" xml:\"networkId,attr,omitempty\""
	WrapperContract   string "json:\"wrapperContract,omitempty\" xml:\"wrapperContract,attr,omitempty\""
	ContractName      string "json:\"contractName,omitempty\" xml:\"contractName,attr,omitempty\""
	MethodName        string "json:\"methodName,omitempty\" xml:\"methodName,attr,omitempty\""
	Sender            string "json:\"sender,omitempty\" xml:\"sender,attr,omitempty\""
	Receiver          string "json:\"receiver,omitempty\" xml:\"receiver,attr,omitempty\""
	Timestamp         string "json:\"timestamp,omitempty\" xml:\"timestamp,attr,omitempty\""
	Purpose           string "json:\"purpose,omitempty\" xml:\"purpose,attr,omitempty\""
	ProdType          string "json:\"prodType,omitempty\" xml:\"prodType,attr,omitempty\""
	Collection        string "json:\"collection,omitempty\" xml:\"collection,attr,omitempty\""
	Type              string "json:\"type,omitempty\" xml:\"type,attr,omitempty\""
	Version           string "json:\"version,omitempty\" xml:\"version,attr,omitempty\""
	Subtype           string "json:\"subtype,omitempty\" xml:\"subtype,attr,omitempty\""
	Action            string "json:\"action,omitempty\" xml:\"action,attr,omitempty\""
	TraceDetails      string "json:\"traceDetails,omitempty\" xml:\"traceDetails,attr,omitempty\""
	OriginalRequestId string "json:\"originalRequestId,omitempty\" xml:\"originalRequestId,attr,omitempty\""
	OriginalTimestamp string "json:\"originalTimestamp,omitempty\" xml:\"originalTimestamp,attr,omitempty\""
	SecureToken       string "json:\"secureToken,omitempty\" xml:\"secureToken,attr,omitempty\""
	Status            string "json:\"status,omitempty\" xml:\"status,attr,omitempty\""
	Code              string "json:\"code,omitempty\" xml:\"code,attr,omitempty\""
	Meta              Meta   "json:\"meta,omitempty\" xml:\"Meta,omitempty\""
}

type Payload struct {
	Type           string            "json:\"type,omitempty\" xml:\"type,attr,omitempty\""
	TokenizedAsset *[]TokenizedAsset "json:\"tokenizedAsset,omitempty\" xml:\"TokenizedAssets>TokenizedAsset,omitempty\""
	Transaction    *[]Transaction    "json:\"transaction,omitempty\" xml:\"Transactions>Transaction,omitempty\""
	Identity       *[]Identity       "json:\"identity,omitempty\" xml:\"Identities>Identity,omitempty\""
	KeyValue       *[]Detail         "json:\"keyValue,omitempty\" xml:\"KeyValue>Detail,omitempty\""
	Event          *[]Event          "json:\"event,omitempty\" xml:\"Events>Event,omitempty\""
	Meta           *Meta             "json:\"meta,omitempty\" xml:\"Meta,omitempty\""
}

type Event struct {
	Id                string "json:\"id,omitempty\" xml:\"id,attr,omitempty\""
	Type              string "json:\"type,omitempty\" xml:\"type,attr,omitempty\""
	EventType         string "json:\"eventType,omitempty\" xml:\"eventType,attr,omitempty\""
	Category          string "json:\"category,omitempty\" xml:\"category,attr,omitempty\""
	Timestamp         string "json:\"timestamp,omitempty\" xml:\"timestamp,attr,omitempty\""
	CreationTimestamp string "json:\"creationTimestamp,omitempty\" xml:\"creationTimestamp,attr,omitempty\""
	Status            string "json:\"status,omitempty\" xml:\"status,attr,omitempty\""
	Description       string "json:\"description,omitempty\" xml:\"description,attr,omitempty\""
	Source            string "json:\"source,omitempty\" xml:\"source,attr,omitempty\""
	Destination       string "json:\"destination,omitempty\" xml:\"destination,attr,omitempty\""
	Data              string "json:\"data,omitempty\" xml:\"data,attr,omitempty\""
	Meta              *Meta  "json:\"meta,omitempty\" xml:\"Meta,omitempty\""
}

type Identity struct {
	Type                string "json:\"type,omitempty\" xml:\"type,attr,omitempty\""
	Id                  string "json:\"id,omitempty\" xml:\"id,attr,omitempty\""
	Category            string "json:\"category,omitempty\" xml:\"category,attr,omitempty\""
	CreationTimestamp   string "json:\"creationTimestamp,omitempty\" xml:\"creationTimestamp,attr,omitempty\""
	LastUpdateTimestamp string "json:\"lastUpdateTimestamp,omitempty\" xml:\"lastUpdateTimestamp,attr,omitempty\""
	Status              string "json:\"status,omitempty\" xml:\"status,attr,omitempty\""
	Issuer              string "json:\"issuer,omitempty\" xml:\"issuer,attr,omitempty\""
	EntityType          string "json:\"entityType,omitempty\" xml:\"entityType,attr,omitempty\""
	Password            string "json:\"password,omitempty\" xml:\"password,attr,omitempty\""
	Alias               string "json:\"alias,omitempty\" xml:\"alias,attr,omitempty\""
	NetworkAlias        string "json:\"networkAlias,omitempty\" xml:\"networkAlias,attr,omitempty\""
	OrganisationAlias   string "json:\"organisationAlias,omitempty\" xml:\"organisationAlias,attr,omitempty\""
	Certificate         string "json:\"certificate,omitempty\" xml:\"certificate,attr,omitempty\""
	Endpoint            string "json:\"endpoint,omitempty\" xml:\"endpoint,attr,omitempty\""
	BridgeAlias         string "json:\"bridgeAlias,omitempty\" xml:\"bridgeAlias,attr,omitempty\""
	NetId               string "json:\"netId,omitempty\" xml:\"netId,attr,omitempty\""
	Layer               string "json:\"layer,omitempty\" xml:\"layer,attr,omitempty\""
	CustodyType         string "json:\"custodyType,omitempty\" xml:\"custodyType,attr,omitempty\""
}

type TokenizedAsset struct {
	Version           string "json:\"version,omitempty\" xml:\"version,attr,omitempty\""
	Id                string "json:\"id,omitempty\" xml:\"id,attr,omitempty\""
	Value             string "json:\"value,omitempty\" xml:\"value,attr,omitempty\""
	Unit              string "json:\"unit,omitempty\" xml:\"unit,attr,omitempty\""
	CreationTimestamp string "json:\"creationTimestamp,omitempty\" xml:\"creationTimestamp,attr,omitempty\""
	IssuerSignature   string "json:\"issuerSignature,omitempty\" xml:\"issuerSignature,attr,omitempty\""
	IssuerAddress     string "json:\"issuerAddress,omitempty\" xml:\"issuerAddress,attr,omitempty\""
	CustodianAddress  string "json:\"custodianAddress,omitempty\" xml:\"custodianAddress,attr,omitempty\""
	OwnerAddress      string "json:\"ownerAddress,omitempty\" xml:\"ownerAddress,attr,omitempty\""
	Type              string "json:\"type,omitempty\" xml:\"type,attr,omitempty\""
	SerialNumber      string "json:\"serialNumber,omitempty\" xml:\"serialNumber,attr,omitempty\""
	Tag               string "json:\"tag,omitempty\" xml:\"tag,attr,omitempty\""
	Meta              *Meta  "json:\"meta,omitempty\" xml:\"Meta,omitempty\""
	ParentId          string "json:\"parentId,omitempty\" xml:\"parentId,attr,omitempty\""
	Status            string "json:\"status,omitempty\" xml:\"status,attr,omitempty\""
}

type Transaction struct {
	Id             string  "json:\"id,omitempty\" xml:\"id,attr,omitempty\""
	Type           string  "json:\"type,omitempty\" xml:\"type,attr,omitempty\""
	From           string  "json:\"from,omitempty\" xml:\"from,attr,omitempty\""
	To             string  "json:\"to,omitempty\" xml:\"to,attr,omitempty\""
	Value          string  "json:\"value,omitempty\" xml:\"value,attr,omitempty\""
	Unit           string  "json:\"unit,omitempty\" xml:\"unit,attr,omitempty\""
	CreationTime   string  "json:\"creationTime,omitempty\" xml:\"creationTime,attr,omitempty\""
	CompletionTime string  "json:\"completionTime,omitempty\" xml:\"completionTime,attr,omitempty\""
	Status         string  "json:\"status,omitempty\" xml:\"status,attr,omitempty\""
	Hash           string  "json:\"hash,omitempty\" xml:\"hash,attr,omitempty\""
	Meta           *Meta   "json:\"meta,omitempty\" xml:\"Meta,omitempty\""
	Details        []Detail "json:\"details,omitempty\" xml:\"Details>Detail,omitempty\""
}

type Detail struct {
	Key   string "json:\"key,omitempty\" xml:\"key,attr,omitempty\""
	Value string "json:\"value,omitempty\" xml:\"value,attr,omitempty\""
}

type Meta struct {
	Name                       string   "json:\"name,omitempty\" xml:\"name,attr,omitempty\""
	Tenure                     string   "json:\"tenure,omitempty\" xml:\"tenure,attr,omitempty\""
	TenureUnit                 string   "json:\"tenureUnit,omitempty\" xml:\"tenureUnit,attr,omitempty\""
	Interval                   string   "json:\"interval,omitempty\" xml:\"interval,attr,omitempty\""
	IntervalUnit               string   "json:\"intervalUnit,omitempty\" xml:\"intervalUnit,attr,omitempty\""
	Interest                   string   "json:\"interest,omitempty\" xml:\"interest,attr,omitempty\""
	InterestUnit               string   "json:\"interestUnit,omitempty\" xml:\"interestUnit,attr,omitempty\""
	TdsFee                     string   "json:\"tdsFee,omitempty\" xml:\"tdsFee,attr,omitempty\""
	TdsFeeUnit                 string   "json:\"tdsFeeUnit,omitempty\" xml:\"tdsFeeUnit,attr,omitempty\""
	PreMatureWithdrawalFee     string   "json:\"preMatureWithdrawalFee,omitempty\" xml:\"preMatureWithdrawalFee,attr,omitempty\""
	PreMatureWithdrawalFeeUnit string   "json:\"preMatureWithdrawalFeeUnit,omitempty\" xml:\"preMatureWithdrawalFeeUnit,attr,omitempty\""
	SwitchFee                  string   "json:\"switchFee,omitempty\" xml:\"switchFee,attr,omitempty\""
	SwitchFeeUnit              string   "json:\"switchFeeUnit,omitempty\" xml:\"switchFeeUnit,attr,omitempty\""
	InterestType               string   "json:\"interestType,omitempty\" xml:\"interestType,attr,omitempty\""
	NomineeName                string   "json:\"nomineeName,omitempty\" xml:\"nomineeName,attr,omitempty\""
	NomineeRelation            string   "json:\"nomineeRelation,omitempty\" xml:\"nomineeRelation,attr,omitempty\""
	WalletAddress              string   "json:\"walletAddress,omitempty\" xml:\"walletAddress,attr,omitempty\""
	ToWalletAddress            string   "json:\"toWalletAddress,omitempty\" xml:\"toWalletAddress,attr,omitempty\""
	FromWalletAddress          string   "json:\"fromWalletAddress,omitempty\" xml:\"fromWalletAddress,attr,omitempty\""
	ToCustodianAddress         string   "json:\"toCustodianAddress,omitempty\" xml:\"toCustodianAddress,attr,omitempty\""
	FromCustodianAddress       string   "json:\"fromCustodianAddress,omitempty\" xml:\"fromCustodianAddress,attr,omitempty\""
	Vpa                        string   "json:\"vpa,omitempty\" xml:\"vpa,attr,omitempty\""
	ToVpa                      string   "json:\"toVpa,omitempty\" xml:\"toVpa,attr,omitempty\""
	FromVpa                    string   "json:\"fromVpa,omitempty\" xml:\"fromVpa,attr,omitempty\""
	UserVpa                    string   "json:\"userVpa,omitempty\" xml:\"userVpa,attr,omitempty\""
	MarketplaceId              string   "json:\"marketplaceId,omitempty\" xml:\"marketplaceId,attr,omitempty\""
	OrgId                      string   "json:\"orgId,omitempty\" xml:\"orgId,attr,omitempty\""
	MspId                      string   "json:\"mspId,omitempty\" xml:\"mspId,attr,omitempty\""
	RoutingMode                string   "json:\"routingMode,omitempty\" xml:\"routingMode,attr,omitempty\""
	PaymentRefId               string   "json:\"paymentRefId,omitempty\" xml:\"paymentRefId,attr,omitempty\""
	PaymentMsgId               string   "json:\"paymentMsgId,omitempty\" xml:\"paymentMsgId,attr,omitempty\""
	PaymentVpa                 string   "json:\"paymentVpa,omitempty\" xml:\"paymentVpa,attr,omitempty\""
	PaymentMode                string   "json:\"paymentMode,omitempty\" xml:\"paymentMode,attr,omitempty\""
	PaymentDate                string   "json:\"paymentDate,omitempty\" xml:\"paymentDate,attr,omitempty\""
	InterestAccrued            string   "json:\"interestAccrued,omitempty\" xml:\"interestAccrued,attr,omitempty\""
	InterestAccruedUnit        string   "json:\"interestAccruedUnit,omitempty\" xml:\"interestAccruedUnit,attr,omitempty\""
	InterestPaid               string   "json:\"interestPaid,omitempty\" xml:\"interestPaid,attr,omitempty\""
	InterestPaidUnit           string   "json:\"interestPaidUnit,omitempty\" xml:\"interestPaidUnit,attr,omitempty\""
	PayoutAmount               string   "json:\"payoutAmount,omitempty\" xml:\"payoutAmount,attr,omitempty\""
	ClientId                   string   "json:\"clientId,omitempty\" xml:\"ClientId,attr,omitempty\""
	SignalDetails              string   "json:\"signalDetails,omitempty\" xml:\"signalDetails,attr,omitempty\""
	Id                         string   "json:\"id,omitempty\" xml:\"id,attr,omitempty\""
	QueryType                  string   "json:\"queryType,omitempty\" xml:\"queryType,attr,omitempty\""
	CollectionName             string   "json:\"collectionName,omitempty\" xml:\"collectionName,attr,omitempty\""
	PayloadRequired            string   "json:\"payloadRequired,omitempty\" xml:\"payloadRequired,attr,omitempty\""
	PayoutAmountUnit           string   "json:\"payoutAmountUnit,omitempty\" xml:\"payoutAmountUnit,attr,omitempty\""
	Payload                    string   "json:\"payload,omitempty\" xml:\"payload,attr,omitempty\""
	PayloadType                string   "json:\"payloadType,omitempty\" xml:\"payloadType,attr,omitempty\""
	PaymentAmount              string   "json:\"paymentAmount,omitempty\" xml:\"paymentAmount,attr,omitempty\""
	ValidTill                  string   "json:\"validTill,omitempty\" xml:\"validTill,attr,omitempty\""
	TemplateId                 string   "json:\"templateId,omitempty\" xml:\"templateId,attr,omitempty\""
	ExpiryDate                 string   "json:\"expiryDate,omitempty\" xml:\"expiryDate,attr,omitempty\""
	UseCaseId                  string   "json:\"useCaseId,omitempty\" xml:\"useCaseId,attr,omitempty\""
	LockedBy                   string   "json:\"lockedBy,omitempty\" xml:\"lockedBy,attr,omitempty\""
	LockedFor                  string   "json:\"lockedFor,omitempty\" xml:\"lockedFor,attr,omitempty\""
	Quantity                   string   "json:\"quantity,omitempty\" xml:\"quantity,attr,omitempty\""
	ContentType                string   "json:\"contentType,omitempty\" xml:\"contentType,attr,omitempty\""
	Details                    []Detail "json:\"details,omitempty\" xml:\"Details>Detail,omitempty\""
}
`
}

func extractJSON(s string) string {
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start >= 0 && end > start {
		return s[start : end+1]
	}
	return s
}

func ExtractRequestedFields(ctx context.Context, prompt string, availableFields []string, llm llms.Model) ([]string, error) {
	fieldsStr := strings.Join(availableFields, ", ")
	extractionPrompt := fmt.Sprintf(`
From the list of fields [%s],
which ones does the user want set in their request? 
User prompt: "%s"

Return ONLY a JSON array of field names.
Example: ["id","value"]
`, fieldsStr, prompt)
	answer, err := llms.GenerateFromSinglePrompt(ctx, llm, extractionPrompt, llms.WithTemperature(0.0))
	if err != nil {
		return nil, err
	}
	var requested []string
	if err := json.Unmarshal([]byte(extractJSON(answer)), &requested); err != nil {
		return nil, err
	}
	return requested, nil
}

func GetSampleValues(ctx context.Context, prompt string, fields []string, llm llms.Model) (map[string]string, error) {
	fieldsStr := strings.Join(fields, ", ")
	valuePrompt := fmt.Sprintf(`
For the user request: "%s",
suggest a value for each of the fields [%s].
Return ONLY a JSON object of {field: value} pairs.
Example: {"id":"474bccfa...", "value":"100"}
`, prompt, fieldsStr)
	answer, err := llms.GenerateFromSinglePrompt(ctx, llm, valuePrompt, llms.WithTemperature(0.0))
	if err != nil {
		return nil, err
	}
	var values map[string]string
	if err := json.Unmarshal([]byte(extractJSON(answer)), &values); err != nil {
		return nil, err
	}
	return values, nil
}

func RenderAssetXML(values map[string]string) string {
	id := values["id"]
	value := values["value"]
	// Add other fields as needed, use "" if not present
	meta := ""
	if m, ok := values["meta"]; ok {
		meta = m
	}
	return fmt.Sprintf(`
<token:ReqManage xmlns:token="http://npci.org/token/schema/">
    <Payload type="tokenized_asset">
        <TokenizedAssets>
            <TokenizedAsset %s %s>
                <Meta>%s</Meta>
            </TokenizedAsset>
        </TokenizedAssets>
    </Payload>
</token:ReqManage>`,
		optAttr("id", id),
		optAttr("value", value),
		meta,
	)
}

func optAttr(name, value string) string {
	if value != "" {
		return fmt.Sprintf(`%s="%s"`, name, value)
	}
	return ""
}
func HandleCreateAssetPrompt(ctx context.Context, prompt string, llm llms.Model) {
	// Define available asset fields (from your model or config)
	assetFields := []string{"id", "value", "meta"}
	requestedFields, err := ExtractRequestedFields(ctx, prompt, assetFields, llm)
	if err != nil {
		panic(err)
	}
	values, err := GetSampleValues(ctx, prompt, requestedFields, llm)
	if err != nil {
		panic(err)
	}
	xml := RenderAssetXML(values)
	fmt.Println("Sample Payload:\n", xml)
}

// QueryInfo tracks the required information for API recommendation
type QueryInfo struct {
	IsAsync        *bool    // nil = unknown, true/false = known
	IsUMICompliant *bool    // nil = unknown, true/false = known
	IsPrivate      *bool    // nil = unknown, true = private, false = public
	FieldNames     []string // empty = no fields provided
	EventFields    []string // fields for event payload (when async is true)
	Operation      string   // operation type: "create"/"issue", "burn"/"manage", "trade"/"settle", or empty
	UseCase        string   // usecase type: "insurance", "fd", "gold bond", etc.
}

// getUsecaseFields returns typical fields for a given usecase
func getUsecaseFields(usecase string, operation string) []string {
	usecase = strings.ToLower(usecase)
	operation = strings.ToLower(operation)

	// Map of usecase -> operation -> fields
	usecaseFieldMap := map[string]map[string][]string{
		"insurance": {
			"create": []string{"startYear", "endYear", "policyNumber", "premium", "coverageAmount", "type"},
			"burn":   []string{"policyNumber", "type", "id"},
			"trade":  []string{"policyNumber", "type", "id", "value"},
		},
		"fd": {
			"create": []string{"principal", "interestRate", "tenure", "maturityDate", "type"},
			"burn":   []string{"id", "type", "principal"},
			"trade":  []string{"id", "type", "value", "principal"},
		},
		"gold bond": {
			"create": []string{"quantity", "purity", "price", "type", "id"},
			"burn":   []string{"id", "type", "quantity"},
			"trade":  []string{"id", "type", "value", "quantity"},
		},
		"bond": {
			"create": []string{"quantity", "purity", "price", "type", "id"},
			"burn":   []string{"id", "type", "quantity"},
			"trade":  []string{"id", "type", "value", "quantity"},
		},
		"mutual fund": {
			"create": []string{"units", "nav", "investmentAmount", "type", "id"},
			"burn":   []string{"id", "type", "units"},
			"trade":  []string{"id", "type", "value", "units"},
		},
	}

	if opMap, ok := usecaseFieldMap[usecase]; ok {
		if fields, ok := opMap[operation]; ok {
			return fields
		}
		// If operation not found, return default fields for the usecase
		if fields, ok := opMap["create"]; ok {
			return fields
		}
	}

	return []string{}
}

// ClassifyQuery determines if the user is asking to create something or asking about a field
func ClassifyQuery(ctx context.Context, userInput, history string, llm llms.Model) (bool, bool, error) {
	// First check: is this an irrelevant request (not API-related)?
	lower := strings.ToLower(userInput)

	// Check for irrelevant requests (buying cars, etc.)
	irrelevantKeywords := []string{"buy", "purchase", "sell", "lamborghini", "lamborgini", "car", "vehicle", "shopping"}
	for _, keyword := range irrelevantKeywords {
		if strings.Contains(lower, keyword) {
			// Check if it's actually API-related (e.g., "buy asset" is relevant)
			apiRelated := strings.Contains(lower, "asset") || strings.Contains(lower, "bond") ||
				strings.Contains(lower, "token") || strings.Contains(lower, "transaction") ||
				strings.Contains(lower, "api") || strings.Contains(lower, "payload")
			if !apiRelated {
				return false, false, nil // Not a creation request, and irrelevant
			}
		}
	}

	// Check for explanation questions first (these should always be field questions)
	explainKeywords := []string{"explain", "what is", "what does", "tell me about", "how does", "describe", "meaning of"}
	for _, keyword := range explainKeywords {
		if strings.Contains(lower, keyword) {
			return false, true, nil // Field question, relevant
		}
	}

	// Check if user is asking about a field (not creating)
	classificationPrompt := fmt.Sprintf(`Analyze the following user query and determine:
1. Is this asking to CREATE something (e.g., "I want to create a gold bond", "create asset", "make a transaction", "burn asset", "build insurance usecase", "I want to build an fd usecase")
2. Is this asking ABOUT a field or property (e.g., "what is toWalletAddress?", "explain id field", "what does async mean?")
3. Is this providing answers to previous questions (e.g., "yes", "no", "async", "private", field names like "id", "value", "create", "burn", "trade")

IMPORTANT: 
- If the user is providing answers to follow-up questions (like "yes", "no", "async", "private", or field names, or operation types like "create"/"burn"/"trade"), 
  this is STILL a creation request continuation, NOT a field question.
- If user mentions "build X usecase" or "insurance usecase" or "fd usecase" → is_creation_request = true, is_relevant = true

User query: %q
Recent conversation (last 3-4 messages only): %s

Return ONLY a JSON object:
{
  "is_creation_request": true or false,
  "is_relevant": true or false,
  "reason": "brief explanation"
}

Rules:
- If asking "explain X" or "what is X" → is_creation_request = false, is_relevant = true
- If asking to create/make/generate/burn/lock/build usecase → is_creation_request = true, is_relevant = true
- If providing answers to questions (yes/no/field names/operation types) → is_creation_request = true, is_relevant = true
- If completely unrelated to APIs → is_relevant = false`, userInput, getRecentHistory(history, 3))

	response, err := llms.GenerateFromSinglePrompt(ctx, llm, classificationPrompt, llms.WithTemperature(0.0))
	if err != nil {
		// Fallback logic
		return classifyQueryFallback(userInput), true, nil
	}

	var result struct {
		IsCreationRequest bool   `json:"is_creation_request"`
		IsRelevant        bool   `json:"is_relevant"`
		Reason            string `json:"reason"`
	}

	if err := json.Unmarshal([]byte(extractJSON(response)), &result); err != nil {
		return classifyQueryFallback(userInput), true, nil
	}

	if !result.IsRelevant {
		return false, false, nil
	}

	return result.IsCreationRequest, true, nil
}

// classifyQueryFallback provides fallback classification logic
func classifyQueryFallback(userInput string) bool {
	lower := strings.ToLower(userInput)

	// Explanation questions
	explainKeywords := []string{"explain", "what is", "what does", "tell me about", "how does", "describe"}
	for _, keyword := range explainKeywords {
		if strings.Contains(lower, keyword) {
			return false
		}
	}

	// Creation keywords
	creationKeywords := []string{"create", "make", "generate", "build", "new", "want to", "need to", "burn", "lock"}
	for _, keyword := range creationKeywords {
		if strings.Contains(lower, keyword) {
			return true
		}
	}

	// If it's just answers (yes/no/field names), treat as creation continuation
	if len(strings.Fields(lower)) <= 3 {
		// Short responses are likely answers to questions
		return true
	}

	return false
}

// getRecentHistory extracts only the last N messages from history
func getRecentHistory(history string, n int) string {
	if history == "" {
		return ""
	}

	lines := strings.Split(history, "\n")
	if len(lines) <= n*2 { // Each message pair (Human + AI) is ~2 lines
		return history
	}

	// Get last N*2 lines (N message pairs)
	start := len(lines) - (n * 2)
	if start < 0 {
		start = 0
	}

	return strings.Join(lines[start:], "\n")
}

// ExtractQueryInfo extracts the 4 required pieces of information from conversation
// Only looks at the current creation request context (not previous unrelated requests)
func ExtractQueryInfo(ctx context.Context, userInput, history string, llm llms.Model, isNewRequest bool) (*QueryInfo, error) {
	// If this is a new creation request, completely ignore previous request context
	// Only look at the current user input
	var contextToUse string
	if isNewRequest {
		// For new requests, completely ignore history - start fresh
		// This ensures event fields from previous requests are not carried over
		contextToUse = ""
	} else {
		// For continuation of same request, use the provided history (which should include Q&A)
		// This is important to capture answers to previous questions
		contextToUse = history
	}

	// Build context message
	contextMsg := ""
	if contextToUse == "" {
		contextMsg = "Previous conversation context: IGNORE - this is a new request, start fresh. Do NOT extract event_fields from previous requests. If async is true in this new request, event_fields should be empty (will be asked separately)."
	} else {
		contextMsg = fmt.Sprintf(`Recent conversation context (this is a CONTINUATION - user is answering questions):
%s

IMPORTANT: Look for question-answer pairs. For example:
- If you see "Is this async?" followed by "yes" or "no" → extract that answer
- If you see "Is this UMI compliant?" followed by "yes" or "no" → extract that answer  
- If you see "Is this private or public?" followed by "private" or "public" → extract that answer
- If you see field names mentioned → extract them
- For event_fields: only extract if user explicitly mentions event fields in THIS request's conversation

Extract information from BOTH the current query AND the conversation context above.`, contextToUse)
	}

	extractionPrompt := fmt.Sprintf(`Analyze the current creation request and extract the following information:

Current user query: %q
%s

CRITICAL RULES:
- If this is a NEW creation request (like "create gold bond" or "burn asset"), ONLY extract information from the current query.
- If this is a CONTINUATION (user answering questions), extract from BOTH current query AND the conversation context above.
- Look for question-answer patterns in the conversation:
  * "Is this async?" → look for "yes"/"no" answer → set is_async accordingly
  * "Is this UMI compliant?" → look for "yes"/"no" answer → set is_umi_compliant accordingly
  * "Is this private or public?" → look for "private"/"public" answer → set is_private accordingly
  * Field names mentioned anywhere in the conversation → add to field_names
- IGNORE all information from PREVIOUS UNRELATED requests (different creation requests).
- But DO use information from the CURRENT request's question-answer flow.

Extract:
1. Usecase type (if user mentions building a usecase like "insurance", "fd", "gold bond", "mutual fund", etc. in current query OR conversation context)
2. Operation type (if user mentions operation in current query OR conversation context:
   - "create" or "issue" → set operation to "create"
   - "burn" or "manage" → set operation to "burn"
   - "trade" or "settle" → set operation to "trade")
3. Is it async? (look for "async", "asynchronous", or "yes"/"no" answers to async questions in current query AND conversation context)
4. Is it UMI compliant? (look for "UMI compliant", "UMI", or "yes"/"no" answers to UMI questions in current query AND conversation context)
5. Is it private or public? (look for "private", "public", or answers to private/public questions in current query AND conversation context)
6. Field names for REQUEST payload (CRITICAL: Only fields mentioned for "request payload", "main payload", "payload", or fields mentioned BEFORE event fields are discussed. Do NOT include event fields here.)
7. Event field names (CRITICAL: Only fields mentioned AFTER user talks about "event payload", "event", or explicitly says "event will have". These are SEPARATE from request payload fields.)

Return ONLY a JSON object:
{
  "usecase": "insurance"/"fd"/"gold bond"/etc. or null,
  "operation": "create"/"burn"/"trade" or null,
  "is_async": true/false/null,
  "is_umi_compliant": true/false/null,
  "is_private": true/false/null,
  "field_names": ["field1", "field2", ...],
  "event_fields": ["eventField1", "eventField2", ...]
}

CRITICAL SEPARATION RULES:
- Request payload fields (field_names) and event payload fields (event_fields) are COMPLETELY SEPARATE.
- If user says "request payload will have X, Y, Z" → put X, Y, Z in field_names ONLY.
- If user says "event will have A, B, C" → put A, B, C in event_fields ONLY.
- If user mentions fields without specifying "request" or "event", look at context:
  * Fields mentioned BEFORE event discussion → field_names
  * Fields mentioned AFTER event discussion or with "event" keyword → event_fields
- DO NOT mix them. If user provides both, they must be in separate arrays.
- If this is a continuation, extract from BOTH current query AND conversation context.
- Use null ONLY if the information is truly not found in the current request's conversation flow.
- For event_fields: 
  * If this is a NEW request and is_async is true, leave event_fields as empty array [] (they will be asked separately)
  * If this is a CONTINUATION and is_async is true, only include event_fields if user explicitly provided them in the conversation
  * Do NOT carry over event_fields from previous unrelated requests`, userInput, contextMsg)

	response, err := llms.GenerateFromSinglePrompt(ctx, llm, extractionPrompt, llms.WithTemperature(0.0))
	if err != nil {
		// Fallback extraction
		return extractQueryInfoFallback(userInput, contextToUse), nil
	}

	var result struct {
		UseCase        string   `json:"usecase"`
		Operation      string   `json:"operation"`
		IsAsync        *bool    `json:"is_async"`
		IsUMICompliant *bool    `json:"is_umi_compliant"`
		IsPrivate      *bool    `json:"is_private"`
		FieldNames     []string `json:"field_names"`
		EventFields    []string `json:"event_fields"`
	}

	if err := json.Unmarshal([]byte(extractJSON(response)), &result); err != nil {
		// Fallback: use the fallback function with proper context
		return extractQueryInfoFallback(userInput, contextToUse), nil
	}

	info := &QueryInfo{
		UseCase:        result.UseCase,
		Operation:      result.Operation,
		IsAsync:        result.IsAsync,
		IsUMICompliant: result.IsUMICompliant,
		IsPrivate:      result.IsPrivate,
		FieldNames:     result.FieldNames,
		EventFields:    result.EventFields,
	}

	// Note: We don't auto-populate usecase fields here to ensure all 4 questions are asked
	// Usecase-specific fields will be suggested in the follow-up question instead

	// If extraction failed, use fallback
	if info.IsAsync == nil && info.IsUMICompliant == nil && info.IsPrivate == nil && len(info.FieldNames) == 0 && info.UseCase == "" {
		fallbackInfo := extractQueryInfoFallback(userInput, contextToUse)
		if fallbackInfo != nil {
			// Merge fallback info but preserve usecase/operation if already extracted
			if info.UseCase == "" {
				info.UseCase = fallbackInfo.UseCase
			}
			if info.Operation == "" {
				info.Operation = fallbackInfo.Operation
			}
			if info.IsAsync == nil {
				info.IsAsync = fallbackInfo.IsAsync
			}
			if info.IsUMICompliant == nil {
				info.IsUMICompliant = fallbackInfo.IsUMICompliant
			}
			if info.IsPrivate == nil {
				info.IsPrivate = fallbackInfo.IsPrivate
			}
			if len(info.FieldNames) == 0 {
				info.FieldNames = fallbackInfo.FieldNames
			}
		}
	}

	return info, nil
}

// extractQueryInfoFallback provides fallback extraction logic
func extractQueryInfoFallback(userInput, context string) *QueryInfo {
	info := &QueryInfo{}
	// Always use context if available to capture previous answers
	// Put context first so previous answers are found
	textToAnalyze := userInput
	if context != "" {
		textToAnalyze = context + " " + userInput
	}
	lower := strings.ToLower(textToAnalyze)

	// Extract usecase type
	usecaseKeywords := map[string]string{
		"insurance":     "insurance",
		"fd":            "fd",
		"fixed deposit": "fd",
		"gold bond":     "gold bond",
		"bond":          "bond",
		"mutual fund":   "mutual fund",
		"mf":            "mutual fund",
	}
	for keyword, usecase := range usecaseKeywords {
		if (strings.Contains(lower, keyword) && strings.Contains(lower, "usecase")) ||
			(strings.Contains(lower, "build") && strings.Contains(lower, keyword)) {
			info.UseCase = usecase
			break
		}
	}

	// Extract operation type
	if strings.Contains(lower, "create") || strings.Contains(lower, "issue") {
		info.Operation = "create"
	} else if strings.Contains(lower, "burn") || strings.Contains(lower, "manage") {
		info.Operation = "burn"
	} else if strings.Contains(lower, "trade") || strings.Contains(lower, "settle") {
		info.Operation = "trade"
	}

	// Check for async - look for explicit mentions or yes/no answers to async questions
	if strings.Contains(lower, "async") || strings.Contains(lower, "asynchronous") {
		// Check for negative indicators
		asyncFalse := strings.Contains(lower, "not async") ||
			strings.Contains(lower, "no async") ||
			strings.Contains(lower, "async: no") ||
			strings.Contains(lower, "async=false") ||
			strings.Contains(lower, "async no") ||
			(strings.Contains(lower, "async") && strings.Contains(lower, "no") &&
				strings.Index(lower, "async") < strings.Index(lower, "no")+10)
		if asyncFalse {
			asyncFalseVal := false
			info.IsAsync = &asyncFalseVal
		} else {
			// Check if there's a "yes" answer near "async" question
			asyncTrue := true
			info.IsAsync = &asyncTrue
		}
	} else if context != "" {
		// Look for yes/no answers to async questions in context
		// Pattern: question about async followed by yes/no
		if (strings.Contains(lower, "async") || strings.Contains(lower, "asynchronous")) &&
			(strings.Contains(lower, " yes") || strings.Contains(lower, "\nyes") ||
				strings.Contains(lower, "yes\n") || strings.Contains(lower, "yes,")) {
			asyncTrue := true
			info.IsAsync = &asyncTrue
		} else if (strings.Contains(lower, "async") || strings.Contains(lower, "asynchronous")) &&
			(strings.Contains(lower, " no") || strings.Contains(lower, "\nno") ||
				strings.Contains(lower, "no\n") || strings.Contains(lower, "no,")) {
			asyncFalseVal := false
			info.IsAsync = &asyncFalseVal
		}
	}

	// Check for UMI compliant - look for explicit mentions or yes/no answers
	if strings.Contains(lower, "umi compliant") || strings.Contains(lower, "umi-compliant") {
		umiFalse := strings.Contains(lower, "not umi") ||
			strings.Contains(lower, "no umi") ||
			strings.Contains(lower, "umi: no") ||
			strings.Contains(lower, "umi=false") ||
			strings.Contains(lower, "umi no") ||
			(strings.Contains(lower, "umi") && strings.Contains(lower, "no") &&
				strings.Index(lower, "umi") < strings.Index(lower, "no")+15)
		if umiFalse {
			umiFalseVal := false
			info.IsUMICompliant = &umiFalseVal
		} else {
			umiTrue := true
			info.IsUMICompliant = &umiTrue
		}
	} else if strings.Contains(lower, "umi") && !strings.Contains(lower, "explain") {
		// Check for yes/no answers to UMI questions
		if strings.Contains(lower, " yes") || strings.Contains(lower, "\nyes") ||
			strings.Contains(lower, "yes\n") || strings.Contains(lower, "yes,") {
			umiTrue := true
			info.IsUMICompliant = &umiTrue
		} else if strings.Contains(lower, " no") || strings.Contains(lower, "\nno") ||
			strings.Contains(lower, "no\n") || strings.Contains(lower, "no,") {
			umiFalseVal := false
			info.IsUMICompliant = &umiFalseVal
		}
	}

	// Check for private/public
	if strings.Contains(lower, "private") && !strings.Contains(lower, "public") {
		privateTrue := true
		info.IsPrivate = &privateTrue
	} else if strings.Contains(lower, "public") {
		privateFalse := false
		info.IsPrivate = &privateFalse
	}

	// Extract field names - be more careful
	commonFields := []string{"id", "value", "key", "toWalletAddress", "fromWalletAddress",
		"walletAddress", "requestId", "msgId", "name", "type", "event", "eventType",
		"startYear", "endYear", "policyNumber", "premium", "coverageAmount",
		"principal", "interestRate", "tenure", "maturityDate",
		"quantity", "purity", "price", "units", "nav", "investmentAmount"}
	for _, field := range commonFields {
		// Check if field is mentioned as a field name, not just in explanation
		if strings.Contains(lower, field) && !strings.Contains(lower, "explain "+field) &&
			!strings.Contains(lower, "what is "+field) {
			info.FieldNames = append(info.FieldNames, field)
		}
	}

	// Note: We don't auto-populate usecase fields in fallback either
	// This ensures all 4 questions (async, UMI, private/public, fields) are asked together
	// Usecase-specific fields will be suggested in the follow-up question

	return info
}

// GenerateFollowUpQuestions generates questions for missing information
func GenerateFollowUpQuestions(ctx context.Context, info *QueryInfo, llm llms.Model) (string, error) {
	// If usecase is mentioned but operation is not specified, ask about operation FIRST
	// Do NOT ask the 4 questions until operation is selected
	if info.UseCase != "" && info.Operation == "" {
		operationPrompt := fmt.Sprintf(`The user wants to build a %s usecase. Ask them which operation they want to perform:
- Create/Issue (req issue API)
- Burn/Manage (req manage API)
- Trade/Settle (req settle API)

Generate a friendly question asking which operation they want. Return ONLY the question.`, info.UseCase)

		response, err := llms.GenerateFromSinglePrompt(ctx, llm, operationPrompt, llms.WithTemperature(0.3))
		if err != nil {
			// Fallback: return a clear question about operation
			return fmt.Sprintf("For %s usecase, which operation do you want to perform?\n\n- CREATE/ISSUE → use req issue API\n- BURN/MANAGE → use req manage API\n- TRADE/SETTLE → use req settle API\n\nPlease specify: create, burn, or trade", info.UseCase), nil
		}
		return strings.TrimSpace(response), nil
	}

	var missing []string

	if info.IsAsync == nil {
		missing = append(missing, "Is this request async? (yes/no)")
	}
	if info.IsUMICompliant == nil {
		missing = append(missing, "Is this UMI compliant? (yes/no)")
	}
	if info.IsPrivate == nil {
		missing = append(missing, "Is this private or public?")
	}
	if len(info.FieldNames) == 0 {
		// If usecase is known, suggest usecase-specific fields (but don't require all of them)
		if info.UseCase != "" {
			op := info.Operation
			if op == "" {
				op = "create"
			}
			suggestedFields := getUsecaseFields(info.UseCase, op)
			if len(suggestedFields) > 0 {
				fieldsStr := strings.Join(suggestedFields, ", ")
				missing = append(missing, fmt.Sprintf("Please provide at least one field name for the REQUEST payload. Suggested fields for %s (%s): %s", info.UseCase, op, fieldsStr))
			} else {
				missing = append(missing, "Please provide at least one field name for the REQUEST payload (e.g., id, type, value, etc.)")
			}
		} else {
			missing = append(missing, "Please provide at least one field name for the REQUEST payload (e.g., id, type, value, etc.)")
		}
	}

	// If async is true, check if event fields are provided
	if info.IsAsync != nil && *info.IsAsync && len(info.EventFields) == 0 {
		missing = append(missing, "Since this is an async request, please provide at least one field name for the EVENT payload separately (e.g., id, type, eventType, timestamp, etc.). Note: Event payload fields are different from request payload fields.")
	}

	if len(missing) == 0 {
		return "", nil
	}

	// Build a comprehensive question that asks for ALL missing information at once
	// Count missing items for better formatting
	numMissing := len(missing)
	missingList := ""
	for i, item := range missing {
		if i == numMissing-1 && numMissing > 1 {
			missingList += fmt.Sprintf("and %d. %s", i+1, item)
		} else {
			missingList += fmt.Sprintf("%d. %s\n", i+1, item)
		}
	}

	questionPrompt := fmt.Sprintf(`You are an API assistant. The user wants to create something, but you need %d pieces of information before you can proceed.

Missing information:
%s

CRITICAL: Generate ONE single question that asks for ALL %d items above. 
- DO NOT ask them one by one
- DO NOT split into multiple questions
- Ask for ALL items in a single, clear question
- Format it like: "To proceed, I need the following: 1) [item 1], 2) [item 2], 3) [item 3], 4) [item 4]. Please provide all of these."

Return ONLY the single question text. Be friendly and clear.`, numMissing, missingList, numMissing)

	response, err := llms.GenerateFromSinglePrompt(ctx, llm, questionPrompt, llms.WithTemperature(0.3))
	if err != nil {
		// Fallback: format all missing items in one clear question
		formattedMissing := ""
		for i, item := range missing {
			formattedMissing += fmt.Sprintf("%d. %s\n", i+1, item)
		}
		return fmt.Sprintf("To proceed with your request, I need the following information:\n%sPlease provide all of these details at once.", formattedMissing), nil
	}

	return strings.TrimSpace(response), nil
}

// AnswerFieldQuestion answers questions about fields without suggesting APIs
func AnswerFieldQuestion(ctx context.Context, userInput, history string, llm llms.Model) (string, error) {
	// Check if user is asking about UMI specifically
	lower := strings.ToLower(userInput)

	// Check for "UMI compliant" vs just "UMI"
	if strings.Contains(lower, "umi compliant") || strings.Contains(lower, "umi-compliant") {
		return "UMI compliant means that a request adheres to the **Unified Market Interface** (UMI) compliance standard. UMI is a standard that ensures interoperability and standardization across different market participants and systems. When a request is UMI compliant, it means it follows the Unified Market Interface specifications for data exchange and communication protocols.", nil
	}

	if strings.Contains(lower, "umi") && (strings.Contains(lower, "explain") ||
		strings.Contains(lower, "what is") || strings.Contains(lower, "what does") ||
		strings.Contains(lower, "meaning") || strings.Contains(lower, "stand for") ||
		strings.Contains(lower, "full form") || strings.Contains(lower, "fullform")) {
		return "UMI stands for **Unified Market Interface**. It's a compliance standard that ensures interoperability and standardization across different market participants and systems. When a request is UMI compliant, it means it adheres to the Unified Market Interface specifications for data exchange and communication protocols.", nil
	}

	// Check for async field question - provide UMI project-specific answer
	if strings.Contains(lower, "async") && (strings.Contains(lower, "what is") ||
		strings.Contains(lower, "explain") || strings.Contains(lower, "what does") ||
		strings.Contains(lower, "field") || strings.Contains(lower, "sync vs async") ||
		strings.Contains(lower, "sync versus async") || strings.Contains(lower, "difference")) {
		return `In the UMI project, the **async** field (or **isAsync**) is a boolean flag in the request context that determines how the API request is processed.

**Async Flow (isAsync = true):**
1. FSP commits the transaction on DLT (Distributed Ledger Technology)
2. Chaincode sends an event to FSP via gRPC
3. FSP produces the event in Kafka
4. Backend consumes the event from Kafka

**Sync Flow (isAsync = false or omitted):**
The API processes the request synchronously, waiting for the operation to complete before returning a response.

When you set 'isAsync: true' in your request, the system follows the async flow where the transaction is committed on DLT first, then events are propagated through gRPC and Kafka for backend processing.`, nil
	}

	// Don't use history for field questions - answer based on current question only
	// This prevents confusion from previous questions
	answerPrompt := fmt.Sprintf(`You are an AI agent for the UMI (Unified Market Interface) project. You provide answers ONLY related to this project.

User question: %q

IMPORTANT RULES:
- You are an AI agent of the UMI project - give answers ONLY related to this project.
- If the user asks about "UMI" or "UMI compliant", explain that UMI stands for "Unified Market Interface" and it's a compliance standard for this project.
- If the user asks about "async" or "isAsync" or "sync vs async", explain the UMI project-specific flow:
  * Async flow: FSP commits on DLT → Chaincode sends event to FSP via gRPC → FSP produces event in Kafka → Backend consumes from Kafka
  * Sync flow: API processes synchronously, waiting for operation to complete
- Answer ONLY the current question. Do NOT reference previous questions or answers.
- Answer the question clearly and concisely with UMI project-specific context.
- Do NOT suggest any APIs or generate payloads unless explicitly asked.
- Just explain what the field is, what it does, or answer their question directly in the context of the UMI project.

If the question is not related to the UMI project, politely redirect: "I'm an AI agent for the UMI project. I can only answer questions related to this project. How can I help you with UMI-related questions?"

If you don't know the answer, say so politely.`, userInput)

	response, err := llms.GenerateFromSinglePrompt(ctx, llm, answerPrompt, llms.WithTemperature(0.3))
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(response), nil
}
