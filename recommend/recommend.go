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

func Recommend(ctx context.Context, apis []model.APIDoc, user string) (model.APIDoc, []model.APIField, string, error) {
	llmClient, err := llm.NewGroqLLM()
	if err != nil {
		return model.APIDoc{}, nil, "", err
	}

	apiSummaries := make([]string, len(apis))
	for i, a := range apis {
		apiSummaries[i] = fmt.Sprintf("[%d] %s %s - %s", i, a.Method, a.Path, a.Description)
	}

	oneShotPrompt := fmt.Sprintf(`
You are a senior Go developer responsible for selecting the correct API and generating the correct sample payload for a user's request.

---

### USER REQUEST
%q

---

### AVAILABLE APIs
%s

---

### TASK INSTRUCTIONS

You must:
1. Analyze the user's request.
2. Choose the most appropriate API from the list above.
3. Identify which fields from that API are relevant based on the user's input.
4. Generate a sample payload following the Go model definition provided below.

---

### GO MODEL DEFINITION
%s

---

### RULES TO FOLLOW STRICTLY

1. **Output Format**
   Return a single valid JSON object in the following structure:

   {
     "api_index": <int>,
     "field_index": [<int>, ...],
     "payload": "<the payload string here (JSON or XML)>"
   }

   - The 'api_index' must match one of the listed APIs (0-based index).
   - The 'field_index' corresponds to the selected fields of that API.
   - The 'payload' must be the final payload string (JSON or XML) — not nested JSON.

---

2. **Format Handling**
   - If user explicitly requests **XML**, return XML payload using tags.
   - If user explicitly requests **JSON**, return JSON payload.
   - Default to JSON.
   - Data content must remain identical across formats (only syntax differs).

3. **Field Population Logic**
   - Include only the fields mentioned by user exactly (case-insensitive match with struct field names).
   - Unrecognized fields go into meta.details with { "name": "<field>", "value": "dummy" }.
   - Follow Go struct hierarchy exactly.
   - If user doesn't mention any fields, leave the payload empty.

4. **Tokenized Asset Rules**
   - For "create", "lock", or "burn" asset actions:
     - Populate under payload.tokenizedAsset.meta.
     - Example:
       {
         "payload": {
           "tokenizedAsset": [
             {
               "meta": {
                 "toWalletAddress": "dummy",
                 "fromWalletAddress": "dummy"
               }
             }
           ]
         }
       }

5. **Hierarchy**
   - Respect full hierarchy: context → payload → tokenizedAsset → meta, etc.
   - Never flatten fields.

6. **Private vs Public Data**
   - If private data mentioned, include source and destination blocks (with id).
   - If public, omit source/destination.

7. **Unknown Fields**
   - Unrecognized fields go inside meta.details → [{ "name": "field", "value": "dummy" }].

8. **No Fields Provided**
   - Return empty payload if user provides nothing.

9. **Context Flags**
   - "UBC compliant" → context.isUBCCompliant = true.
   - "async" → context.isAsync = true, else false.

10. **Interactive Clarification (Minimum Info)**
   - Do not generate payload or API recommendation until the following are known:
     1. Whether it is UBC compliant.
     2. Whether it is async.
     3. Whether data is private or public.
     4. At least one valid field (id, key, etc.)
   - If any of these are missing, respond with clarifying questions instead of payload.

---

### OUTPUT REQUIREMENTS
- Return **ONLY** a JSON object matching the schema above.
- No prose, no explanations, no code fences.

`, user, strings.Join(apiSummaries, "\n"), getRequestModelSnippet())

	// One unified LLM call
	response, err := llms.GenerateFromSinglePrompt(ctx, llmClient, oneShotPrompt, llms.WithTemperature(0.2))
	if err != nil {
		return model.APIDoc{}, nil, "", err
	}

	var result struct {
		APIIndex   int      `json:"api_index"`
		FieldIndex []int    `json:"field_index"`
		Payload    string   `json:"payload"`
	}

	if err := json.Unmarshal([]byte(extractJSON(response)), &result); err != nil {
		return model.APIDoc{}, nil, "", fmt.Errorf("parse combined LLM output: %w; raw=%s", err, response)
	}

	if result.APIIndex < 0 || result.APIIndex >= len(apis) {
		return model.APIDoc{}, nil, "", errors.New("api_index out of range")
	}

	chosen := apis[result.APIIndex]
	var picked []model.APIField
	for _, idx := range result.FieldIndex {
		if idx >= 0 && idx < len(chosen.Fields) {
			picked = append(picked, chosen.Fields[idx])
		}
	}

	return chosen, picked, strings.TrimSpace(result.Payload), nil
}

