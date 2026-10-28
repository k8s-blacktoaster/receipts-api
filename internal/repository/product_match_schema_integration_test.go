package repository

import (
	"context"
	"testing"

	"receipts-db/internal/database"
	"receipts-db/internal/models"
)

// Migration 009 round-trips the match columns and backfills pre-existing mappings as manual.
func TestMatchColumnsMigration(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	migrateTo(t, db, 8)
	var mid, pid, rid int64
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(db.QueryRowContext(ctx, `INSERT INTO merchants (name) VALUES ('acme') RETURNING id`).Scan(&mid))
	must(db.QueryRowContext(ctx, `INSERT INTO products (canonical_name) VALUES ('Widget') RETURNING id`).Scan(&pid))
	must(db.QueryRowContext(ctx, `INSERT INTO receipts (store_name, merchant_id, date, status) VALUES ('acme',$1,'2026-01-02','reviewed') RETURNING id`, mid).Scan(&rid))
	_, err := db.ExecContext(ctx, `INSERT INTO receipt_items (receipt_id, name, quantity, product_id) VALUES ($1,'w',1,$2),($1,'v',1,NULL)`, rid, pid)
	must(err)
	if err := database.RunMigrations(db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	var manual, unmapped int
	must(db.QueryRowContext(ctx, `SELECT count(*) FILTER (WHERE match_source='manual' AND match_confidence=1),
		count(*) FILTER (WHERE product_id IS NULL AND match_source IS NULL) FROM receipt_items`).Scan(&manual, &unmapped))
	if manual != 1 || unmapped != 1 {
		t.Fatalf("backfill manual=%d unmapped=%d", manual, unmapped)
	}
	if _, err := db.ExecContext(ctx, `UPDATE receipt_items SET match_source='guess'`); err == nil {
		t.Fatal("bad match_source accepted")
	}

	repo := NewReceiptRepository(db)
	conf := 0.62
	in := &models.Receipt{StoreName: "acme", Date: "2026-02-03", Source: models.SourceManual,
		Items: []models.ReceiptItem{{Name: "Gadget", Quantity: 1, ProductID: &pid, MatchSource: "llm", MatchConfidence: &conf}}}
	must(repo.Create(ctx, in))
	got, err := repo.GetByID(ctx, in.ID)
	must(err)
	it := got.Items[0]
	if it.ProductID == nil || *it.ProductID != pid || it.MatchSource != "llm" || it.MatchConfidence == nil || *it.MatchConfidence != conf {
		t.Fatalf("round trip: %+v", it)
	}
}
