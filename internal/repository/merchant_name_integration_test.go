package repository

import (
	"context"
	"testing"

	"receipts-db/internal/database"
	"receipts-db/internal/models"
)

// SQL merchant_name_key() (006) must agree with models.NormalizeMerchantName.
func TestMerchantNameSQLGoParity(t *testing.T) {
	db := testDB(t)
	inputs := []string{}
	for in, want := range models.MerchantNameCases {
		inputs = append(inputs, in, want)
	}
	inputs = append(inputs, "ÉCOLE Ünited", "a&&b", "&", "x - _ - y", "Dollar$ & Cents¢", "[brackets]\\back")
	for _, in := range inputs {
		var sqlKey string
		if err := db.QueryRow(`SELECT merchant_name_key($1)`, in).Scan(&sqlKey); err != nil {
			t.Fatal(err)
		}
		if goKey := models.NormalizeMerchantName(in); sqlKey != goKey {
			t.Errorf("%q: sql=%q go=%q", in, sqlKey, goKey)
		}
	}
}

// Writes resolve variants to one merchant whose stored name is the key.
func TestMerchantResolveByKey(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	repo := NewReceiptRepository(db)
	var ids []int64
	for _, n := range []string{"Trader Joe's", "TRADER  JOES", "trader-joe’s"} {
		r := &models.Receipt{StoreName: n, Date: "2026-10-01"}
		if err := repo.Create(ctx, r); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, r.MerchantID)
	}
	if ids[0] != ids[1] || ids[1] != ids[2] {
		t.Fatalf("expected one merchant, got %v", ids)
	}
	var name string
	db.QueryRow(`SELECT name FROM merchants WHERE id = $1`, ids[0]).Scan(&name)
	if name != "trader_joes" {
		t.Fatalf("stored name %q", name)
	}
	if err := repo.Create(ctx, &models.Receipt{StoreName: "!!!", Date: "2026-10-01"}); err == nil {
		t.Fatal("punctuation-only merchant name must be rejected")
	}
}

// Unique key on merchants.name and the normalization CHECK.
func TestMerchantNameUniqueAndCheck(t *testing.T) {
	db := testDB(t)
	if _, err := db.Exec(`INSERT INTO merchants (name) VALUES ('costco')`); err != nil {
		t.Fatal(err)
	}
	mustExecErr(t, db, "merchants_name_uniq", `INSERT INTO merchants (name) VALUES ('costco')`)
	for _, bad := range []string{"Costco", "costco ", "trader joes", "_costco", "barnes&noble", ""} {
		mustExecErr(t, db, "merchants_name_normalized", `INSERT INTO merchants (name) VALUES ($1)`, bad)
	}
}

