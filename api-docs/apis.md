### Manage
**Path:** /umi/v1/ReqManage  
**Method:** POST  
**Description:** Manage method will process the request based on the type and action and return error if any. This will be used to manage assets like lock, unlock, burn the assets on DLT.  
**Fields:**
- name: manage  type: xml  description: manage payload

---

### Issue
**Path:** /umi/v1/ReqIssue  
**Method:** POST  
**Description:** Issue method will process the request based on the type and action and return error if any. This will be used to issue/create new assets on DLT.  
**Fields:**
- name: issue  type: xml  description: issue payload

---

### Settle
**Path:** /umi/v1/ReqSettle  
**Method:** POST  
**Description:** Settle method will process the request based on the requestType and requestAction and return error if any. This will be used for inter-network asset trade custom instructions in coordinator. It will be used to transfer one asset from one organization to another.  
**Fields:**
- name: settle  type: xml  description: settle payload

---

### Transact
**Path:** /umi/v1/ReqTransact  
**Method:** POST  
**Description:** Transact method will process the request based on the type and action and return error if any. This will be used to create transactions on DLT.  
**Fields:**
- name: transact  type: xml  description: transact payload

---

### Query
**Path:** /umi/v1/ReqQuery  
**Method:** POST  
**Description:** Query method will fetch data from chaincode and return the response. This will support both normal query and rich query on DLT.  
**Fields:**
- name: query  type: xml  description: query payload

---

### Healthz
**Path:** /umi/v1/Healthz  
**Method:** GET  
**Description:** Healthz method will return 200 status code if the FSP service is up.

---

### Message
**Path:** /umi/v1/ReqMessage 
**Method:** POST  
**Description:** Message method will process the request based on the requestType and requestAction and return error if any.
This will be used for inter network asset trade using bridge. If trade is happening between two different networks then bridge is there for communicating between the two networks so whenever bridge is getting called Message will be called. 
**Fields:**
- name: message  type: xml  description: message payload

---

### CreateTemplates
**Path:** /umi/v1/ReqTemplates 
**Method:** POST  
**Description:** CreateTemplates method will process the request based on the requestType and requestAction and return error if any. This will be used for creating the template. 
**Fields:**
- name: createTemplate  type: xml  description: create template payload

---

### DeleteTemplates
**Path:** /umi/v1/ReqTemplateById
**Method:** DELETE  
**Description:** DeleteTemplates method will process the request based on the requestType and requestAction and return error if any. This will be used for deleting the template.
**Fields:**
- name: deleteTemplate  type: xml  description: delete template payload

---

### GetTemplates
**Path:** /umi/v1/ReqTemplates
**Method:** GET  
**Description:** GetTemplates method will process the request to retrieve templates based on the provided parameters and return the response. This method is responsible for validating the request parameters, setting up the request context, and invoking the service to fetch the templates.
**Fields:**
- name: getTemplates  type: xml  description: get templates payload

---

### GetTemplateById
**Path:** /umi/v1/ReqTemplateById
**Method:** GET  
**Description:** GetTemplateById method will process the request to retrieve a template by its ID and return the response. This method is responsible for validating the request parameters, setting up the request context, and invoking the service to fetch the template.
**Fields:**
- name: getTemplateById  type: xml  description: get template by id payload

---

### CreateOffers
**Path:** /umi/v1/ReqOffers 
**Method:** POST  
**Description:** CreateOffers method will process the request based on the requestType and requestAction and return error if any. This will be used for creating the offer from the template. 
**Fields:**
- name: createOffer  type: xml  description: create offer payload

---

### DeleteOffers
**Path:** /umi/v1/ReqOffers 
**Method:** DELETE  
**Description:** DeleteOffers method will process the request based on the requestType and requestAction and return error if any. This will be used for deleting the offer from the template.
**Fields:**
- name: deleteOffer  type: xml  description: delete offer payload

---

### UpdateOffer
**Path:** /umi/v1/ReqOffers
**Method:** PUT  
**Description:** UpdateOffer method will process the request based on the requestType and requestAction and return error if any. This will be used for updating the offer.
**Fields:**
- name: updateOffer  type: xml  description: delete offer payload

---

### Validate
**Path:** /umi/v1/ReqValidate
**Method:** POST  
**Description:** This method validates the voucher token.
**Fields:**
- name: validate  type: xml  description: validate payload

---

### GetOffers
**Path:** /umi/v1/ReqOffers 
**Method:** GET  
**Description:** GetOffers method will process the request to retrieve offers based on the provided parameters and return the response. This method is responsible for validating the request parameters, setting up the request context, and invoking the service to fetch the offers.
**Fields:**
- name: getOffers  type: xml  description: get offers payload

---

### UANS
**Path:** /umi/v1/ReqUANS
**Method:** POST  
**Description:** The UANS method is used to handle specific operations based on the provided parameters. It takes a variadic number of parameters to customize the query and returns an error if one occurs. It has a query method which is used to perform a query operation based on the provided parameters.
**Fields:**
- name: reqUANS  type: xml  description: uans payload
