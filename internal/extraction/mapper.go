package extraction

import (
	"strings"
	"time"

	"receipts-db/internal/models"
)

func str(p *string) string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(*p)
}

func i64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

var reducing = map[string]bool{"instant_savings": true, "coupon": true, "discount": true}
var adding = map[string]bool{"deposit": true, "fee": true, "tip": true}

// Apply maps an extraction onto the merchant-agnostic receipt model, in place. It keeps the
// receipt's identity/provenance (id, source, source_document_id, uploader) and replaces the
// parsed fields, items and adjustments. Signs are normalized to the DB convention:
// item discount_cents and savings_cents positive; adjustments negative for reductions.
func Apply(r *models.Receipt, e *Extracted) {
	// keep the printed merchant name on the receipt (legacy store_name); the repository resolves
	// the merchant by models.NormalizeMerchantName, and Check flags names that normalize to "".
	r.StoreName = str(e.MerchantName)
	r.StoreNumber = str(e.StoreNumber)
	r.StoreAddress = str(e.StoreAddress)
	r.StorePhone = str(e.StorePhone)
	r.StoreCity, r.StoreState, r.StoreZip = str(e.StoreCity), strings.ToUpper(str(e.StoreState)), str(e.StoreZip)
	r.Date = ""
	if d := str(e.PurchaseDate); d != "" {
		if _, err := time.Parse("2006-01-02", d); err == nil {
			r.Date = d
			if t := str(e.PurchaseTime); t != "" {
				if ts, err := time.ParseInLocation("2006-01-02 15:04", d+" "+t, time.Local); err == nil {
					r.PurchasedAt = &ts
				}
			}
		}
	}
	if c := strings.ToUpper(str(e.Currency)); len(c) == 3 {
		r.Currency = c
	}
	r.SubtotalCents = abs(i64(e.SubtotalCents))
	r.TaxCents = abs(i64(e.TaxCents))
	r.TotalCents = abs(i64(e.TotalCents))
	r.SavingsCents = abs(i64(e.SavingsCents))
	r.PaymentMethod = str(e.PaymentMethod)
	if l4 := str(e.PaymentLast4); len(l4) == 4 {
		r.PaymentLast4 = l4
	} else {
		r.PaymentLast4 = ""
	}
	if e.Confidence != nil && *e.Confidence >= 0 && *e.Confidence <= 1 {
		c := *e.Confidence
		r.Confidence = &c
	}

	r.Items = r.Items[:0]
	for i, it := range e.Items {
		name := strings.TrimSpace(it.Name)
		if name == "" {
			name = str(it.RawText)
		}
		qty := 1.0
		if it.Quantity != nil && *it.Quantity > 0 {
			qty = *it.Quantity
		}
		isReturn := it.IsReturn != nil && *it.IsReturn
		line := i + 1
		if it.LineNo != nil {
			line = *it.LineNo
		}
		item := models.ReceiptItem{
			LineNo: &line, RawText: str(it.RawText), Name: name,
			SKU: str(it.SKU), ItemNumber: str(it.ItemNumber), UPC: models.CleanUPC(str(it.UPC)),
			Quantity: qty, Unit: str(it.Unit),
			UnitPriceCents: i64(it.UnitPriceCents), LineTotalCents: i64(it.LineTotalCents),
			DiscountCents: abs(i64(it.DiscountCents)), TaxFlag: str(it.TaxFlag), IsReturn: isReturn,
		}
		if !isReturn {
			item.UnitPriceCents, item.LineTotalCents = abs(item.UnitPriceCents), abs(item.LineTotalCents)
		}
		r.Items = append(r.Items, item)
	}

	r.Adjustments = r.Adjustments[:0]
	for _, a := range e.Adjustments {
		kind := strings.ToLower(strings.TrimSpace(a.Kind))
		amt := a.AmountCents
		switch {
		case reducing[kind]:
			amt = -abs(amt)
		case adding[kind]:
			amt = abs(amt)
		default:
			kind = "other"
		}
		r.Adjustments = append(r.Adjustments, models.ReceiptAdjustment{Kind: kind, RawText: str(a.RawText), AmountCents: amt})
	}
}
