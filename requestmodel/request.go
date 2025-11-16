package requestmodel

import "encoding/xml"

type Request struct {
	XmlName     xml.Name
	XmlNs       string               `xml:"xmlns:token,attr"`
	Source      []BusinessIdentifier `json:"source,omitempty" xml:"Source>BusinessIdentifiers>BusinessIdentifier,omitempty"`
	Destination []BusinessIdentifier `json:"destination,omitempty" xml:"Destination>BusinessIdentifiers>BusinessIdentifier,omitempty"`
	Context     Context              `json:"context,omitempty" xml:"Context,omitempty"`
	Payload     Payload              `json:"payload,omitempty" xml:"Payload,omitempty"`
	Signature   string               `json:"signature,omitempty" xml:"signature,attr,omitempty"`
}

type BusinessIdentifier struct {
	Type        string    `json:"type,omitempty" xml:"type,attr,omitempty"`
	Id          string    `json:"id,omitempty" xml:"id,attr,omitempty"`
	PublicKey   string    `json:"publicKey,omitempty" xml:"publicKey,attr,omitempty"`
	Signature   string    `json:"signature,omitempty" xml:"signature,attr,omitempty"`
	CallbackUrl string    `json:"callbackUrl,omitempty" xml:"callbackUrl,attr,omitempty"`
	Account     []Account `json:"account,omitempty" xml:"Accounts>Account,omitempty"`
	Meta        Meta      `json:"meta,omitempty" xml:"Meta,omitempty"`
}

type Account struct {
	Type    string `json:"type,omitempty" xml:"type,attr,omitempty"`
	Address string `json:"address,omitempty" xml:"address,attr,omitempty"`
	VPA     string `json:"vpa,omitempty" xml:"vpa,attr,omitempty"`
}

type Context struct {
	RequestId         string `json:"requestId,omitempty" xml:"requestId,attr,omitempty"`
	MsgId             string `json:"msgId,omitempty" xml:"msgId,attr,omitempty"`
	IsAsync           bool   `json:"isAsync,omitempty" xml:"isAsync,attr,omitempty"`
	IsUMICompliant    bool   `json:"isUMICompliant,omitempty" xml:"isUMICompliant,attr,omitempty"`
	IdempotencyKey    string `json:"idempotencyKey,omitempty" xml:"idempotencyKey,attr,omitempty"`
	NetworkId         string `json:"networkId,omitempty" xml:"networkId,attr,omitempty"`
	WrapperContract   string `json:"wrapperContract,omitempty" xml:"wrapperContract,attr,omitempty"`
	ContractName      string `json:"contractName,omitempty" xml:"contractName,attr,omitempty"`
	MethodName        string `json:"methodName,omitempty" xml:"methodName,attr,omitempty"`
	Sender            string `json:"sender,omitempty" xml:"sender,attr,omitempty"`
	Receiver          string `json:"receiver,omitempty" xml:"receiver,attr,omitempty"`
	Timestamp         string `json:"timestamp,omitempty" xml:"timestamp,attr,omitempty"`
	Purpose           string `json:"purpose,omitempty" xml:"purpose,attr,omitempty"`
	ProdType          string `json:"prodType,omitempty" xml:"prodType,attr,omitempty"`
	Collection        string `json:"collection,omitempty" xml:"collection,attr,omitempty"`
	Type              string `json:"type,omitempty" xml:"type,attr,omitempty"`
	Version           string `json:"version,omitempty" xml:"version,attr,omitempty"`
	Subtype           string `json:"subtype,omitempty" xml:"subtype,attr,omitempty"`
	Action            string `json:"action,omitempty" xml:"action,attr,omitempty"`
	TraceDetails      string `json:"traceDetails,omitempty" xml:"traceDetails,attr,omitempty"`
	OriginalRequestId string `json:"originalRequestId,omitempty" xml:"originalRequestId,attr,omitempty"`
	OriginalTimestamp string `json:"originalTimestamp,omitempty" xml:"originalTimestamp,attr,omitempty"`
	SecureToken       string `json:"secureToken,omitempty" xml:"secureToken,attr,omitempty"`
	Status            string `json:"status,omitempty" xml:"status,attr,omitempty"`
	Code              string `json:"code,omitempty" xml:"code,attr,omitempty"`
	Meta              Meta   `json:"meta,omitempty" xml:"Meta,omitempty"`
}