func Recommend1(ctx context.Context, apis []model.APIDoc, user string) (model.APIDoc, []model.APIField, string, error) {
	llm, err := llm.NewGroqLLM()
	if err != nil {
		return model.APIDoc{}, nil, "", err
	}

	apiSummaries := make([]string, len(apis))
	for i, a := range apis {
		apiSummaries[i] = fmt.Sprintf("[%d] %s %s - %s", i, a.Method, a.Path, a.Description)
	}

	pickPrompt := fmt.Sprintf(`You are selecting the best API for the user's request.

APIs:
%s

User request: %q

Return ONLY valid JSON with shape: {"api_index": <int>}
`, strings.Join(apiSummaries, "\n"), user)

	apiJSON, err := llms.GenerateFromSinglePrompt(ctx, llm, pickPrompt,
		llms.WithTemperature(0.0))
	if err != nil {
		return model.APIDoc{}, nil, "", err
	}

	var step1 struct {
		APIIndex int `json:"api_index"`
	}
	if err := json.Unmarshal([]byte(extractJSON(apiJSON)), &step1); err != nil {
		return model.APIDoc{}, nil, "", fmt.Errorf("parse API index: %w; raw=%s", err, apiJSON)
	}
	if step1.APIIndex < 0 || step1.APIIndex >= len(apis) {
		return model.APIDoc{}, nil, "", errors.New("api_index out of range")
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
		return model.APIDoc{}, nil, "", err
	}

	var step2 Selection
	if err := json.Unmarshal([]byte(extractJSON(fieldsJSON)), &step2); err != nil {
		return model.APIDoc{}, nil, "", fmt.Errorf("parse field_index: %w; raw=%s", err, fieldsJSON)
	}

	var picked []model.APIField
	for _, idx := range step2.FieldIndex {
		if idx >= 0 && idx < len(chosen.Fields) {
			picked = append(picked, chosen.Fields[idx])
		}
	}

	payloadPrompt := fmt.Sprintf(`
You are a senior Go developer responsible for generating a precise, valid sample request payload for an API.

### User Instruction
%q

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

2. **Field population logic**
   - Only include fields the user explicitly mentions.
   - Populate only those fields explicitly mentioned by the user that exist *exactly* in the request struct.
   - If user is mentioning any unrelated or unspecified field then keep that in details field of meta as name and give value as any dummy.
   - Field names are case-insensitive but must match the struct definition (e.g., "toWalletAddress" is valid; "address" is not unless the struct has that field).
   - Follow the Go struct hierarchy strictly.
   - If the user is not mentioning any field then don't populate anything, keep the whole request payload empty everytime

3. **Tokenized Asset Rules**
   - If user asks to *create*, *lock*, or *burn* an asset:
     - Populate inside 'payload -> tokenizedAsset'
     - For example, if user says “create asset with toWalletAddress and fromWalletAddress”, then include:
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

4. **Hierarchy Rules**
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

5. **Private vs Public Data**
   - If the user mentions private data:
     - Include both 'source' and 'destination' blocks.
     - Include an "id" field inside each.
   - If the user mentions public data:
     - Do **not** include source or destination.

6. **Unknown Fields Handling**
   - If the user mentions a field that does not exist in the struct (e.g., "address", "key", or "customData"), include it inside:
     meta.details → as a list of { "name": "<field>", "value": "<dummy_value>" }.
   - Example:
     {
       "meta": {
         "details": [
           { "name": "address", "value": "user_provided_value" },
           { "name": "key", "value": "dummy_key_value" }
         ]
       }
     }


7. **If the user provides no field**
   - Return nothing (no payload at all).

8. **Context Flags**
   - If user mentions “UMI compliant” → set 'isUMICompliant': true in context'.
   - If user mentions “async” → set 'isAsync': true 'in context, else false'.
   - If not mentioned, omit these fields entirely.

9. **Follow-up Questions (for Interactivity)**
   - If user query is vague (e.g., “I want to create asset”), respond with brief clarification questions such as:
     - “Would you like it to be UMI compliant?”
     - “Should this be created in async mode?”
     - “Please provide a few field names to include in the payload.”
   - Wait for user’s response before generating final payload.

10. **Minimum Required Information (New Rule)**
   - Do **not** generate or recommend any payload or API until the user has clearly provided the following four details:
     1. Whether it should be **UMI compliant** (yes/no)
     2. Whether it should be **async** (yes/no)
     3. Whether the data is **private or public**
     4. At least one **field name** (like id, value, key, or any valid struct field)
   - Keep asking these as follow-up questions until all four are answered.
   - Once all four are provided, then — and only then — generate the payload and recommend the suitable API.


---

### OUTPUT
Generate only the payload (JSON or XML as per user request). Add explanations, notes, or comments and ask questions as well.
`, user, getRequestModelSnippet(), chosen.Method, chosen.Path)

	payloadResp, err := llms.GenerateFromSinglePrompt(ctx, llm, payloadPrompt,
		llms.WithTemperature(0.2))
	if err != nil {
		return chosen, picked, "", err
	}

	samplePayload := strings.TrimSpace(payloadResp)

	return chosen, picked, samplePayload, nil
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
	Meta           *Meta             "json:\"meta,omitempty\" xml:\"Meta,omitempty\""
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
	IsAsync        *bool   // nil = unknown, true/false = known
	IsUMICompliant *bool   // nil = unknown, true/false = known
	IsPrivate      *bool   // nil = unknown, true = private, false = public
	FieldNames     []string // empty = no fields provided
}

