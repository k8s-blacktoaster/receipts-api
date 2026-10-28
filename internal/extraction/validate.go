package extraction

import (
	"fmt"

	"receipts-db/internal/models"
)

// DefaultToleranceCents is the allowed rounding slack for totals checks.
const DefaultToleranceCents = 2

func within(a, b, tol int64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= tol
}

// Check returns review reasons for a mapped receipt (empty = OK to mark parsed).
// Required: merchant, date, total, at least one item. Totals: total must equal
// subtotal + tax - savings, or subtotal + tax + adjustments, or subtotal + tax (receipts differ
// in whether savings are already netted out of the subtotal), within tolerance.
func Check(r *models.Receipt, e *Extracted, tol int64) []string {
	var reasons []string
	if models.NormalizeMerchantName(r.StoreName) == "" {
		reasons = append(reasons, "missing merchant name")
	}
	if r.Date == "" {
		reasons = append(reasons, "missing or invalid purchase date")
	}
	if e == nil || e.TotalCents == nil {
		reasons = append(reasons, "missing total")
	}
	if e == nil || e.SubtotalCents == nil {
		reasons = append(reasons, "missing subtotal")
	}
	if len(r.Items) == 0 {
		reasons = append(reasons, "no line items")
	}
	for i, it := range r.Items {
		if it.Name == "" {
			reasons = append(reasons, fmt.Sprintf("item %d has no name", i+1))
		}
	}
	if e != nil && e.TotalCents != nil && e.SubtotalCents != nil {
		var adj int64
		for _, a := range r.Adjustments {
			adj += a.AmountCents
		}
		base := r.SubtotalCents + r.TaxCents
		ok := within(base-r.SavingsCents, r.TotalCents, tol) ||
			within(base+adj, r.TotalCents, tol) ||
			within(base, r.TotalCents, tol)
		if !ok {
			reasons = append(reasons, fmt.Sprintf(
				"total mismatch: subtotal %d + tax %d - savings %d (adjustments %d) = %d, printed total %d",
				r.SubtotalCents, r.TaxCents, r.SavingsCents, adj, base-r.SavingsCents, r.TotalCents))
		}
	}
	return reasons
}