type Payload struct {
	Type           string            `json:"type,omitempty" xml:"type,attr,omitempty"`
	TokenizedAsset *[]TokenizedAsset `json:"tokenizedAsset,omitempty" xml:"TokenizedAssets>TokenizedAsset,omitempty"`
	Transaction    *[]Transaction    `json:"transaction,omitempty" xml:"Transactions>Transaction,omitempty"`
	Identity       *[]Identity       `json:"identity,omitempty" xml:"Identities>Identity,omitempty"`
	KeyValue       *[]Detail         `json:"keyValue,omitempty" xml:"KeyValue>Detail,omitempty"`
	Meta           *Meta             `json:"meta,omitempty" xml:"Meta,omitempty"`
}

type Identity struct {
	Type                string `json:"type,omitempty" xml:"type,attr,omitempty"`
	Id                  string `json:"id,omitempty" xml:"id,attr,omitempty"`
	Category            string `json:"category,omitempty" xml:"category,attr,omitempty"`
	CreationTimestamp   string `json:"creationTimestamp,omitempty" xml:"creationTimestamp,attr,omitempty"`
	LastUpdateTimestamp string `json:"lastUpdateTimestamp,omitempty" xml:"lastUpdateTimestamp,attr,omitempty"`
	Status              string `json:"status,omitempty" xml:"status,attr,omitempty"`
	Issuer              string `json:"issuer,omitempty" xml:"issuer,attr,omitempty"`
	EntityType          string `json:"entityType,omitempty" xml:"entityType,attr,omitempty"`
	Password            string `json:"password,omitempty" xml:"password,attr,omitempty"`
	Alias               string `json:"alias,omitempty" xml:"alias,attr,omitempty"`
	NetworkAlias        string `json:"networkAlias,omitempty" xml:"networkAlias,attr,omitempty"`
	OrganisationAlias   string `json:"organisationAlias,omitempty" xml:"organisationAlias,attr,omitempty"`
	Certificate         string `json:"certificate,omitempty" xml:"certificate,attr,omitempty"`
	Endpoint            string `json:"endpoint,omitempty" xml:"endpoint,attr,omitempty"`
	BridgeAlias         string `json:"bridgeAlias,omitempty" xml:"bridgeAlias,attr,omitempty"`
	NetId               string `json:"netId,omitempty" xml:"netId,attr,omitempty"`
	Layer               string `json:"layer,omitempty" xml:"layer,attr,omitempty"`
	CustodyType         string `json:"custodyType,omitempty" xml:"custodyType,attr,omitempty"`
}

type TokenizedAsset struct {
	Version           string `json:"version,omitempty" xml:"version,attr,omitempty"`
	Id                string `json:"id,omitempty" xml:"id,attr,omitempty"`
	Value             string `json:"value,omitempty" xml:"value,attr,omitempty"`
	Unit              string `json:"unit,omitempty" xml:"unit,attr,omitempty"`
	CreationTimestamp string `json:"creationTimestamp,omitempty" xml:"creationTimestamp,attr,omitempty"`
	IssuerSignature   string `json:"issuerSignature,omitempty" xml:"issuerSignature,attr,omitempty"`
	IssuerAddress     string `json:"issuerAddress,omitempty" xml:"issuerAddress,attr,omitempty"`
	CustodianAddress  string `json:"custodianAddress,omitempty" xml:"custodianAddress,attr,omitempty"`
	OwnerAddress      string `json:"ownerAddress,omitempty" xml:"ownerAddress,attr,omitempty"`
	Type              string `json:"type,omitempty" xml:"type,attr,omitempty"`
	SerialNumber      string `json:"serialNumber,omitempty" xml:"serialNumber,attr,omitempty"`
	Tag               string `json:"tag,omitempty" xml:"tag,attr,omitempty"`
	Meta              *Meta  `json:"meta,omitempty" xml:"Meta,omitempty"`
	ParentId          string `json:"parentId,omitempty" xml:"parentId,attr,omitempty"`
	Status            string `json:"status,omitempty" xml:"status,attr,omitempty"`
}

