// Package extraction turns receipt photos into structured, merchant-agnostic receipts using a
// pluggable vision-LLM Provider (Grok/xAI by default; Anthropic stub), selected by config.
package extraction

import (
	"context"
	"encoding/json"
	"errors"
)

// PromptVersion is stored on source_documents so extractions can be re-run/compared.
const PromptVersion = "receipt-v1"

// ErrNotImplemented is returned by providers that are wired but not built yet.
var ErrNotImplemented = errors.New("extraction provider not implemented")

// Image is one photo of a receipt.
type Image struct {
	MIME string
	Data []byte
}

// Provider extracts a receipt from one or more photos.
type Provider interface {
	Name() string
	Model() string
	Extract(ctx context.Context, images []Image) (*Result, error)
}

// Result is a provider's parsed output plus the raw JSON it returned.
type Result struct {
	Receipt *Extracted
	Raw     json.RawMessage
	Model   string
}

// Extracted is the provider-neutral extraction shape (matches ReceiptJSONSchema).
// Money is integer cents. Pointers distinguish "not printed / unreadable" (null) from 0.
type Extracted struct {
	MerchantName  *string           `json:"merchant_name"`
	StoreNumber   *string           `json:"store_number"`
	StoreAddress  *string           `json:"store_address"`
	StorePhone    *string           `json:"store_phone"`
	PurchaseDate  *string           `json:"purchase_date"` // YYYY-MM-DD
	PurchaseTime  *string           `json:"purchase_time"` // HH:MM (24h), optional
	Currency      *string           `json:"currency"`
	SubtotalCents *int64            `json:"subtotal_cents"`
	TaxCents      *int64            `json:"tax_cents"`
	TotalCents    *int64            `json:"total_cents"`
	SavingsCents  *int64            `json:"savings_cents"`
	PaymentMethod *string           `json:"payment_method"`
	PaymentLast4  *string           `json:"payment_last4"`
	Confidence    *float64          `json:"confidence"`
	Items         []ExtractedItem   `json:"items"`
	Adjustments   []ExtractedAdjust `json:"adjustments"`
}

type ExtractedItem struct {
	LineNo         *int     `json:"line_no"`
	RawText        *string  `json:"raw_text"`
	Name           string   `json:"name"`
	SKU            *string  `json:"sku"`
	ItemNumber     *string  `json:"item_number"`
	UPC            *string  `json:"upc"`
	Quantity       *float64 `json:"quantity"`
	Unit           *string  `json:"unit"`
	UnitPriceCents *int64   `json:"unit_price_cents"`
	LineTotalCents *int64   `json:"line_total_cents"`
	DiscountCents  *int64   `json:"discount_cents"`
	TaxFlag        *string  `json:"tax_flag"`
	IsReturn       *bool    `json:"is_return"`
}

type ExtractedAdjust struct {
	Kind        string  `json:"kind"`
	RawText     *string `json:"raw_text"`
	AmountCents int64   `json:"amount_cents"`
}