// Backfill: legacy 001/002 receipts with spelling variants merge into one merchant per key,
// moving receipts, stores and aliases.
func TestMerchantBackfillMerge(t *testing.T) {
	db := testDB(t)
	migrateTo(t, db, 2)
	for _, name := range []string{"Costco", "COSTCO", "costco", "trader  joes", "Trader Joe's", "TRADER   JOES"} {
		var id int
		if err := db.QueryRow(`INSERT INTO receipts (store_name, date, subtotal) VALUES ($1, '2025-01-01', 12.34) RETURNING id`, name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO receipt_items (receipt_id, sku, name, quantity, unit_price) VALUES ($1, 'X', 'Item', 1, 12.34)`, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.RunMigrationsN(db, "../../migrations", 3); err != nil { // up to 005
		t.Fatal(err)
	}
	// stores + aliases on a duplicate merchant (005 keeps 'Trader Joe''s' and 'trader  joes' apart)
	var tjA, tjB int64
	db.QueryRow(`SELECT id FROM merchants WHERE name = 'Trader Joe''s'`).Scan(&tjA)
	db.QueryRow(`SELECT id FROM merchants WHERE name = 'trader  joes'`).Scan(&tjB)
	if tjA == 0 || tjB == 0 || tjA == tjB {
		t.Fatalf("setup: expected two TJ merchants before 006, got %d %d", tjA, tjB)
	}
	setup := []struct {
		q    string
		args []any
	}{
		{`INSERT INTO stores (id, merchant_id, store_number) VALUES (701, $1, '55'), (702, $2, '55'), (703, $2, '99')`, []any{tjA, tjB}},
		{`INSERT INTO products (id, canonical_name) VALUES (601, 'Bananas')`, nil},
		{`INSERT INTO product_aliases (product_id, merchant_id, alias_text) VALUES (601, $1, 'BANANAS'), (601, $2, 'bananas'), (601, $2, 'ORG BANANAS')`, []any{tjA, tjB}},
		// NORM-03: same item number, different text -> the later one is kept with item_number cleared
		{`INSERT INTO product_aliases (product_id, merchant_id, alias_text, item_number) VALUES (601, $1, 'BANANA BUNCH', '4011'), (601, $2, 'BANANAS LOOSE', '4011')`, []any{tjA, tjB}},
		{`UPDATE receipts SET store_id = 702 WHERE id = (SELECT min(id) FROM receipts WHERE merchant_id = $1)`, []any{tjB}},
	}
	for _, st := range setup {
		if _, err := db.Exec(st.q, st.args...); err != nil {
			t.Fatalf("%s: %v", st.q, err)
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
	if len(got) != 2 || got[0] != "costco" || got[1] != "trader_joes" {
		t.Fatalf("merchants = %q, want [costco trader_joes]", got)
	}
	var keep int64
	db.QueryRow(`SELECT id FROM merchants WHERE name = 'trader_joes'`).Scan(&keep)
	var tjReceipts, stores, aliases, orphan, onDupStore int
	db.QueryRow(`SELECT count(*) FROM receipts WHERE merchant_id = $1`, keep).Scan(&tjReceipts)
	db.QueryRow(`SELECT count(*) FROM stores WHERE merchant_id = $1 AND store_number IS NOT NULL`, keep).Scan(&stores)
	db.QueryRow(`SELECT count(*) FROM product_aliases WHERE merchant_id = $1`, keep).Scan(&aliases)
	db.QueryRow(`SELECT count(*) FROM receipts WHERE merchant_id IS NULL OR merchant_id NOT IN (SELECT id FROM merchants)`).Scan(&orphan)
	db.QueryRow(`SELECT count(*) FROM receipts WHERE store_id = 702`).Scan(&onDupStore)
	var cleared, numbered int
	db.QueryRow(`SELECT count(*) FROM product_aliases WHERE alias_text = 'BANANAS LOOSE' AND item_number IS NULL`).Scan(&cleared)
	db.QueryRow(`SELECT count(*) FROM product_aliases WHERE alias_text = 'BANANA BUNCH' AND item_number = '4011'`).Scan(&numbered)
	if cleared != 1 || numbered != 1 {
		t.Fatalf("item-number collision: cleared=%d numbered=%d", cleared, numbered)
	}
	if tjReceipts != 3 || stores != 2 || aliases != 4 || orphan != 0 || onDupStore != 0 {
		t.Fatalf("merge: receipts=%d stores=%d aliases=%d orphan=%d onDupStore=%d", tjReceipts, stores, aliases, orphan, onDupStore)
	}
}

// 006 trigram index exists and similarity() ranks a nickname to the right merchant.
func TestMerchantTrigramIndexAndSimilarity(t *testing.T) {
	db := testDB(t)
	var n int
	db.QueryRow(`SELECT count(*) FROM pg_indexes WHERE indexname = 'idx_merchants_name_trgm' AND indexdef LIKE '%gin_trgm_ops%'`).Scan(&n)
	if n != 1 {
		t.Fatal("idx_merchants_name_trgm missing")
	}
	for _, m := range []string{"trader_joes", "target", "costco", "whole_foods"} {
		if _, err := db.Exec(`INSERT INTO merchants (name) VALUES ($1)`, m); err != nil {
			t.Fatal(err)
		}
	}
	var best string
	var sim float64
	q := models.NormalizeMerchantName("Trader Joe")
	if err := db.QueryRow(`SELECT name, similarity(name, $1) FROM merchants ORDER BY similarity(name, $1) DESC LIMIT 1`, q).Scan(&best, &sim); err != nil {
		t.Fatal(err)
	}
	if best != "trader_joes" || sim <= 0.3 {
		t.Fatalf("best=%q sim=%v", best, sim)
	}
}

func TestItemAliasKeySQLGoParity(t *testing.T) {
	db := testDB(t)
	inputs := []string{"MILK 1/2 GAL", "  Soda\t 1.5L  ", "SODA 1,5L", "COKE-2L", "a\u00a0\u00a0b", "X__Y", "Ben & Jerry's",
		"ÉCLAIR 2CT", "", "   ", "!!", "line\r\nbreak", "tab\vvt\fff", "Ünited—“quotes”", "[brackets]\\back"}
	for _, in := range inputs {
		var sqlKey string
		if err := db.QueryRow(`SELECT item_alias_key($1)`, in).Scan(&sqlKey); err != nil {
			t.Fatal(err)
		}
		if goKey := models.ItemAliasKey(in); sqlKey != goKey {
			t.Errorf("%q: sql=%q go=%q", in, sqlKey, goKey)
		}
	}
}
