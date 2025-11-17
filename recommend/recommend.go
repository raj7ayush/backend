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
	llm, err := llm.NewGroqLLM()
	if err != nil {
		return model.APIDoc{}, nil, "", "", err
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

4. **Event Payload Rules**
   - If user asks for *event* payload or mentions "event":
     - Populate inside 'payload -> event'
     - For example, if user says "create event payload with id and type", then include:
       "payload": {
         "event": [
           {
             "id": "dummy",
             "type": "dummy",
             "eventType": "dummy"
           }
         ]
       }

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

7. **Unknown Fields Handling**
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


8. **If the user provides no field**
   - Return nothing (no payload at all).

9. **Context Flags**
   - If user mentions “UMI compliant” → set 'isUMICompliant': true in context'.
   - If user mentions “async” → set 'isAsync': true 'in context, else false'.
   - If not mentioned, omit these fields entirely.

10. **Follow-up Questions (for Interactivity)**
   - If user query is vague (e.g., “I want to create asset”), respond with brief clarification questions such as:
     - “Would you like it to be UMI compliant?”
     - “Should this be created in async mode?”
     - “Please provide a few field names to include in the payload.”
   - Wait for user’s response before generating final payload.

11. **Minimum Required Information (New Rule)**
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
		return chosen, picked, "", "", err
	}

	samplePayload := strings.TrimSpace(payloadResp)

	return chosen, picked, samplePayload, "", nil
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

	pickPrompt := fmt.Sprintf(`You are selecting the best API for the user's request.

APIs:
%s

User request: %q

Return ONLY valid JSON with shape: {"api_index": <int>}
`, strings.Join(apiSummaries, "\n"), user)

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

4. **Event Payload Rules**
   - If user asks for *event* payload or mentions "event":
     - Populate inside 'payload -> event'
     - For example, if user says "create event payload with id and type", then include:
       "payload": {
         "event": [
           {
             "id": "dummy",
             "type": "dummy",
             "eventType": "dummy"
           }
         ]
       }

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

7. **Unknown Fields Handling**
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


8. **If the user provides no field**
   - Return nothing (no payload at all).

9. **Context Flags**
   - If user mentions "UMI compliant" → set 'isUMICompliant': true in context'.
   - If user mentions "async" → set 'isAsync': true 'in context, else false'.
   - If not mentioned, omit these fields entirely.

---

### OUTPUT
Generate only the payload (JSON or XML as per user request). Add explanations, notes, or comments and ask questions as well.
`, user, getRequestModelSnippet(), chosen.Method, chosen.Path)

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
	IsAsync        *bool   // nil = unknown, true/false = known
	IsUMICompliant *bool   // nil = unknown, true/false = known
	IsPrivate      *bool   // nil = unknown, true = private, false = public
	FieldNames     []string // empty = no fields provided
	EventFields    []string // fields for event payload (when async is true)
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
1. Is this asking to CREATE something (e.g., "I want to create a gold bond", "create asset", "make a transaction", "burn asset")
2. Is this asking ABOUT a field or property (e.g., "what is toWalletAddress?", "explain id field", "what does async mean?")
3. Is this providing answers to previous questions (e.g., "yes", "no", "async", "private", field names like "id", "value")

IMPORTANT: If the user is providing answers to follow-up questions (like "yes", "no", "async", "private", or field names), 
this is STILL a creation request continuation, NOT a field question.

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
- If asking to create/make/generate/burn/lock → is_creation_request = true, is_relevant = true
- If providing answers to questions (yes/no/field names) → is_creation_request = true, is_relevant = true
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
	contextToUse := ""
	if isNewRequest {
		// For new requests, completely ignore history - start fresh
		contextToUse = ""
	} else {
		// For continuation of same request, use recent context (last 4-5 messages)
		contextToUse = getRecentHistory(history, 5)
	}
	
	// Build context message
	contextMsg := ""
	if contextToUse == "" {
		contextMsg = "Previous conversation context: IGNORE - this is a new request, start fresh."
	} else {
		contextMsg = fmt.Sprintf("Recent conversation context: %s\n\nNOTE: Only extract from the CURRENT request, not from previous unrelated requests.", contextToUse)
	}
	
	extractionPrompt := fmt.Sprintf(`Analyze ONLY the current creation request and extract the following information:

Current user query: %q
%s

CRITICAL RULES:
- If this is a NEW creation request (like "create gold bond" or "burn asset"), ONLY extract information from the current query.
- IGNORE all information from previous requests in the conversation.
- Each new creation request must start fresh - do NOT reuse async/UMI/private/public/fields from previous requests.

Extract:
1. Is it async? (look for "async", "asynchronous", "yes/no" answers ONLY in current query)
2. Is it UMI compliant? (look for "UMI compliant", "UMI", "yes/no" answers ONLY in current query)
3. Is it private or public? (look for "private", "public", "yes/no" answers ONLY in current query)
4. Field names mentioned for main payload (any field names like id, value, key, toWalletAddress, etc. ONLY in current query)
5. Event field names mentioned (if async is true, look for event-related fields like id, type, eventType, timestamp, etc. ONLY in current query)

