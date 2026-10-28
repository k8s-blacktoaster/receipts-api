package models

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"
)

// Money is stored and transported as integer cents (int64). Never use floats for money.

// Receipt source values (receipts.source).
const (
	SourcePhoto  = "photo"
	SourceManual = "manual"
)

// Receipt statuses (receipts.status) for the photo pipeline.
const (
	StatusUploaded    = "uploaded"
	StatusParsing     = "parsing"
	StatusParsed      = "parsed"
	StatusNeedsReview = "needs_review"
	StatusReviewed    = "reviewed"
	StatusFailed      = "failed"
)

var validStatuses = map[string]bool{
	StatusUploaded: true, StatusParsing: true, StatusParsed: true,
	StatusNeedsReview: true, StatusReviewed: true, StatusFailed: true,
}

// Member is a household member (purchaser / uploader / reviewer).
type Member struct {
	ID          int64     `json:"id"`
	DisplayName string    `json:"display_name"`
	Email       string    `json:"email,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Receipt is a single purchase from any merchant. The model is merchant-agnostic:
// StoreName identifies the merchant (chain); StoreNumber/StoreAddress identify the location.
type Receipt struct {
	ID         int    `json:"id"`
	MerchantID int64  `json:"merchant_id,omitempty"`
	StoreID    *int64 `json:"store_id,omitempty"`

	// Merchant / location as printed. Used to resolve merchant_id / store_id on write.
	StoreName     string `json:"store_name"`
	StoreCategory string `json:"store_category,omitempty"`
	StoreNumber   string `json:"store_number,omitempty"`
	StoreAddress  string `json:"store_address,omitempty"`
	StorePhone    string `json:"store_phone,omitempty"`
	// StoreCity/State/Zip live on the stores row (city, region, postal_code).
	StoreCity  string `json:"store_city,omitempty"`
	StoreState string `json:"store_state,omitempty"`
	StoreZip   string `json:"store_zip,omitempty"`

	Date        string     `json:"date"` // YYYY-MM-DD (local purchase date)
	PurchasedAt *time.Time `json:"purchased_at,omitempty"`

	SubtotalCents int64  `json:"subtotal_cents"`
	TaxCents      int64  `json:"tax_cents"`
	TotalCents    int64  `json:"total_cents"`
	SavingsCents  int64  `json:"savings_cents"`
	Currency      string `json:"currency,omitempty"`

	PaymentMethod string `json:"payment_method,omitempty"`
	PaymentLast4  string `json:"payment_last4,omitempty"`

	Source           string   `json:"source,omitempty"`
	ExternalID       *string  `json:"external_id,omitempty"`
	SourceDocumentID *int64   `json:"source_document_id,omitempty"`
	MemberID         *int64   `json:"member_id,omitempty"`      // whose purchase (nullable: legacy/unattributed)
	UploadedByID     *int64   `json:"uploaded_by_id,omitempty"` // who uploaded the photo
	Confidence       *float64 `json:"confidence,omitempty"`

	Status         string     `json:"status,omitempty"`
	ParseError     string     `json:"parse_error,omitempty"`
	ParseStartedAt *time.Time `json:"parse_started_at,omitempty"`
	ParsedAt       *time.Time `json:"parsed_at,omitempty"`
	ReviewedAt     *time.Time `json:"reviewed_at,omitempty"`
	ReviewedByID   *int64     `json:"reviewed_by_id,omitempty"`

	Items       []ReceiptItem       `json:"items,omitempty"`
	Adjustments []ReceiptAdjustment `json:"adjustments,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ReceiptItem is one printed line. SKU, ItemNumber and UPC are generic optional identifiers.
type ReceiptItem struct {
	ID          int     `json:"id,omitempty"`
	ReceiptID   int     `json:"-"`
	LineNo      *int    `json:"line_no,omitempty"`
	RawText     string  `json:"raw_text,omitempty"`
	SKU         string  `json:"sku,omitempty"`
	ItemNumber  string  `json:"item_number,omitempty"`
	UPC         string  `json:"upc,omitempty"`
	ProductID   *int64  `json:"product_id,omitempty"`
	Name        string  `json:"name"`
	Description string  `json:"description,omitempty"`
	Quantity    float64 `json:"quantity"` // fractional for weighted items (NUMERIC(12,3))
	Unit        string  `json:"unit,omitempty"`

	UnitPriceCents int64 `json:"unit_price_cents"`
	LineTotalCents int64 `json:"line_total_cents"` // printed extended price; derived if 0
	DiscountCents  int64 `json:"discount_cents"`   // instant savings / coupon on this line (positive)
	NetTotalCents  int64 `json:"net_total_cents"`  // read-only: line_total - discount

	Taxable  *bool  `json:"taxable,omitempty"`
	TaxFlag  string `json:"tax_flag,omitempty"`
	IsReturn bool   `json:"is_return,omitempty"`

	EditedAt *time.Time `json:"edited_at,omitempty"` // set when changed during review

	CreatedAt time.Time `json:"created_at,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// ReceiptAdjustment is a receipt-level coupon, instant savings, deposit, fee or tip.
type ReceiptAdjustment struct {
	ID          int    `json:"id,omitempty"`
	ReceiptID   int    `json:"-"`
	AppliesToID *int   `json:"applies_to_item_id,omitempty"`
	Kind        string `json:"kind"`
	RawText     string `json:"raw_text,omitempty"`
	AmountCents int64  `json:"amount_cents"` // negative reduces the total
}

var validAdjustmentKinds = map[string]bool{
	"instant_savings": true, "coupon": true, "discount": true, "deposit": true,
	"fee": true, "tip": true, "other": true,
}

// ComputedLineTotalCents returns round(quantity * unit_price) in cents.
func (it ReceiptItem) ComputedLineTotalCents() int64 {
	return int64(math.Round(it.Quantity * float64(it.UnitPriceCents)))
}

// Normalize fills defaults: currency, source, review state, line totals, net totals,
// and the receipt total (subtotal + tax) when it was not supplied.
func (r *Receipt) Normalize() {
	if r.Currency == "" {
		r.Currency = "USD"
	}
	if r.Source == "" {
		r.Source = SourceManual
	}
	if r.Status == "" {
		if r.Source == SourceManual {
			r.Status = StatusReviewed // hand-entered receipts need no review
		} else {
			r.Status = StatusUploaded
		}
	}
	for i := range r.Items {
		it := &r.Items[i]
		if it.LineTotalCents == 0 && it.UnitPriceCents != 0 {
			it.LineTotalCents = it.ComputedLineTotalCents()
		}
		it.NetTotalCents = it.LineTotalCents - it.DiscountCents
	}
	if r.TotalCents == 0 && (r.SubtotalCents != 0 || r.TaxCents != 0) {
		r.TotalCents = r.SubtotalCents + r.TaxCents
	}
}

// ItemsNetCents sums net line totals plus receipt-level adjustments.
func (r *Receipt) ItemsNetCents() int64 {
	var sum int64
	for _, it := range r.Items {
		sum += it.LineTotalCents - it.DiscountCents
	}
	for _, a := range r.Adjustments {
		if a.Kind != "tip" && a.Kind != "fee" && a.Kind != "deposit" {
			sum += a.AmountCents
		}
	}
	return sum
}

// Validate checks structural rules (not arithmetic reconciliation; that is phase 2 review logic).
func (r *Receipt) Validate() error {
	if r.StoreName == "" {
		return errors.New("Store name is required")
	}
	if NormalizeMerchantName(r.StoreName) == "" {
		return errors.New("Store name must contain a letter or digit")
	}
	if r.Date == "" {
		return errors.New("Date is required")
	}
	if _, err := time.Parse("2006-01-02", r.Date); err != nil {
		return errors.New("Date must be YYYY-MM-DD")
	}
	if r.SubtotalCents < 0 {
		return errors.New("Subtotal cannot be negative")
	}
	if r.TaxCents < 0 {
		return errors.New("Tax cannot be negative")
	}
	if r.SavingsCents < 0 {
		return errors.New("Savings cannot be negative")
	}
	if r.Source != "" && r.Source != SourcePhoto && r.Source != SourceManual {
		return fmt.Errorf("Source must be %q or %q", SourcePhoto, SourceManual)
	}
	if r.Status != "" && !validStatuses[r.Status] {
		return errors.New("Invalid status")
	}
	for i, it := range r.Items {
		if it.Name == "" {
			return fmt.Errorf("Item name is required for item %d", i+1)
		}
		if it.Quantity <= 0 {
			return fmt.Errorf("Item quantity must be positive for item %d", i+1)
		}
		if it.UnitPriceCents < 0 && !it.IsReturn {
			return fmt.Errorf("Item unit price cannot be negative for item %d", i+1)
		}
		if it.DiscountCents < 0 {
			return fmt.Errorf("Item discount cannot be negative for item %d", i+1)
		}
	}
	for i, a := range r.Adjustments {
		if !validAdjustmentKinds[a.Kind] {
			return fmt.Errorf("Invalid adjustment kind for adjustment %d", i+1)
		}
	}
	return nil
}

// Product is a canonical, merchant-independent product.
type Product struct {
	ID            int64     `json:"id"`
	CanonicalName string    `json:"canonical_name"`
	Brand         string    `json:"brand,omitempty"`
	Category      string    `json:"category,omitempty"`
	UPC           string    `json:"upc,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

// ProductAlias maps how one merchant prints a product to the canonical product.
// Unique per (merchant, alias text) and per (merchant, item number).
type ProductAlias struct {
	ID         int64    `json:"id"`
	ProductID  int64    `json:"product_id"`
	MerchantID int64    `json:"merchant_id"`
	AliasText  string   `json:"alias_text"`
	ItemNumber string   `json:"item_number,omitempty"`
	Source     string   `json:"source"`
	Confidence *float64 `json:"confidence,omitempty"`
}

// PurchaseLocation answers "where did I buy X".
type PurchaseLocation struct {
	ProductID      int64   `json:"product_id"`
	ProductName    string  `json:"product_name"`
	MerchantID     int64   `json:"merchant_id"`
	MerchantName   string  `json:"merchant_name"`
	StoreID        *int64  `json:"store_id,omitempty"`
	StoreNumber    string  `json:"store_number,omitempty"`
	StoreAddress   string  `json:"store_address,omitempty"`
	ReceiptID      int     `json:"receipt_id"`
	PurchaseDate   string  `json:"purchase_date"`
	Quantity       float64 `json:"quantity"`
	UnitPriceCents int64   `json:"unit_price_cents"`
	NetTotalCents  int64   `json:"net_total_cents"`
}

// SourceDocument is an uploaded photo (or other input) and its raw extraction.
type SourceDocument struct {
	ID         int64  `json:"id"`
	Kind       string `json:"kind"`
	ObjectKey  string `json:"object_key,omitempty"`
	SHA256     []byte `json:"-"` // of the original upload (dedupe)
	MimeType   string `json:"mime_type,omitempty"`
	JPEGSHA256 []byte `json:"-"` // of the stored normalized JPEG
	// OriginalObjectKey/MimeType are set only with KEEP_ORIGINAL_UPLOADS=true.
	OriginalObjectKey string          `json:"original_object_key,omitempty"`
	OriginalMimeType  string          `json:"original_mime_type,omitempty"`
	Width, Height     int             `json:"-"`
	UploadedByID      *int64          `json:"uploaded_by_id,omitempty"`
	RawExtraction     json.RawMessage `json:"raw_extraction,omitempty"`
	Model             string          `json:"model,omitempty"`
	PromptVersion     string          `json:"prompt_version,omitempty"`
	ParseState        string          `json:"parse_state"`
	Error             string          `json:"error,omitempty"`
	ParsedAt          *time.Time      `json:"parsed_at,omitempty"`
	CreatedAt         time.Time       `json:"created_at"`
}

// UploadResult is returned by POST /uploads.
type UploadResult struct {
	ReceiptID        int    `json:"receipt_id"`
	SourceDocumentID int64  `json:"source_document_id"`
	Status           string `json:"status"`
	Duplicate        bool   `json:"duplicate"`
}

// Merchant is a normalized merchant (Name is the NormalizeMerchantName key).
type Merchant struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Category     string `json:"category,omitempty"`
	ReceiptCount int    `json:"receipt_count"`
}