type Transaction struct {
	Id                     string `json:"id,omitempty" xml:"id,attr,omitempty"`
	Type                   string `json:"type,omitempty" xml:"type,attr,omitempty"`
	Category               string `json:"category,omitempty" xml:"category,attr,omitempty"`
	CreationTimestamp      string `json:"creationTimestamp,omitempty" xml:"creationTimestamp,attr,omitempty"`
	Status                 string `json:"status,omitempty" xml:"status,attr,omitempty"`
	PublisherName          string `json:"publisherName,omitempty" xml:"publisherName,attr,omitempty"`
	PublisherVPA           string `json:"publisherVPA,omitempty" xml:"publisherVPA,attr,omitempty"`
	PublisherWalletAddress string `json:"publisherWalletAddress,omitempty" xml:"publisherWalletAddress,attr,omitempty"`
	PublisherSignature     string `json:"publisherSignature,omitempty" xml:"publisherSignature,attr,omitempty"`
	PublisherLogoURL       string `json:"publisherLogoUrl,omitempty" xml:"publisherLogoUrl,attr,omitempty"`
	TermsAndConditionsURL  string `json:"termsAndConditionsUrl,omitempty" xml:"termsAndConditionsUrl,attr,omitempty"`
	Data                   *Data  `json:"data,omitempty" xml:"Data,omitempty"`
}

type Data struct {
	Type           string            `json:"type,omitempty" xml:"type,attr,omitempty"`
	TokenizedAsset *[]TokenizedAsset `json:"tokenizedAsset,omitempty" xml:"TokenizedAssets>TokenizedAsset,omitempty"`
	KeyValue       *[]Detail         `json:"keyValue,omitempty" xml:"KeyValue>Detail,omitempty"`
	Meta           *Meta             `json:"meta,omitempty" xml:"Meta,omitempty"`
}

