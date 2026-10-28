package repository

import (
	"context"
	"errors"
	"testing"

	"receipts-db/internal/models"
)

func TestReviewQueueMapLearnsAliasAndReusesIt(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	products, receipts := NewProductRepository(db), NewReceiptRepository(db)

	mk := func(items ...models.ReceiptItem) *models.Receipt {
		r := &models.Receipt{StoreName: "Corner Grocer", Date: "2026-03-01", Source: models.SourceManual, Items: items}
		if err := receipts.Create(ctx, r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	r1 := mk(models.ReceiptItem{RawText: "ORG BANANA", Name: "Organic bananas", ItemNumber: "4011", Quantity: 1},
		models.ReceiptItem{Name: "Milk 1gal", Quantity: 1})
	r2 := mk(models.ReceiptItem{RawText: "ORG BANANA", Name: "bananas", Quantity: 1}, // same text, no number
		models.ReceiptItem{Name: "Eggs", ItemNumber: "4011", Quantity: 1}) // same number, other text

	q, total, err := products.ListReviewItems(ctx, 50, 0)
	if err != nil || total != 4 || len(q) != 4 {
		t.Fatalf("queue: %d %d %v", total, len(q), err)
	}
	banana := &models.Product{CanonicalName: "Bananas, organic"}
	if err := products.CreateProduct(ctx, banana); err != nil {
		t.Fatal(err)
	}
	res, err := products.MapItem(ctx, int64(r1.Items[0].ID), banana.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res.AliasID == 0 || res.AlsoMapped != 2 {
		t.Fatalf("map result %+v", res)
	}
	got, _ := receipts.GetByID(ctx, r2.ID)
	if got.Items[0].ProductID == nil || got.Items[0].MatchSource != "exact" ||
		got.Items[1].ProductID == nil || got.Items[1].MatchSource != "item_number" {
		t.Fatalf("propagation: %+v", got.Items)
	}
	_, total, _ = products.ListReviewItems(ctx, 50, 0)
	if total != 1 {
		t.Fatalf("queue after map = %d, want 1 (milk)", total)
	}
	// "Reused next time": a new receipt from the same merchant auto-maps by number and by text.
	r3 := mk(models.ReceiptItem{Name: "whatever", ItemNumber: "4011", Quantity: 1},
		models.ReceiptItem{Name: "org banana", Quantity: 1})
	got, _ = receipts.GetByID(ctx, r3.ID)
	for _, it := range got.Items {
		if it.ProductID == nil || *it.ProductID != banana.ID {
			t.Fatalf("not reused: %+v", it)
		}
	}
	// Remap the same text to another product: alias moves, no unique-key violation.
	plantain := &models.Product{CanonicalName: "Plantains"}
	_ = products.CreateProduct(ctx, plantain)
	if _, err := products.MapItem(ctx, int64(r1.Items[0].ID), plantain.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = db.QueryRowContext(ctx, `SELECT count(*) FROM product_aliases WHERE product_id = $1`, plantain.ID).Scan(&n)
	if n != 1 {
		t.Fatalf("aliases moved = %d", n)
	}

	// Search by canonical name and by alias text.
	for _, s := range []string{"banan", "ORG BANANA", "plantain"} {
		ps, err := products.SearchProducts(ctx, s, 5)
		if err != nil || len(ps) == 0 {
			t.Errorf("search %q: %v %v", s, ps, err)
		}
	}
	if ps, _ := products.SearchProducts(ctx, "zzzz", 5); len(ps) != 0 {
		t.Errorf("search zzzz: %v", ps)
	}

	// Errors.
	if _, err := products.MapItem(ctx, 999999, banana.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown item: %v", err)
	}
	if _, err := products.MapItem(ctx, int64(r1.Items[1].ID), 999999); !errors.Is(err, ErrProductNotFound) {
		t.Errorf("unknown product: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE receipts SET status = 'parsing' WHERE id = $1`, r1.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := products.MapItem(ctx, int64(r1.Items[1].ID), banana.ID); !errors.Is(err, ErrReceiptBusy) {
		t.Errorf("parsing receipt: %v", err)
	}
	if _, total, _ = products.ListReviewItems(ctx, 50, 0); total != 0 {
		t.Errorf("parsing receipt still queued: %d", total)
	}
}
