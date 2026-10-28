package repository

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"receipts-db/internal/models"
)

// seedCatalog creates merchant "acme" (via a receipt) and n products, each with an acme alias
// "ACME ITEM i" / item number "10i".
func seedCatalog(t *testing.T, ctx context.Context, products ProductRepository, receipts ReceiptRepository, n int) (merchantID int64, ids []int64) {
	t.Helper()
	seed := &models.Receipt{StoreName: "Acme", Date: "2026-01-01", Source: models.SourceManual,
		Items: []models.ReceiptItem{{Name: "seed", Quantity: 1}}}
	if err := receipts.Create(ctx, seed); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		p := &models.Product{CanonicalName: fmt.Sprintf("Product %d", i), UPC: fmt.Sprintf("0000000000%02d", i)}
		if err := products.CreateProduct(ctx, p); err != nil {
			t.Fatal(err)
		}
		if err := products.UpsertAlias(ctx, &models.ProductAlias{ProductID: p.ID, MerchantID: seed.MerchantID,
			AliasText: fmt.Sprintf("ACME ITEM %d", i), ItemNumber: fmt.Sprintf("10%d", i), UPC: fmt.Sprintf("0000000000%02d", i)}); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, p.ID)
	}
	return seed.MerchantID, ids
}

// Success criterion: >=90% of lines with an item number/UPC auto-map (here: all of them).
func TestMatcherAutoMapsItemNumbersAndUPCs(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	products, receipts := NewProductRepository(db), NewReceiptRepository(db)
	_, ids := seedCatalog(t, ctx, products, receipts, 20)

	r := &models.Receipt{StoreName: "ACME", Date: "2026-02-01", Source: models.SourceManual}
	for i := 0; i < 20; i++ {
		it := models.ReceiptItem{Name: fmt.Sprintf("printed differently %d", i), Quantity: 1}
		if i%2 == 0 {
			it.ItemNumber = fmt.Sprintf("10%d", i)
		} else {
			it.UPC = fmt.Sprintf("0000000000%02d", i)
		}
		r.Items = append(r.Items, it)
	}
	if err := receipts.Create(ctx, r); err != nil {
		t.Fatal(err)
	}
	got, err := receipts.GetByID(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	mapped := 0
	for i, it := range got.Items {
		if it.ProductID != nil && *it.ProductID == ids[i] {
			mapped++
		}
		want := map[bool]string{true: "item_number", false: "upc"}[i%2 == 0]
		if it.MatchSource != want || it.MatchConfidence == nil || *it.MatchConfidence != 1 {
			t.Errorf("line %d: source %q conf %v", i, it.MatchSource, it.MatchConfidence)
		}
	}
	if mapped*10 < len(got.Items)*9 {
		t.Fatalf("auto-mapped %d/%d (<90%%)", mapped, len(got.Items))
	}
}

func TestMatcherAliasFuzzyScopeAndManual(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	products, receipts := NewProductRepository(db), NewReceiptRepository(db)
	_, ids := seedCatalog(t, ctx, products, receipts, 3)

	r := &models.Receipt{StoreName: "Acme", Date: "2026-02-01", Source: models.SourceManual, Items: []models.ReceiptItem{
		{RawText: "acme item 1", Name: "Item one", Quantity: 1}, // exact alias (case-insensitive)
		{Name: "ACME ITEM 2X", Quantity: 1},                     // fuzzy -> suggestion only
		{Name: "Completely unrelated", Quantity: 1},             // nothing
		{Name: "anything", ProductID: &ids[0], Quantity: 1},     // already mapped: kept, manual
		{Name: "x", ItemNumber: "999", Quantity: 1},             // unknown item number
	}}
	if err := receipts.Create(ctx, r); err != nil {
		t.Fatal(err)
	}
	got, _ := receipts.GetByID(ctx, r.ID)
	it := got.Items
	if it[0].ProductID == nil || *it[0].ProductID != ids[1] || it[0].MatchSource != "exact" {
		t.Errorf("exact: %+v", it[0])
	}
	if it[1].ProductID != nil || it[1].SuggestedProductID == nil || *it[1].SuggestedProductID != ids[2] ||
		it[1].MatchSource != "fuzzy" || it[1].MatchConfidence == nil || *it[1].MatchConfidence >= 1 || *it[1].MatchConfidence < FuzzySuggestThreshold {
		t.Errorf("fuzzy: %+v", it[1])
	}
	if it[2].ProductID != nil || it[2].SuggestedProductID != nil || it[2].MatchSource != "" {
		t.Errorf("none: %+v", it[2])
	}
	if it[3].ProductID == nil || *it[3].ProductID != ids[0] || it[3].MatchSource != "manual" {
		t.Errorf("manual: %+v", it[3])
	}
	if it[4].ProductID != nil {
		t.Errorf("unknown item number mapped: %+v", it[4])
	}

	// Aliases are merchant-scoped: another merchant's identical item number / text does not map.
	other := &models.Receipt{StoreName: "Other Shop", Date: "2026-02-01", Source: models.SourceManual, Items: []models.ReceiptItem{
		{Name: "ACME ITEM 1", ItemNumber: "101", Quantity: 1},
		{Name: "?", UPC: "000000000001", Quantity: 1}}}
	if err := receipts.Create(ctx, other); err != nil {
		t.Fatal(err)
	}
	og, _ := receipts.GetByID(ctx, other.ID)
	for _, it := range og.Items {
		if it.ProductID != nil {
			t.Errorf("cross-merchant alias/UPC applied: %+v", it)
		}
	}
}

// Exact alias matching ignores only case and whitespace runs (item_alias_key); punctuation is
// significant, so sizes/fractions never collapse. QA repros must not match.
func TestMatcherAliasKeyConservative(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	products, receipts := NewProductRepository(db), NewReceiptRepository(db)
	seed := &models.Receipt{StoreName: "Acme", Date: "2026-01-01", Source: models.SourceManual, Items: []models.ReceiptItem{{Name: "seed", Quantity: 1}}}
	if err := receipts.Create(ctx, seed); err != nil {
		t.Fatal(err)
	}
	ids := map[string]int64{}
	for _, a := range []string{"MILK 1/2 GAL", "SODA 1.5L", "COKE 2L"} {
		p := &models.Product{CanonicalName: a}
		_ = products.CreateProduct(ctx, p)
		if err := products.UpsertAlias(ctx, &models.ProductAlias{ProductID: p.ID, MerchantID: seed.MerchantID, AliasText: a}); err != nil {
			t.Fatal(err)
		}
		ids[a] = p.ID
	}
	cases := []struct {
		line, want string // want alias text when an exact match is expected, "" = no exact match
	}{
		{"milk 1/2 gal", "MILK 1/2 GAL"}, {"  MILK   1/2\tGAL ", "MILK 1/2 GAL"}, {"Soda 1.5l", "SODA 1.5L"},
		{"MILK 12 GAL", ""}, {"SODA 15L", ""}, {"SODA 1,5L", ""}, {"COKE-2L", ""},
	}
	r := &models.Receipt{StoreName: "Acme", Date: "2026-02-01", Source: models.SourceManual}
	for _, c := range cases {
		r.Items = append(r.Items, models.ReceiptItem{Name: strings.ReplaceAll(c.line, "\\t", "\t"), Quantity: 1})
	}
	if err := receipts.Create(ctx, r); err != nil {
		t.Fatal(err)
	}
	got, _ := receipts.GetByID(ctx, r.ID)
	for i, c := range cases {
		it := got.Items[i]
		if c.want != "" {
			if it.ProductID == nil || *it.ProductID != ids[c.want] || it.MatchSource != "exact" {
				t.Errorf("%q should match %q exactly: %+v", c.line, c.want, it)
			}
		} else if it.ProductID != nil {
			t.Errorf("%q must not auto-map: %+v", c.line, it)
		}
	}
	coke := got.Items[6]
	if coke.SuggestedProductID == nil || *coke.SuggestedProductID != ids["COKE 2L"] || coke.MatchSource != "fuzzy" {
		t.Errorf("COKE-2L should be a fuzzy suggestion for COKE 2L: %+v", coke)
	}
	// Correction propagation uses the same key: "MILK 12 GAL" is not re-pointed by mapping
	// "milk  1/2 gal", and learning a new text never re-points the COKE 2L alias.
	r2 := &models.Receipt{StoreName: "Acme", Date: "2026-02-02", Source: models.SourceManual, Items: []models.ReceiptItem{
		{Name: "Choc Milk 1/2", Quantity: 1}, {Name: "choc  MILK 1/2", Quantity: 1}, {Name: "choc milk 12", Quantity: 1}}}
	_ = receipts.Create(ctx, r2)
	res, err := products.MapItem(ctx, int64(r2.Items[0].ID), ids["MILK 1/2 GAL"], nil)
	if err != nil || res.AlsoMapped != 1 {
		t.Fatalf("propagation: %+v %v", res, err)
	}
	if _, err := products.MapItem(ctx, int64(got.Items[6].ID), ids["SODA 1.5L"], nil); err != nil {
		t.Fatal(err)
	}
	var cokeAlias int64
	_ = db.QueryRowContext(ctx, `SELECT product_id FROM product_aliases WHERE alias_text = 'COKE 2L'`).Scan(&cokeAlias)
	if cokeAlias != ids["COKE 2L"] {
		t.Fatalf("COKE 2L alias re-pointed to %d", cokeAlias)
	}
}
