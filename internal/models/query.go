package models

// Read-only query results for the MCP tools (phase 5). Every result carries receipt ids and
// dates so an answer can be checked against the receipts (MCP-02).

// MerchantSummary is a merchant with its counted receipts (MCP-05).
type MerchantSummary struct {
	ID              int64  `json:"id"`
	Name            string `json:"name"` // normalized key, the one stored name
	ReceiptCount    int    `json:"receipt_count"`
	TotalSpentCents int64  `json:"total_spent_cents"`
	LastVisit       string `json:"last_visit,omitempty"`
	LastReceiptID   int64  `json:"last_receipt_id,omitempty"`
}

// MerchantCandidate is a fuzzy (pg_trgm) match for a merchant name with no exact match (MCP-06).
type MerchantCandidate struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	Similarity float64 `json:"similarity"`
}

// PurchaseLine is one line item on a counted receipt.
type PurchaseLine struct {
	ItemID         int64   `json:"item_id"`
	ReceiptID      int64   `json:"receipt_id"`
	PurchaseDate   string  `json:"purchase_date,omitempty"`
	ReceiptStatus  string  `json:"receipt_status"`
	MerchantID     int64   `json:"merchant_id"`
	MerchantName   string  `json:"merchant_name"`
	StoreID        *int64  `json:"store_id,omitempty"`
	StoreNumber    string  `json:"store_number,omitempty"`
	StoreAddress   string  `json:"store_address,omitempty"`
	ItemName       string  `json:"item_name,omitempty"`
	RawText        string  `json:"raw_text,omitempty"`
	ProductID      *int64  `json:"product_id,omitempty"`
	ProductName    string  `json:"product_name,omitempty"`
	Quantity       float64 `json:"quantity"`
	UnitPriceCents int64   `json:"unit_price_cents"`
	NetTotalCents  int64   `json:"net_total_cents"`
	IsReturn       bool    `json:"is_return,omitempty"`
}

// SpendGroup totals receipts grouped by merchant name or month.
type SpendGroup struct {
	Key          string  `json:"key"`
	ReceiptCount int     `json:"receipt_count"`
	TotalCents   int64   `json:"total_cents"`
	TaxCents     int64   `json:"tax_cents"`
	SavingsCents int64   `json:"savings_cents"`
	FirstDate    string  `json:"first_date,omitempty"`
	LastDate     string  `json:"last_date,omitempty"`
	ReceiptIDs   []int64 `json:"receipt_ids"` // newest first, capped
	// ReceiptIDsTruncated: the group has more receipts than receipt_ids lists.
	ReceiptIDsTruncated bool `json:"receipt_ids_truncated"`
}

// ProductLocation is one merchant + store where a product was bought (aggregated in SQL).
type ProductLocation struct {
	MerchantID          int64   `json:"merchant_id"`
	MerchantName        string  `json:"merchant_name"`
	StoreID             *int64  `json:"store_id,omitempty"`
	StoreNumber         string  `json:"store_number,omitempty"`
	StoreAddress        string  `json:"store_address,omitempty"`
	Purchases           int     `json:"purchases"`
	LastDate            string  `json:"last_date,omitempty"`
	LastUnitPriceCents  int64   `json:"last_unit_price_cents"`
	ReceiptIDs          []int64 `json:"receipt_ids"` // newest first, capped
	ReceiptIDsTruncated bool    `json:"receipt_ids_truncated"`
}

// WhereBought: every location (newest first) plus the newest purchases.
type WhereBought struct {
	Locations []ProductLocation `json:"locations"`
	Purchases []PurchaseLine    `json:"purchases"`
	Truncated bool              `json:"truncated"` // more purchases than listed
}
