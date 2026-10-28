package models

import "testing"

func TestNormalizeComputesTotalsInCents(t *testing.T) {
	r := Receipt{
		StoreName: "Home Depot", Date: "2026-10-01", SubtotalCents: 1097, TaxCents: 85,
		Items: []ReceiptItem{
			{Name: "Wood screws", Quantity: 3, UnitPriceCents: 199},             // 597
			{Name: "Bananas", Quantity: 1.42, UnitPriceCents: 69, Unit: "lb"},   // 97.98 -> 98
			{Name: "Tape", Quantity: 1, UnitPriceCents: 499, DiscountCents: 97}, // net 402
		},
	}
	r.Normalize()
	if got := r.Items[0].LineTotalCents; got != 597 {
		t.Fatalf("line0 = %d", got)
	}
	if got := r.Items[1].LineTotalCents; got != 98 {
		t.Fatalf("weighted line = %d", got)
	}
	if got := r.Items[2].NetTotalCents; got != 402 {
		t.Fatalf("net = %d", got)
	}
	if r.TotalCents != 1182 {
		t.Fatalf("total = %d, want subtotal+tax 1182", r.TotalCents)
	}
	if got := r.ItemsNetCents(); got != 597+98+402 {
		t.Fatalf("items net = %d", got)
	}
	if r.Currency != "USD" || r.Source != SourceManual || r.Status != StatusReviewed {
		t.Fatalf("defaults not applied: %+v", r)
	}
}

func TestNormalizeKeepsPrintedTotals(t *testing.T) {
	r := Receipt{TotalCents: 500, SubtotalCents: 450, TaxCents: 40,
		Items: []ReceiptItem{{Name: "x", Quantity: 2, UnitPriceCents: 100, LineTotalCents: 150}}}
	r.Normalize()
	if r.TotalCents != 500 || r.Items[0].LineTotalCents != 150 {
		t.Fatalf("printed values overwritten: %+v", r)
	}
}

func TestValidate(t *testing.T) {
	ok := Receipt{StoreName: "Safeway", Date: "2026-10-01", Items: []ReceiptItem{{Name: "Milk", Quantity: 1, UnitPriceCents: 429}}}
	if err := ok.Validate(); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	cases := map[string]Receipt{
		"no store":     {Date: "2026-10-01"},
		"bad date":     {StoreName: "a", Date: "10/01/2026"},
		"neg tax":      {StoreName: "a", Date: "2026-10-01", TaxCents: -1},
		"bad status":   {StoreName: "a", Date: "2026-10-01", Status: "done"},
		"bad source":   {StoreName: "a", Date: "2026-10-01", Source: "scraper"},
		"zero qty":     {StoreName: "a", Date: "2026-10-01", Items: []ReceiptItem{{Name: "x"}}},
		"neg discount": {StoreName: "a", Date: "2026-10-01", Items: []ReceiptItem{{Name: "x", Quantity: 1, DiscountCents: -5}}},
		"bad adj kind": {StoreName: "a", Date: "2026-10-01", Adjustments: []ReceiptAdjustment{{Kind: "bogus"}}},
	}
	for name, r := range cases {
		if err := r.Validate(); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
