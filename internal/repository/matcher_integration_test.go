package repository

import (
	"context"
	"fmt"
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

// Exact alias matching ignores case, punctuation and whitespace runs (merchant_name_key).
func TestMatcherNormalizesAliasText(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	products, receipts := NewProductRepository(db), NewReceiptRepository(db)
	_, ids := seedCatalog(t, ctx, products, receipts, 2) // alias "ACME ITEM 1"
	r := &models.Receipt{StoreName: "Acme", Date: "2026-02-01", Source: models.SourceManual, Items: []models.ReceiptItem{
		{Name: "acme.  item-1", Quantity: 1}, {Name: "  ACME_ITEM 1 ", Quantity: 1}, {Name: "!!", Quantity: 1}}}
	if err := receipts.Create(ctx, r); err != nil {
		t.Fatal(err)
	}
	got, _ := receipts.GetByID(ctx, r.ID)
	for _, it := range got.Items[:2] {
		if it.ProductID == nil || *it.ProductID != ids[1] || it.MatchSource != "exact" {
			t.Errorf("%q not matched exactly: %+v", it.Name, it)
		}
	}
	if got.Items[2].ProductID != nil {
		t.Errorf("punctuation-only line matched: %+v", got.Items[2])
	}
	// A review correction propagates to differently punctuated lines too.
	r2 := &models.Receipt{StoreName: "Acme", Date: "2026-02-02", Source: models.SourceManual, Items: []models.ReceiptItem{
		{Name: "Choc. Milk", Quantity: 1}, {Name: "choc milk", Quantity: 1}}}
	_ = receipts.Create(ctx, r2)
	res, err := products.MapItem(ctx, int64(r2.Items[0].ID), ids[0], nil)
	if err != nil || res.AlsoMapped != 1 {
		t.Fatalf("propagation: %+v %v", res, err)
	}
}
