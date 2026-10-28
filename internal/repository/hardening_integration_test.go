package repository

import (
	"context"
	"strings"
	"testing"

	"receipts-db/internal/database"
	"receipts-db/internal/models"
)

func mustExecErr(t *testing.T, db *database.DB, want, q string, args ...any) {
	t.Helper()
	_, err := db.Exec(q, args...)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("expected error containing %q, got %v\nquery: %s", want, err, q)
	}
}

// F1: merchant names are deduped case- and whitespace-insensitively.
func TestF1MerchantDedupeTrimmed(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	repo := NewReceiptRepository(db)
	a := &models.Receipt{StoreName: "Trader Joe's", Date: "2026-10-01", SubtotalCents: 100}
	b := &models.Receipt{StoreName: "  trader joe's  ", Date: "2026-10-02", SubtotalCents: 200}
	for _, r := range []*models.Receipt{a, b} {
		if err := repo.Create(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	if a.MerchantID != b.MerchantID {
		t.Fatalf("expected same merchant, got %d vs %d", a.MerchantID, b.MerchantID)
	}
	mustExecErr(t, db, "merchants_name_uniq", `INSERT INTO merchants (name) VALUES ('TRADER JOE''S')`)
	mustExecErr(t, db, "merchants_name_trimmed", `INSERT INTO merchants (name) VALUES (' Other ')`)
}

// F2: money columns are non-negative; adjustment signs are consistent.
func TestF2MoneyChecks(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	repo := NewReceiptRepository(db)
	r := &models.Receipt{StoreName: "Acme", Date: "2026-10-01", SubtotalCents: 500,
		Items: []models.ReceiptItem{{Name: "x", Quantity: 1, UnitPriceCents: 500}}}
	if err := repo.Create(ctx, r); err != nil {
		t.Fatal(err)
	}
	mustExecErr(t, db, "receipts_total_nonneg", `UPDATE receipts SET total_cents = -1 WHERE id = $1`, r.ID)
	mustExecErr(t, db, "receipts_savings_nonneg", `UPDATE receipts SET savings_cents = -1 WHERE id = $1`, r.ID)
	mustExecErr(t, db, "receipt_items_discount_nonneg", `UPDATE receipt_items SET discount_cents = -5 WHERE receipt_id = $1`, r.ID)
	mustExecErr(t, db, "receipt_items_line_total_nonneg", `UPDATE receipt_items SET line_total_cents = -5 WHERE receipt_id = $1`, r.ID)
	if _, err := db.Exec(`UPDATE receipt_items SET is_return = true, unit_price_cents = -500, line_total_cents = -500 WHERE receipt_id = $1`, r.ID); err != nil {
		t.Fatalf("returns may be negative: %v", err)
	}
	mustExecErr(t, db, "receipt_adjustments_sign", `INSERT INTO receipt_adjustments (receipt_id, kind, amount_cents) VALUES ($1, 'coupon', 100)`, r.ID)
	mustExecErr(t, db, "receipt_adjustments_sign", `INSERT INTO receipt_adjustments (receipt_id, kind, amount_cents) VALUES ($1, 'fee', -100)`, r.ID)
	// mismatched totals are allowed at the DB level (phase 2 flags needs_review)
	if _, err := db.Exec(`UPDATE receipts SET total_cents = 99999 WHERE id = $1`, r.ID); err != nil {
		t.Fatalf("total mismatch must not be a DB error: %v", err)
	}
}

// F3: a receipt's store must belong to its merchant.
func TestF3StoreBelongsToMerchant(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	repo := NewReceiptRepository(db)
	a := &models.Receipt{StoreName: "A Mart", StoreNumber: "1", Date: "2026-10-01"}
	b := &models.Receipt{StoreName: "B Mart", StoreNumber: "1", Date: "2026-10-01"}
	for _, r := range []*models.Receipt{a, b} {
		if err := repo.Create(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	mustExecErr(t, db, "receipts_store_merchant_fkey", `UPDATE receipts SET store_id = $1 WHERE id = $2`, *b.StoreID, a.ID)
	// deleting a store still nulls receipts.store_id
	if _, err := db.Exec(`DELETE FROM stores WHERE id = $1`, *a.StoreID); err != nil {
		t.Fatalf("store delete: %v", err)
	}
}

// F4: FK columns are indexed.
func TestF4FKIndexes(t *testing.T) {
	db := testDB(t)
	for _, name := range []string{"idx_receipts_source_document_id", "idx_receipts_uploaded_by_id",
		"idx_receipts_reviewed_by_id", "idx_source_documents_uploaded_by_id", "idx_receipt_adjustments_applies_to_item_id"} {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM pg_indexes WHERE indexname = $1`, name).Scan(&n); err != nil || n != 1 {
			t.Fatalf("index %s missing (n=%d err=%v)", name, n, err)
		}
	}
}

// F5: up -steps N applies only N migrations.
func TestF5UpSteps(t *testing.T) {
	db := testDB(t)
	if err := database.RollbackMigrations(db, "../../migrations", 0); err != nil {
		t.Fatal(err)
	}
	if err := database.RunMigrationsN(db, "../../migrations", 2); err != nil {
		t.Fatal(err)
	}
	var n int
	db.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&n)
	if n != 2 {
		t.Fatalf("expected 2 applied, got %d", n)
	}
	if err := database.RunMigrationsN(db, "../../migrations", 1); err != nil {
		t.Fatal(err)
	}
	db.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&n)
	if n != 3 {
		t.Fatalf("expected 3 applied, got %d", n)
	}
	if err := database.RunMigrations(db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
}

// F6: stores without a number are upserted by normalized address.
func TestF6StoreUpsertByAddress(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	repo := NewReceiptRepository(db)
	a := &models.Receipt{StoreName: "Corner Shop", StoreAddress: "1 Main St", Date: "2026-10-01"}
	b := &models.Receipt{StoreName: "Corner Shop", StoreAddress: " 1 MAIN ST ", Date: "2026-10-02"}
	for _, r := range []*models.Receipt{a, b} {
		if err := repo.Create(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	if *a.StoreID != *b.StoreID {
		t.Fatalf("expected same store, got %d vs %d", *a.StoreID, *b.StoreID)
	}
	mustExecErr(t, db, "stores_merchant_address_uniq", `INSERT INTO stores (merchant_id, address) VALUES ($1, '1 main st')`, a.MerchantID)
}

// F7: legacy store_name is nullable and views read merchant/store.
func TestF7ViewsUseMerchantStore(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	repo := NewReceiptRepository(db)
	r := &models.Receipt{StoreName: "Linked Mart", StoreAddress: "9 Elm", Date: "2026-10-01", SubtotalCents: 100}
	if err := repo.Create(ctx, r); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE receipts SET store_name = NULL, store_address = NULL WHERE id = $1`, r.ID); err != nil {
		t.Fatalf("store_name should be nullable: %v", err)
	}
	var name, addr string
	if err := db.QueryRow(`SELECT store_name, store_address FROM receipt_details_view WHERE receipt_id = $1 LIMIT 1`, r.ID).Scan(&name, &addr); err != nil {
		t.Fatal(err)
	}
	if name != "Linked Mart" || addr != "9 Elm" {
		t.Fatalf("view not reading merchant/store: %q %q", name, addr)
	}
	if err := db.QueryRow(`SELECT store_name FROM recent_purchases_view WHERE receipt_id = $1`, r.ID).Scan(&name); err != nil || name != "Linked Mart" {
		t.Fatalf("recent_purchases_view: %q %v", name, err)
	}
}

// F1/F6 backfill: 005 merges pre-existing whitespace-variant merchants and duplicate stores.
func TestF1F6BackfillMerges(t *testing.T) {
	db := testDB(t)
	if err := database.RollbackMigrations(db, "../../migrations", 1); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO merchants (id, name) VALUES (901, 'Sprouts'), (902, ' sprouts ')`,
		`INSERT INTO stores (id, merchant_id, address) VALUES (911, 901, '1 Main'), (912, 902, '1 main ')`,
		`INSERT INTO receipts (store_name, date, merchant_id, store_id) VALUES ('Sprouts', '2026-01-01', 901, 911), (' sprouts ', '2026-01-02', 902, 912)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := database.RunMigrations(db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	var merchants, stores, recMerchants int
	db.QueryRow(`SELECT count(*) FROM merchants WHERE lower(name) = 'sprouts'`).Scan(&merchants)
	db.QueryRow(`SELECT count(*) FROM stores WHERE merchant_id = 901`).Scan(&stores)
	db.QueryRow(`SELECT count(DISTINCT merchant_id) FROM receipts WHERE merchant_id = 901 AND store_id = 911`).Scan(&recMerchants)
	if merchants != 1 || stores != 1 || recMerchants != 1 {
		t.Fatalf("merge failed: merchants=%d stores=%d receipts-on-kept=%d", merchants, stores, recMerchants)
	}
}
