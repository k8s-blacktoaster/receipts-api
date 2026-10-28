package extraction

import (
	"strings"
	"testing"

	"receipts-db/internal/models"
)

// Synthetic receipts (invented names/prices) modeled on the failure modes seen in the live
// golden run; none use real receipt text.
func item(name string, line, disc int64) models.ReceiptItem {
	return models.ReceiptItem{Name: name, Quantity: 1, UnitPriceCents: line, LineTotalCents: line, DiscountCents: disc}
}

func ptr(v int64) *int64 { return &v }

func checkOf(r *models.Receipt) []string {
	r.StoreName, r.Date = "Fake Warehouse", "2026-01-02"
	e := &Extracted{SubtotalCents: ptr(r.SubtotalCents), TotalCents: ptr(r.TotalCents)}
	return Check(r, e, 2)
}

func hasReason(rs []string, sub string) bool {
	for _, r := range rs {
		if strings.Contains(r, sub) {
			return true
		}
	}
	return false
}

func TestReconcileCleanReceiptPasses(t *testing.T) {
	r := &models.Receipt{SubtotalCents: 2000 + 1500 - 300 + 120, TaxCents: 100,
		Items: []models.ReceiptItem{item("WIDGET A", 2000, 300), item("GADGET B", 1500, 0), item("BOTTLE DEPOSIT", 120, 0)}}
	r.TotalCents = r.SubtotalCents + r.TaxCents
	if rs := checkOf(r); len(rs) != 0 {
		t.Fatalf("clean receipt flagged: %v", rs)
	}
}

// R1-like: the discount was netted into line_total AND recorded as discount_cents.
func TestReconcileDoubleCountedDiscount(t *testing.T) {
	// printed: WIDGET 20.00, "/ 123 3.00-", GADGET 15.00; subtotal 32.00
	r := &models.Receipt{SubtotalCents: 3200, TaxCents: 0, TotalCents: 3200,
		Items: []models.ReceiptItem{item("WIDGET", 1700, 300), item("GADGET", 1500, 0)}}
	rs := checkOf(r)
	if !hasReason(rs, "items don't reconcile") || !hasReason(rs, "off by -300") {
		t.Fatalf("double-counted discount not flagged: %v", rs)
	}
}

// R2-like: savings_cents copied from an item price; items otherwise correct.
func TestReconcileSavingsCopiedFromItem(t *testing.T) {
	r := &models.Receipt{SubtotalCents: 2000 + 2459 - 500, TaxCents: 0, SavingsCents: 2459,
		Items: []models.ReceiptItem{item("WIDGET", 2000, 500), item("GADGET", 2459, 0)}}
	r.TotalCents = r.SubtotalCents
	rs := checkOf(r)
	if !hasReason(rs, "savings 2459 equals the price of item 2") || hasReason(rs, "items don't reconcile") {
		t.Fatalf("copied savings: %v", rs)
	}
}

// R3-like: instant-savings lines as positive items, one item missing, deposit twice.
func TestReconcileSavingsAsItemsMissingItemDuplicateDeposit(t *testing.T) {
	// printed: WIDGET 20.00 (/ 3.00-), GADGET 15.00, SPROCKET 9.00, CA BOTTLE DEP 1.20; subtotal 42.20
	r := &models.Receipt{SubtotalCents: 2000 - 300 + 1500 + 900 + 120, TaxCents: 0,
		Items: []models.ReceiptItem{item("WIDGET", 2000, 0), item("INSTANT SAVINGS", 300, 0), item("GADGET", 1500, 0),
			item("CA BOTTLE DEP", 120, 0)},
		Adjustments: []models.ReceiptAdjustment{{Kind: "deposit", RawText: "CA BOTTLE DEP 1.20", AmountCents: 120}}}
	r.TotalCents = r.SubtotalCents
	rs := checkOf(r)
	for _, want := range []string{`"INSTANT SAVINGS" looks like a discount line`, "deposit 120 also appears as item 4", "items don't reconcile"} {
		if !hasReason(rs, want) {
			t.Errorf("missing reason %q in %v", want, rs)
		}
	}
}

func TestReconcileNegativeAndDiscountLookingNames(t *testing.T) {
	r := &models.Receipt{SubtotalCents: 1000, TaxCents: 0, TotalCents: 1000,
		Items: []models.ReceiptItem{item("WIDGET", 1300, 0), {Name: "OOPS", Quantity: 1, LineTotalCents: -300},
			item("/ 4455 3.00-", 0, 0), item("STORE COUPON", 0, 0), item("ITEM 2.50-", 0, 0)}}
	rs := checkOf(r)
	for _, want := range []string{`"OOPS" has a negative price`, `"/ 4455 3.00-" looks like a discount`, `"STORE COUPON" looks like`, `"ITEM 2.50-" looks like`} {
		if !hasReason(rs, want) {
			t.Errorf("missing %q in %v", want, rs)
		}
	}
	// "1234 / 1234" style names (only digits and slashes) are discount references too
	if rs := checkOf(&models.Receipt{SubtotalCents: 0, TotalCents: 0, Items: []models.ReceiptItem{item("1450796 / 1450796", 400, 0)}}); !hasReason(rs, "looks like a discount") {
		t.Errorf("digits/slash name not flagged: %v", rs)
	}
	// a correct item whose raw_text includes its folded-in discount line is not flagged
	ok := item("WIDGET", 2000, 300)
	ok.RawText = "WIDGET 20.00 / 4455 3.00-"
	if rs := checkOf(&models.Receipt{SubtotalCents: 1700, TotalCents: 1700, Items: []models.ReceiptItem{ok}}); len(rs) != 0 {
		t.Errorf("folded discount raw_text flagged: %v", rs)
	}
	// returns may be negative
	r2 := &models.Receipt{SubtotalCents: 700, TaxCents: 0, TotalCents: 700,
		Items: []models.ReceiptItem{item("WIDGET", 1000, 0), {Name: "RETURNED GADGET", Quantity: 1, LineTotalCents: -300, IsReturn: true}}}
	if rs := checkOf(r2); len(rs) != 0 {
		t.Fatalf("return flagged: %v", rs)
	}
}