// ClassifyQuery determines if the user is asking to create something or asking about a field
func ClassifyQuery(ctx context.Context, userInput, history string, llm llms.Model) (bool, error) {
	// Check if user is asking about a field (not creating)
	classificationPrompt := fmt.Sprintf(`Analyze the following user query and determine if they are:
1. Asking to CREATE something (e.g., "I want to create a gold bond", "create asset", "make a transaction")
2. Asking ABOUT a field or property (e.g., "what is toWalletAddress?", "explain id field", "what does async mean?")

User query: %q
Conversation history: %s

Return ONLY a JSON object with this structure:
{
  "is_creation_request": true or false,
  "reason": "brief explanation"
}

If the user is asking about a field, property, or explanation, set is_creation_request to false.
If the user is asking to create, make, or generate something, set is_creation_request to true.`, userInput, history)

	response, err := llms.GenerateFromSinglePrompt(ctx, llm, classificationPrompt, llms.WithTemperature(0.0))
	if err != nil {
		return false, err
	}

	var result struct {
		IsCreationRequest bool   `json:"is_creation_request"`
		Reason            string `json:"reason"`
	}

	if err := json.Unmarshal([]byte(extractJSON(response)), &result); err != nil {
		// Fallback: check for common creation keywords
		lower := strings.ToLower(userInput)
		creationKeywords := []string{"create", "make", "generate", "build", "new", "want to", "need to"}
		for _, keyword := range creationKeywords {
			if strings.Contains(lower, keyword) {
				// Check if it's not a question about the keyword itself
				questionWords := []string{"what is", "what does", "explain", "tell me about", "how does"}
				isQuestion := false
				for _, qw := range questionWords {
					if strings.Contains(lower, qw) {
						isQuestion = true
						break
					}
				}
				if !isQuestion {
					return true, nil
				}
			}
		}
		return false, nil
	}

	return result.IsCreationRequest, nil
}