type Meta struct {
	Name                       string   `json:"name,omitempty" xml:"name,attr,omitempty"`
	Tenure                     string   `json:"tenure,omitempty" xml:"tenure,attr,omitempty"`
	TenureUnit                 string   `json:"tenureUnit,omitempty" xml:"tenureUnit,attr,omitempty"`
	Interval                   string   `json:"interval,omitempty" xml:"interval,attr,omitempty"`
	IntervalUnit               string   `json:"intervalUnit,omitempty" xml:"intervalUnit,attr,omitempty"`
	Interest                   string   `json:"interest,omitempty" xml:"interest,attr,omitempty"`
	InterestUnit               string   `json:"interestUnit,omitempty" xml:"interestUnit,attr,omitempty"`
	TdsFee                     string   `json:"tdsFee,omitempty" xml:"tdsFee,attr,omitempty"`
	TdsFeeUnit                 string   `json:"tdsFeeUnit,omitempty" xml:"tdsFeeUnit,attr,omitempty"`
	PreMatureWithdrawalFee     string   `json:"preMatureWithdrawalFee,omitempty" xml:"preMatureWithdrawalFee,attr,omitempty"`
	PreMatureWithdrawalFeeUnit string   `json:"preMatureWithdrawalFeeUnit,omitempty" xml:"preMatureWithdrawalFeeUnit,attr,omitempty"`
	SwitchFee                  string   `json:"switchFee,omitempty" xml:"switchFee,attr,omitempty"`
	SwitchFeeUnit              string   `json:"switchFeeUnit,omitempty" xml:"switchFeeUnit,attr,omitempty"`
	InterestType               string   `json:"interestType,omitempty" xml:"interestType,attr,omitempty"`
	NomineeName                string   `json:"nomineeName,omitempty" xml:"nomineeName,attr,omitempty"`
	NomineeRelation            string   `json:"nomineeRelation,omitempty" xml:"nomineeRelation,attr,omitempty"`
	WalletAddress              string   `json:"walletAddress,omitempty" xml:"walletAddress,attr,omitempty"`
	ToWalletAddress            string   `json:"toWalletAddress,omitempty" xml:"toWalletAddress,attr,omitempty"`
	FromWalletAddress          string   `json:"fromWalletAddress,omitempty" xml:"fromWalletAddress,attr,omitempty"`
	ToCustodianAddress         string   `json:"toCustodianAddress,omitempty" xml:"toCustodianAddress,attr,omitempty"`
	FromCustodianAddress       string   `json:"fromCustodianAddress,omitempty" xml:"fromCustodianAddress,attr,omitempty"`
	Vpa                        string   `json:"vpa,omitempty" xml:"vpa,attr,omitempty"`
	ToVpa                      string   `json:"toVpa,omitempty" xml:"toVpa,attr,omitempty"`
	FromVpa                    string   `json:"fromVpa,omitempty" xml:"fromVpa,attr,omitempty"`
	UserVpa                    string   `json:"userVpa,omitempty" xml:"userVpa,attr,omitempty"`
	MarketplaceId              string   `json:"marketplaceId,omitempty" xml:"marketplaceId,attr,omitempty"`
	OrgId                      string   `json:"orgId,omitempty" xml:"orgId,attr,omitempty"`
	MspId                      string   `json:"mspId,omitempty" xml:"mspId,attr,omitempty"`
	RoutingMode                string   `json:"routingMode,omitempty" xml:"routingMode,attr,omitempty"`
	PaymentRefId               string   `json:"paymentRefId,omitempty" xml:"paymentRefId,attr,omitempty"`
	PaymentMsgId               string   `json:"paymentMsgId,omitempty" xml:"paymentMsgId,attr,omitempty"`
	PaymentVpa                 string   `json:"paymentVpa,omitempty" xml:"paymentVpa,attr,omitempty"`
	PaymentMode                string   `json:"paymentMode,omitempty" xml:"paymentMode,attr,omitempty"`
	PaymentDate                string   `json:"paymentDate,omitempty" xml:"paymentDate,attr,omitempty"`
	InterestAccrued            string   `json:"interestAccrued,omitempty" xml:"interestAccrued,attr,omitempty"`
	InterestAccruedUnit        string   `json:"interestAccruedUnit,omitempty" xml:"interestAccruedUnit,attr,omitempty"`
	InterestPaid               string   `json:"interestPaid,omitempty" xml:"interestPaid,attr,omitempty"`
	InterestPaidUnit           string   `json:"interestPaidUnit,omitempty" xml:"interestPaidUnit,attr,omitempty"`
	PayoutAmount               string   `json:"payoutAmount,omitempty" xml:"payoutAmount,attr,omitempty"`
	ClientId                   string   `json:"clientId,omitempty" xml:"ClientId,attr,omitempty"`
	SignalDetails              string   `json:"signalDetails,omitempty" xml:"signalDetails,attr,omitempty"`
	Id                         string   `json:"id,omitempty" xml:"id,attr,omitempty"`
	QueryType                  string   `json:"queryType,omitempty" xml:"queryType,attr,omitempty"`
	CollectionName             string   `json:"collectionName,omitempty" xml:"collectionName,attr,omitempty"`
	PayloadRequired            string   `json:"payloadRequired,omitempty" xml:"payloadRequired,attr,omitempty"`
	PayoutAmountUnit           string   `json:"payoutAmountUnit,omitempty" xml:"payoutAmountUnit,attr,omitempty"`
	Payload                    string   `json:"payload,omitempty" xml:"payload,attr,omitempty"`
	PayloadType                string   `json:"payloadType,omitempty" xml:"payloadType,attr,omitempty"`
	PaymentAmount              string   `json:"paymentAmount,omitempty" xml:"paymentAmount,attr,omitempty"`
	ValidTill                  string   `json:"validTill,omitempty" xml:"validTill,attr,omitempty"`
	TemplateId                 string   `json:"templateId,omitempty" xml:"templateId,attr,omitempty"`
	ExpiryDate                 string   `json:"expiryDate,omitempty" xml:"expiryDate,attr,omitempty"`
	UseCaseId                  string   `json:"useCaseId,omitempty" xml:"useCaseId,attr,omitempty"`
	LockedBy                   string   `json:"lockedBy,omitempty" xml:"lockedBy,attr,omitempty"`
	LockedFor                  string   `json:"lockedFor,omitempty" xml:"lockedFor,attr,omitempty"`
	Quantity                   string   `json:"quantity,omitempty" xml:"quantity,attr,omitempty"`
	ContentType                string   `json:"contentType,omitempty" xml:"contentType,attr,omitempty"`
	Details                    []Detail `json:"details,omitempty" xml:"Details>Detail,omitempty"`
}

type Detail struct {
	Name  string `json:"name,omitempty" xml:"name,attr,omitempty"`
	Value string `json:"value,omitempty" xml:"value,attr,omitempty"`
}
