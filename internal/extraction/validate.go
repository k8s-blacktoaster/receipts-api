package extraction

import (
	"fmt"
	"regexp"
	"strings"

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
	} else if why := merchantLooksLikeLocation(r); why != "" {
		reasons = append(reasons, "merchant name "+strconvQuote(r.StoreName)+" looks like the store location ("+why+")")
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
	reasons = append(reasons, checkItems(r, tol)...)
	return reasons
}

// discountName matches item names that look like a discount/savings line rather than a product.
// It is applied to the name, and to raw_text only when the item carries no discount (a correct
// item's raw_text may legitimately include the discount line that was folded into it).
var discountName = regexp.MustCompile(`(?i)\b(savings?|discount|coupon|instant\s*sav|markdown|you\s*saved)\b|\d+[.,]\d{2}\s*-\s*$|(^|\s)/\s*\d+|^[\d\s/]+$`)

// checkItems reconciles line items against the subtotal and flags lines that look misread.
// Generic (merchant-agnostic): sum(line_total) - sum(discount) + adjustments must equal the
// subtotal within tol (adjustments may also sit after the subtotal, so subtotal without them
// is accepted too, as is a pre-discount subtotal whose total then reflects the discounts).
func checkItems(r *models.Receipt, tol int64) []string {
	if len(r.Items) == 0 {
		return nil
	}
	var reasons []string
	var lines, disc, adj int64
	for i, it := range r.Items {
		lines += it.LineTotalCents
		disc += it.DiscountCents
		if !it.IsReturn && (it.LineTotalCents < 0 || it.UnitPriceCents < 0) {
			reasons = append(reasons, fmt.Sprintf("item %d %q has a negative price", i+1, it.Name))
		}
		if discountName.MatchString(it.Name) || (it.DiscountCents == 0 && discountName.MatchString(it.RawText)) {
			reasons = append(reasons, fmt.Sprintf("item %d %q looks like a discount line, not a product", i+1, it.Name))
		}
	}
	for _, a := range r.Adjustments {
		adj += a.AmountCents
		if a.Kind == "deposit" || a.Kind == "fee" {
			for i, it := range r.Items {
				if it.LineTotalCents == a.AmountCents && sharesWord(it.Name+" "+it.RawText, a.RawText) {
					reasons = append(reasons, fmt.Sprintf("%s %d also appears as item %d %q (counted twice)", a.Kind, a.AmountCents, i+1, it.Name))
					break
				}
			}
		}
	}
	net := lines - disc
	// Some receipts print the subtotal before discounts and take them off after it; then the
	// total must show them (this keeps a discount that was also netted into line_total flagged).
	grossForm := disc+(-sumReducing(r)) > 0 && within(lines, r.SubtotalCents, tol) &&
		within(r.SubtotalCents+r.TaxCents-disc+sumReducing(r), r.TotalCents, tol)
	if r.SubtotalCents > 0 && !within(net+adj, r.SubtotalCents, tol) && !within(net, r.SubtotalCents, tol) && !grossForm {
		reasons = append(reasons, fmt.Sprintf(
			"items don't reconcile: line totals %d - discounts %d + adjustments %d = %d, printed subtotal %d (off by %d)",
			lines, disc, adj, net+adj, r.SubtotalCents, net+adj-r.SubtotalCents))
	}
	if r.SavingsCents > 0 && !within(r.SavingsCents, disc-sumReducing(r), tol) {
		for i, it := range r.Items {
			if it.LineTotalCents == r.SavingsCents {
				reasons = append(reasons, fmt.Sprintf("savings %d equals the price of item %d %q (likely copied, not a printed savings line)", r.SavingsCents, i+1, it.Name))
				break
			}
		}
	}
	return reasons
}

func sumReducing(r *models.Receipt) int64 {
	var s int64
	for _, a := range r.Adjustments {
		if a.AmountCents < 0 {
			s += a.AmountCents
		}
	}
	return s
}

func sharesWord(a, b string) bool {
	words := strings.FieldsFunc(strings.ToLower(b), func(c rune) bool { return !(c >= 'a' && c <= 'z') })
	a = strings.ToLower(a)
	for _, w := range words {
		if len(w) >= 3 && strings.Contains(a, w) {
			return true
		}
	}
	return false
}

var storeNumberLike = regexp.MustCompile(`#\s*\d+|(?i)\b(store|branch|location)\s*#?\s*\d+`)

// merchantLooksLikeLocation catches a branch line used as the merchant (generic: compares with
// the receipt's own store fields, no brand list). Matching is on whole normalized tokens, so
// "Safeway Oakland" (city Oakland) and "Target 123 Main St" (address "123 Main St, Ste 4") are
// flagged while "Oaklander Deli" is not.
func merchantLooksLikeLocation(r *models.Receipt) string {
	key := nameTokens((r.StoreName))
	if storeNumberLike.MatchString(r.StoreName) {
		return "contains a store number"
	}
	if city := nameTokens((r.StoreCity)); len(city) > 0 {
		if equalTokens(key, city) {
			return "same as the store city"
		}
		if containsTokens(key, city) {
			return "contains the store city"
		}
	}
	if r.StoreAddress != "" {
		addr := nameTokens((r.StoreAddress))
		if equalTokens(key, addr) {
			return "same as the store address"
		}
		street := nameTokens((strings.SplitN(r.StoreAddress, ",", 2)[0]))
		if len(street) >= 2 && containsTokens(key, street) {
			return "contains the store street address"
		}
	}
	return ""
}

// nameTokens splits a value into the merchant-key tokens (same normalization as stored names).
func nameTokens(s string) []string {
	return strings.FieldsFunc(models.NormalizeMerchantName(s), func(r rune) bool { return r == '_' })
}

func equalTokens(a, b []string) bool {
	return len(a) == len(b) && containsTokens(a, b)
}

// containsTokens reports whether needle appears as a contiguous run of whole tokens in hay.
func containsTokens(hay, needle []string) bool {
	if len(needle) == 0 || len(needle) > len(hay) {
		return false
	}
outer:
	for i := 0; i+len(needle) <= len(hay); i++ {
		for j := range needle {
			if hay[i+j] != needle[j] {
				continue outer
			}
		}
		return true
	}
	return false
}

func strconvQuote(s string) string { return fmt.Sprintf("%q", s) }
