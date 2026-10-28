package repository

import (
	"context"
	"testing"

	"receipts-db/internal/database"
	"receipts-db/internal/models"
)

// Migration 011: invalid product UPCs cleared, valid ones copied to each merchant's oldest alias,
// duplicates within a merchant keep only the oldest; format CHECKs and per-merchant uniqueness.
func TestMerchantUPCMigration(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	migrateTo(t, db, 10)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO merchants (id, name) VALUES (1,'acme'),(2,'other')`)
	exec(`INSERT INTO products (id, canonical_name, upc) VALUES (1,'A','012345678905'),(2,'B','012345678905'),(3,'C','12-34'),(4,'D','12345678')`)
	exec(`INSERT INTO product_aliases (id, product_id, merchant_id, alias_text) VALUES
		(10,1,1,'a1'),(11,1,1,'a2'),(12,2,1,'b1'),(13,2,2,'b-other'),(14,3,1,'c1'),(15,4,2,'d1')`)
	if err := database.RunMigrations(db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	got := map[int]string{}
	rows, _ := db.QueryContext(ctx, `SELECT id, coalesce(upc,'') FROM product_aliases ORDER BY id`)
	for rows.Next() {
		var id int
		var u string
		rows.Scan(&id, &u)
		got[id] = u
	}
	rows.Close()
	want := map[int]string{10: "012345678905", 11: "", 12: "", 13: "012345678905", 14: "", 15: "12345678"}
	for id, u := range want {
		if got[id] != u {
			t.Errorf("alias %d upc %q want %q", id, got[id], u)
		}
	}
	var bad int
	db.QueryRowContext(ctx, `SELECT count(*) FROM products WHERE id = 3 AND upc IS NULL`).Scan(&bad)
	if bad != 1 {
		t.Error("invalid product UPC not cleared")
	}
	if _, err := db.ExecContext(ctx, `UPDATE products SET upc = '123' WHERE id = 4`); err == nil {
		t.Error("products.upc format CHECK missing")
	}
	if _, err := db.ExecContext(ctx, `UPDATE product_aliases SET upc = '012345678905' WHERE id = 11`); err == nil {
		t.Error("per-merchant UPC uniqueness missing")
	}
}

// Mapping a line with a UPC teaches the merchant's UPC; the next receipt maps by it.
func TestMapLearnsMerchantUPC(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	products, receipts := NewProductRepository(db), NewReceiptRepository(db)
	r := &models.Receipt{StoreName: "Acme", Date: "2026-05-01", Source: models.SourceManual,
		Items: []models.ReceiptItem{{Name: "Soda 12pk", UPC: "049000028911", Quantity: 1}}}
	if err := receipts.Create(ctx, r); err != nil {
		t.Fatal(err)
	}
	p := &models.Product{CanonicalName: "Cola 12 pack"}
	if _, err := products.MapItem(ctx, int64(r.Items[0].ID), 0, p); err != nil {
		t.Fatal(err)
	}
	r2 := &models.Receipt{StoreName: "ACME", Date: "2026-05-02", Source: models.SourceManual,
		Items: []models.ReceiptItem{{Name: "COLA12", UPC: "049000028911", Quantity: 1}}}
	if err := receipts.Create(ctx, r2); err != nil {
		t.Fatal(err)
	}
	got, _ := receipts.GetByID(ctx, r2.ID)
	if got.Items[0].ProductID == nil || *got.Items[0].ProductID != p.ID || got.Items[0].MatchSource != "upc" {
		t.Fatalf("not matched by learned UPC: %+v", got.Items[0])
	}
}
