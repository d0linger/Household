package store

type PantryFoodFactsLookupInput struct {
	Generation    int64  `json:"generation"`
	Code          string `json:"code"`
	AllowExternal bool   `json:"allow_external"`
}

type PantryFoodFactsSource struct {
	Provider        string `json:"provider"`
	Code            string `json:"code"`
	Name            string `json:"name"`
	Brand           string `json:"brand"`
	PackDescription string `json:"pack_description"`
	URL             string `json:"url"`
	DatabaseLicense string `json:"database_license"`
	ContentsLicense string `json:"contents_license"`
	FetchedAt       string `json:"fetched_at"`
	ExpiresAt       string `json:"expires_at"`
	PayloadSHA256   string `json:"payload_sha256"`
	Fingerprint     string `json:"fingerprint"`
}

type PantryFoodFactsLookup struct {
	Generation int64                  `json:"generation"`
	Code       string                 `json:"code"`
	State      string                 `json:"state"`
	Message    string                 `json:"message"`
	FromCache  bool                   `json:"from_cache"`
	Stale      bool                   `json:"stale"`
	RetryAt    string                 `json:"retry_at,omitempty"`
	Source     *PantryFoodFactsSource `json:"source,omitempty"`
	Local      Record                 `json:"local,omitempty"`
}

type PantryFoodFactsInput struct {
	Generation        int64    `json:"generation"`
	Code              string   `json:"code"`
	Provider          string   `json:"provider"`
	CacheFingerprint  string   `json:"cache_fingerprint"`
	SelectedFields    []string `json:"selected_fields"`
	ProductID         string   `json:"product_id"`
	ProductVersion    int64    `json:"product_version"`
	ProductName       string   `json:"product_name"`
	Unit              string   `json:"unit"`
	IntegerQuantities bool     `json:"integer_quantities"`
	CreatePack        bool     `json:"create_pack"`
	PackLabel         string   `json:"pack_label"`
	PackQuantity      string   `json:"pack_quantity"`
	PackUnit          string   `json:"pack_unit"`
}

type PantryFoodFactsReview struct {
	Input       PantryFoodFactsInput  `json:"input"`
	Fingerprint string                `json:"fingerprint"`
	Source      PantryFoodFactsSource `json:"source"`
	Product     Record                `json:"product"`
	Pack        Record                `json:"pack,omitempty"`
	Effects     []string              `json:"effects"`
}

type PantryFoodFactsReceipt struct {
	OperationID    string                `json:"operation_id"`
	Generation     int64                 `json:"generation"`
	AcceptedAt     string                `json:"accepted_at"`
	ProductID      string                `json:"product_id"`
	PackID         string                `json:"pack_id,omitempty"`
	BarcodeID      string                `json:"barcode_id"`
	ProvenanceID   string                `json:"provenance_id"`
	Source         PantryFoodFactsSource `json:"source"`
	SelectedFields []string              `json:"selected_fields"`
	Message        string                `json:"message"`
}

type PantryFoodFactsOperation struct {
	State   string                  `json:"state"`
	Message string                  `json:"message"`
	Receipt *PantryFoodFactsReceipt `json:"receipt,omitempty"`
}

type PantryFoodFactsProvenanceView struct {
	Sources []PantryFoodFactsSource `json:"sources"`
	More    bool                    `json:"more"`
}
