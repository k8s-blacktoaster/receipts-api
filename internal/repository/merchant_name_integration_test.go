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

// 006 backfill: display name is chosen among existing receipts.store_name variants.
func TestMerchantBackfillDisplayName(t *testing.T) {
	db := testDB(t)
	migrateTo(t, db, 5)
	for _, q := range []string{
		// simulate legacy duplicates that pre-date the unique key
		`DROP INDEX merchants_name_uniq`,
		// frequency beats casing: COSTCO printed on 2 receipts, Costco on 1
		`INSERT INTO merchants (id, name) VALUES (801, 'costco'), (802, 'Costco'), (803, 'COSTCO')`,
		`INSERT INTO receipts (store_name, date, merchant_id) VALUES ('COSTCO','2026-01-01',801), ('COSTCO','2026-01-02',803), ('Costco','2026-01-03',802)`,
		// tie (1 vote each): mixed case wins over all-lower
		`INSERT INTO merchants (id, name) VALUES (811, 'trader joes'), (812, 'Trader Joes')`,
		`INSERT INTO receipts (store_name, date, merchant_id) VALUES ('trader joes','2026-01-01',811), ('Trader  Joes','2026-01-02',812)`,
		// no receipts, no mixed-case variant: keep an existing variant (never invent casing)
		`INSERT INTO merchants (id, name) VALUES (841, 'whole foods'), (842, 'WHOLE  FOODS')`,
		// internal-whitespace variant + duplicate store number merges
		`INSERT INTO merchants (id, name) VALUES (821, 'Sprouts  Market'), (822, 'sprouts market')`,
		`INSERT INTO stores (id, merchant_id, store_number) VALUES (831, 821, '7'), (832, 822, '7')`,
		`INSERT INTO receipts (store_name, date, merchant_id, store_id) VALUES ('Sprouts Market','2026-01-01',822,832)`,
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
	check(801, "COSTCO")
	check(811, "Trader Joes")
	check(841, "whole foods")
	check(821, "Sprouts Market")
	var n, sid int
	db.QueryRow(`SELECT count(*) FROM merchants WHERE id IN (802,803,812,822,842)`).Scan(&n)
	db.QueryRow(`SELECT store_id FROM receipts WHERE store_name = 'Sprouts Market'`).Scan(&sid)
	if n != 0 || sid != 831 {
		t.Fatalf("merge incomplete: leftover=%d store=%d", n, sid)
	}
}

// QA repro: legacy 001/002 data with case/whitespace variants -> Costco, Trader Joes.
func TestQAReproLegacyStoreNameVariants(t *testing.T) {
	db := testDB(t)
	migrateTo(t, db, 2)
	for _, name := range []string{"costco", "Costco", "COSTCO", "trader  joes", "Trader Joes", "TRADER   JOES"} {
		var id int
		if err := db.QueryRow(`INSERT INTO receipts (store_name, date, subtotal) VALUES ($1, '2025-01-01', 12.34) RETURNING id`, name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO receipt_items (receipt_id, sku, name, quantity, unit_price) VALUES ($1, 'X', 'Item', 1, 12.34)`, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.RunMigrations(db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(`SELECT name FROM merchants ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var n string
		rows.Scan(&n)
		got = append(got, n)
	}
	if len(got) != 2 || got[0] != "Costco" || got[1] != "Trader Joes" {
		t.Fatalf("merchants = %q, want [Costco Trader Joes]", got)
	}
	var orphans int
	db.QueryRow(`SELECT count(*) FROM receipts r LEFT JOIN merchants m ON m.id = r.merchant_id WHERE m.id IS NULL`).Scan(&orphans)
	if orphans != 0 {
		t.Fatalf("%d receipts lost their merchant", orphans)
	}
}
