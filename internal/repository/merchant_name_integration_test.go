package repository

import (
	"context"
	"testing"

	"receipts-db/internal/database"
	"receipts-db/internal/models"
)

func TestNormalizeMerchantName(t *testing.T) {
	for in, want := range map[string]string{"  Trader  Joes ": "Trader Joes", "Costco\tWholesale": "Costco Wholesale", "x": "x"} {
		if got := NormalizeMerchantName(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}

// A1: internal whitespace collapses for dedupe; A2: lowercase stored name is upgraded.
func TestMerchantInternalWhitespaceAndCasingUpgrade(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	repo := NewReceiptRepository(db)
	a := &models.Receipt{StoreName: "trader joes", Date: "2026-10-01"}
	b := &models.Receipt{StoreName: "Trader  Joes", Date: "2026-10-02"}
	c := &models.Receipt{StoreName: "TRADER JOES", Date: "2026-10-03"}
	for _, r := range []*models.Receipt{a, b, c} {
		if err := repo.Create(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	if a.MerchantID != b.MerchantID || b.MerchantID != c.MerchantID {
		t.Fatalf("expected one merchant: %d %d %d", a.MerchantID, b.MerchantID, c.MerchantID)
	}
	var name string
	db.QueryRow(`SELECT name FROM merchants WHERE id = $1`, a.MerchantID).Scan(&name)
	if name != "Trader Joes" { // upgraded from lowercase once; a later cased variant does not overwrite
		t.Fatalf("display name = %q", name)
	}
	mustExecErr(t, db, "merchants_name_uniq", `INSERT INTO merchants (name) VALUES ('TRADER JOES')`)
	mustExecErr(t, db, "merchants_name_normalized", `INSERT INTO merchants (name) VALUES ('Two  Spaces')`)
}

// A1/A2 backfill: 006 merges internal-whitespace variants and picks the display name.
func TestMerchantBackfillDisplayName(t *testing.T) {
	db := testDB(t)
	if err := database.RollbackMigrations(db, "../../migrations", 1); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		// simulate legacy duplicates that pre-date the unique key
		`DROP INDEX merchants_name_uniq`,
		// most common wins: 'Costco' used by 2 receipts beats 'costco' (1) and 'COSTCO  ' (merchant only)
		`INSERT INTO merchants (id, name) VALUES (801, 'costco'), (802, 'Costco'), (803, 'COSTCO')`,
		`INSERT INTO receipts (store_name, date, merchant_id) VALUES ('a','2026-01-01',801), ('b','2026-01-01',802), ('c','2026-01-01',802)`,
		// tie (1 each): cased variant beats all-lowercase even though lowercase has lower id
		`INSERT INTO merchants (id, name) VALUES (811, 'trader joes'), (812, 'Trader Joes')`,
		// internal whitespace variant merges (005 allowed it)
		`INSERT INTO merchants (id, name) VALUES (821, 'Whole  Foods'), (822, 'whole foods')`,
		`INSERT INTO stores (id, merchant_id, store_number) VALUES (831, 821, '7'), (832, 822, '7')`,
		`INSERT INTO receipts (store_name, date, merchant_id, store_id) VALUES ('w','2026-01-01',822,832)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := database.RunMigrations(db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	check := func(id int, want string) {
		t.Helper()
		var name string
		if err := db.QueryRow(`SELECT name FROM merchants WHERE id = $1`, id).Scan(&name); err != nil || name != want {
			t.Fatalf("merchant %d = %q (%v), want %q", id, name, err, want)
		}
	}
	check(801, "Costco")
	check(811, "Trader Joes")
	check(821, "Whole Foods")
	var n, sid int
	db.QueryRow(`SELECT count(*) FROM merchants WHERE id IN (802,803,812,822)`).Scan(&n)
	db.QueryRow(`SELECT store_id FROM receipts WHERE store_name = 'w'`).Scan(&sid)
	if n != 0 || sid != 831 {
		t.Fatalf("merge incomplete: leftover=%d store=%d", n, sid)
	}
}