// ExtractQueryInfo extracts the 4 required pieces of information from conversation
func ExtractQueryInfo(ctx context.Context, userInput, history string, llm llms.Model) (*QueryInfo, error) {
	extractionPrompt := fmt.Sprintf(`Analyze the conversation and extract the following information:

User query: %q
Conversation history: %s

Extract:
1. Is it async? (look for "async", "asynchronous", "yes/no" answers)
2. Is it UMI compliant? (look for "UMI compliant", "UMI", "yes/no" answers)
3. Is it private or public? (look for "private", "public", "yes/no" answers)
4. Field names mentioned (any field names like id, value, key, toWalletAddress, etc.)

Return ONLY a JSON object:
{
  "is_async": true/false/null,
  "is_umi_compliant": true/false/null,
  "is_private": true/false/null,
  "field_names": ["field1", "field2", ...]
}

Use null if the information is not found or unclear.`, userInput, history)

	response, err := llms.GenerateFromSinglePrompt(ctx, llm, extractionPrompt, llms.WithTemperature(0.0))
	if err != nil {
		return nil, err
	}

	var result struct {
		IsAsync        *bool    `json:"is_async"`
		IsUMICompliant *bool    `json:"is_umi_compliant"`
		IsPrivate      *bool    `json:"is_private"`
		FieldNames     []string `json:"field_names"`
	}

	if err := json.Unmarshal([]byte(extractJSON(response)), &result); err != nil {
		// Fallback: simple keyword matching
		info := &QueryInfo{}
		lower := strings.ToLower(userInput + " " + history)
		
		// Check for async (handle both yes and no)
		if strings.Contains(lower, "async") || strings.Contains(lower, "asynchronous") {
			// Check for negative indicators
			asyncFalse := strings.Contains(lower, "not async") || 
				strings.Contains(lower, "no async") || 
				strings.Contains(lower, "async: no") ||
				strings.Contains(lower, "async=false")
			if asyncFalse {
				asyncFalseVal := false
				info.IsAsync = &asyncFalseVal
			} else {
				asyncTrue := true
				info.IsAsync = &asyncTrue
			}
		}
		
		// Check for UMI compliant (handle both yes and no)
		if strings.Contains(lower, "umi compliant") || strings.Contains(lower, "umi-compliant") || strings.Contains(lower, "umi") {
			// Check for negative indicators
			umiFalse := strings.Contains(lower, "not umi") || 
				strings.Contains(lower, "no umi") || 
				strings.Contains(lower, "umi: no") ||
				strings.Contains(lower, "umi=false")
			if umiFalse {
				umiFalseVal := false
				info.IsUMICompliant = &umiFalseVal
			} else {
				umiTrue := true
				info.IsUMICompliant = &umiTrue
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
		
		// Try to extract field names from common patterns
		// This is a simple heuristic - the LLM-based extraction is preferred
		commonFields := []string{"id", "value", "key", "toWalletAddress", "fromWalletAddress", 
			"walletAddress", "requestId", "msgId", "name", "type"}
		for _, field := range commonFields {
			if strings.Contains(lower, field) {
				info.FieldNames = append(info.FieldNames, field)
			}
		}
		
		return info, nil
	}

	return &QueryInfo{
		IsAsync:        result.IsAsync,
		IsUMICompliant: result.IsUMICompliant,
		IsPrivate:      result.IsPrivate,
		FieldNames:     result.FieldNames,
	}, nil
}

// GenerateFollowUpQuestions generates questions for missing information
func GenerateFollowUpQuestions(ctx context.Context, info *QueryInfo, llm llms.Model) (string, error) {
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
		missing = append(missing, "Please provide at least one field name for the payload (e.g., id, value, key, toWalletAddress, etc.)")
	}

	if len(missing) == 0 {
		return "", nil
	}

	questionPrompt := fmt.Sprintf(`Generate a friendly, conversational follow-up question asking for the following missing information:
%s

Return ONLY the question text, be concise and friendly.`, strings.Join(missing, "\n"))

	response, err := llms.GenerateFromSinglePrompt(ctx, llm, questionPrompt, llms.WithTemperature(0.3))
	if err != nil {
		// Fallback: simple question
		return "To help you better, I need a few more details:\n" + strings.Join(missing, "\n"), nil
	}

	return strings.TrimSpace(response), nil
}

// AnswerFieldQuestion answers questions about fields without suggesting APIs
func AnswerFieldQuestion(ctx context.Context, userInput, history string, llm llms.Model) (string, error) {
	answerPrompt := fmt.Sprintf(`You are a helpful API documentation assistant. The user is asking about a field or property.

User question: %q
Conversation history: %s

Answer the question clearly and concisely. Do NOT suggest any APIs or generate payloads. Just explain what the field is, what it does, or answer their question directly.

If you don't know the answer, say so politely.`, userInput, history)

	response, err := llms.GenerateFromSinglePrompt(ctx, llm, answerPrompt, llms.WithTemperature(0.3))
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(response), nil
}