Return ONLY a JSON object:
{
  "is_async": true/false/null,
  "is_umi_compliant": true/false/null,
  "is_private": true/false/null,
  "field_names": ["field1", "field2", ...],
  "event_fields": ["eventField1", "eventField2", ...]
}

Use null if the information is not found or unclear in the CURRENT query only. Do NOT use information from previous requests.
For event_fields, only include if is_async is true or if user mentions event fields.`, userInput, contextMsg)

	response, err := llms.GenerateFromSinglePrompt(ctx, llm, extractionPrompt, llms.WithTemperature(0.0))
	if err != nil {
		// Fallback extraction
		return extractQueryInfoFallback(userInput, contextToUse), nil
	}

	var result struct {
		IsAsync        *bool    `json:"is_async"`
		IsUMICompliant *bool    `json:"is_umi_compliant"`
		IsPrivate      *bool    `json:"is_private"`
		FieldNames     []string `json:"field_names"`
		EventFields    []string `json:"event_fields"`
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

	info := &QueryInfo{
		IsAsync:        result.IsAsync,
		IsUMICompliant: result.IsUMICompliant,
		IsPrivate:      result.IsPrivate,
		FieldNames:     result.FieldNames,
		EventFields:    result.EventFields,
	}
	
	// If extraction failed, use fallback
	if info.IsAsync == nil && info.IsUMICompliant == nil && info.IsPrivate == nil && len(info.FieldNames) == 0 {
		fallbackInfo := extractQueryInfoFallback(userInput, contextToUse)
		if fallbackInfo != nil {
			info = fallbackInfo
		}
	}
	
	return info, nil
}

// extractQueryInfoFallback provides fallback extraction logic
func extractQueryInfoFallback(userInput, context string) *QueryInfo {
	info := &QueryInfo{}
	// Only use context if it's not empty (for continuation), otherwise only use current input
	textToAnalyze := userInput
	if context != "" {
		textToAnalyze = userInput + " " + context
	}
	lower := strings.ToLower(textToAnalyze)
	
	// Check for async
	if strings.Contains(lower, "async") || strings.Contains(lower, "asynchronous") {
		asyncFalse := strings.Contains(lower, "not async") || 
			strings.Contains(lower, "no async") || 
			strings.Contains(lower, "async: no") ||
			strings.Contains(lower, "async=false") ||
			strings.Contains(lower, "async no")
		if asyncFalse {
			asyncFalseVal := false
			info.IsAsync = &asyncFalseVal
		} else {
			asyncTrue := true
			info.IsAsync = &asyncTrue
		}
	}
	
	// Check for UMI compliant
	if strings.Contains(lower, "umi compliant") || strings.Contains(lower, "umi-compliant") || 
		(strings.Contains(lower, "umi") && !strings.Contains(lower, "explain")) {
		umiFalse := strings.Contains(lower, "not umi") || 
			strings.Contains(lower, "no umi") || 
			strings.Contains(lower, "umi: no") ||
			strings.Contains(lower, "umi=false") ||
			strings.Contains(lower, "umi no")
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
	
	// Extract field names - be more careful
	commonFields := []string{"id", "value", "key", "toWalletAddress", "fromWalletAddress", 
		"walletAddress", "requestId", "msgId", "name", "type", "event", "eventType"}
	for _, field := range commonFields {
		// Check if field is mentioned as a field name, not just in explanation
		if strings.Contains(lower, field) && !strings.Contains(lower, "explain "+field) &&
			!strings.Contains(lower, "what is "+field) {
			info.FieldNames = append(info.FieldNames, field)
		}
	}
	
	return info
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
	
	// If async is true, check if event fields are provided
	if info.IsAsync != nil && *info.IsAsync && len(info.EventFields) == 0 {
		missing = append(missing, "Since this is an async request, please provide at least one field name for the event payload (e.g., id, type, eventType, timestamp, etc.)")
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
	
	// Check for async field question
	if strings.Contains(lower, "async") && (strings.Contains(lower, "what is") || 
		strings.Contains(lower, "explain") || strings.Contains(lower, "what does") ||
		strings.Contains(lower, "field")) {
		return "The **async** field (or **isAsync**) is a boolean flag in the request context that indicates whether the API request should be processed asynchronously. When set to `true`, the request is processed asynchronously, meaning the API will return immediately and process the request in the background. When set to `false` or omitted, the request is processed synchronously, meaning the API will wait for the operation to complete before returning a response.", nil
	}
	
	// Don't use history for field questions - answer based on current question only
	// This prevents confusion from previous questions
	answerPrompt := fmt.Sprintf(`You are a helpful API documentation assistant. The user is asking about a field or property.

User question: %q

IMPORTANT RULES:
- If the user asks about "UMI" or "UMI compliant", explain that UMI stands for "Unified Market Interface" and it's a compliance standard.
- If the user asks about "async" or "isAsync", explain it's a boolean flag for asynchronous processing.
- Answer ONLY the current question. Do NOT reference previous questions or answers.
- Answer the question clearly and concisely.
- Do NOT suggest any APIs or generate payloads.
- Just explain what the field is, what it does, or answer their question directly.

If you don't know the answer, say so politely.`, userInput)

	response, err := llms.GenerateFromSinglePrompt(ctx, llm, answerPrompt, llms.WithTemperature(0.3))
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(response), nil
}
