package repository

import (
	"context"
	"errors"
	"testing"

	"receipts-db/internal/models"
)

func TestMerchantMerge(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	receipts, merchants, products := NewReceiptRepository(db), NewMerchantRepository(db), NewProductRepository(db)
	mk := func(name, num, addr string) *models.Receipt {
		r := &models.Receipt{StoreName: name, StoreNumber: num, StoreAddress: addr, Date: "2026-04-01",
			Source: models.SourceManual, Items: []models.ReceiptItem{{Name: "x", Quantity: 1}}}
		if err := receipts.Create(ctx, r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	into := mk("Corner Market", "12", "1 Elm St")
	from1 := mk("Corner Mkt", "12", "")         // same store number -> folded
	from2 := mk("Corner Mkt", "", "1 ELM ST")   // same address -> folded
	from3 := mk("Corner Mkt", "40", "9 Oak Rd") // new store -> moved
	from4 := mk("Corner Mkt", "41", "1 Elm St") // same address, other number -> kept, not folded
	p := &models.Product{CanonicalName: "Bread"}
	q := &models.Product{CanonicalName: "Rolls"}
	_ = products.CreateProduct(ctx, p)
	_ = products.CreateProduct(ctx, q)
	for _, a := range []models.ProductAlias{
		{ProductID: p.ID, MerchantID: into.MerchantID, AliasText: "BREAD", ItemNumber: "77"},
		{ProductID: p.ID, MerchantID: from1.MerchantID, AliasText: "bread"},                          // text collision -> dropped
		{ProductID: q.ID, MerchantID: from1.MerchantID, AliasText: "DINNER ROLLS", ItemNumber: "77"}, // number collision -> kept, cleared
		{ProductID: q.ID, MerchantID: from1.MerchantID, AliasText: "ROLLS 6CT", ItemNumber: "78"},    // moved intact
	} {
		a := a
		if err := products.UpsertAlias(ctx, &a); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := merchants.Merge(ctx, into.MerchantID, into.MerchantID); !errors.Is(err, ErrSelfMerge) {
		t.Fatalf("self merge: %v", err)
	}
	if _, err := merchants.Merge(ctx, 99999, into.MerchantID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE receipts SET status = 'parsing' WHERE id = $1`, from3.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := merchants.Merge(ctx, from1.MerchantID, into.MerchantID); !errors.Is(err, ErrReceiptBusy) {
		t.Fatalf("busy: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE receipts SET status = 'reviewed' WHERE id = $1`, from3.ID); err != nil {
		t.Fatal(err)
	}

	res, err := merchants.Merge(ctx, from1.MerchantID, into.MerchantID)
	if err != nil {
		t.Fatal(err)
	}
	if res.Receipts != 4 || res.StoresFolded != 2 || res.StoresMoved != 2 || res.AliasesDropped != 1 ||
		res.ItemNumbersCleared != 1 || res.AliasesMoved != 2 || res.Into.ReceiptCount != 5 {
		t.Fatalf("result %+v", res)
	}
	if _, err := merchants.GetByID(ctx, from1.MerchantID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("source merchant not deleted: %v", err)
	}
	for _, r := range []*models.Receipt{from1, from2} {
		got, _ := receipts.GetByID(ctx, r.ID)
		if got.MerchantID != into.MerchantID || got.StoreID == nil || *got.StoreID != *into.StoreID {
			t.Errorf("receipt %d: merchant %d store %v", r.ID, got.MerchantID, got.StoreID)
		}
	}
	g4, _ := receipts.GetByID(ctx, from4.ID)
	var num string
	_ = db.QueryRowContext(ctx, `SELECT store_number FROM stores WHERE id = $1`, *g4.StoreID).Scan(&num)
	if *g4.StoreID == *into.StoreID || num != "41" {
		t.Errorf("numbered store folded by address: store %d number %q", *g4.StoreID, num)
	}
	var cleared int
	_ = db.QueryRowContext(ctx, `SELECT count(*) FROM product_aliases WHERE alias_text = 'DINNER ROLLS' AND item_number IS NULL AND merchant_id = $1`, into.MerchantID).Scan(&cleared)
	if cleared != 1 {
		t.Fatalf("colliding alias not kept with item_number cleared")
	}
	// The kept alias still maps by text on the merged merchant; 77 still maps to into's product.
	r := mk("Corner Market", "", "")
	r.Items = []models.ReceiptItem{{Name: "dinner rolls", Quantity: 1}, {Name: "?", ItemNumber: "77", Quantity: 1}}
	if err := receipts.Update(ctx, r); err != nil {
		t.Fatal(err)
	}
	got, _ := receipts.GetByID(ctx, r.ID)
	if got.Items[0].ProductID == nil || *got.Items[0].ProductID != q.ID || got.Items[1].ProductID == nil || *got.Items[1].ProductID != p.ID {
		t.Fatalf("post-merge mapping: %+v", got.Items)
	}
}

// Merges stick: the merged-away name (and names already redirected to it) resolve to the
// survivor on later receipts; renaming another merchant onto a redirected name is a conflict.
func TestMerchantMergeRedirects(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	receipts, merchants := NewReceiptRepository(db), NewMerchantRepository(db)
	mk := func(name string) *models.Receipt {
		r := &models.Receipt{StoreName: name, Date: "2026-04-01", Source: models.SourceManual,
			Items: []models.ReceiptItem{{Name: "x", Quantity: 1}}}
		if err := receipts.Create(ctx, r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	lucky, luckys, survivor, other := mk("Lucky"), mk("Luckys"), mk("Lucky Supermarkets"), mk("Other")
	if _, err := merchants.Merge(ctx, lucky.MerchantID, luckys.MerchantID); err != nil {
		t.Fatal(err)
	}
	if _, err := merchants.Merge(ctx, luckys.MerchantID, survivor.MerchantID); err != nil { // chain
		t.Fatal(err)
	}
	for _, name := range []string{"Lucky", "LUCKY", "Luckys"} {
		if r := mk(name); r.MerchantID != survivor.MerchantID {
			t.Errorf("%q -> merchant %d, want survivor %d", name, r.MerchantID, survivor.MerchantID)
		}
	}
	var n int
	_ = db.QueryRowContext(ctx, `SELECT count(*) FROM merchants WHERE name IN ('lucky','luckys')`).Scan(&n)
	if n != 0 {
		t.Fatalf("merged-away merchants re-created: %d", n)
	}
	var conflict *MerchantConflict
	if _, err := merchants.Rename(ctx, other.MerchantID, "Lucky"); !errors.As(err, &conflict) || conflict.Existing.ID != survivor.MerchantID {
		t.Fatalf("rename onto redirected name: %v", err)
	}
	// The survivor may take the redirected name back; the redirect goes away.
	if m, err := merchants.Rename(ctx, survivor.MerchantID, "Lucky"); err != nil || m.Name != "lucky" {
		t.Fatalf("survivor rename: %v %v", m, err)
	}
	if r := mk("Lucky"); r.MerchantID != survivor.MerchantID {
		t.Fatalf("after rename: %d", r.MerchantID)
	}
}
