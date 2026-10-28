package repository

import (
	"context"
	"testing"

	"receipts-db/internal/database"
	"receipts-db/internal/models"
)

// Learning the same alias repeatedly (case/whitespace variants) keeps exactly one row, pointing
// at the latest product.
func TestAliasLearningUpserts(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	products, receipts := NewProductRepository(db), NewReceiptRepository(db)
	seed := &models.Receipt{StoreName: "Acme", Date: "2026-07-01", Source: models.SourceManual, Items: []models.ReceiptItem{{Name: "seed", Quantity: 1}}}
	if err := receipts.Create(ctx, seed); err != nil {
		t.Fatal(err)
	}
	var last int64
	for i, text := range []string{"Oat Milk 1/2 GAL", "oat milk 1/2 gal", "  OAT   MILK 1/2\tGAL "} {
		p := &models.Product{CanonicalName: "Oat milk v" + string(rune('1'+i))}
		_ = products.CreateProduct(ctx, p)
		// direct learn path (both insert branches) and the repository upsert
		if _, err := learnAlias(ctx, db, seed.MerchantID, text, "", p.ID); err != nil {
			t.Fatal(err)
		}
		if err := products.UpsertAlias(ctx, &models.ProductAlias{ProductID: p.ID, MerchantID: seed.MerchantID, AliasText: text}); err != nil {
			t.Fatal(err)
		}
		last = p.ID
	}
	// review-map of a whitespace variant line also lands on the same row
	r := &models.Receipt{StoreName: "Acme", Date: "2026-07-02", Source: models.SourceManual, Items: []models.ReceiptItem{{Name: "OAT MILK  1/2 GAL", Quantity: 1}}}
	_ = receipts.Create(ctx, r)
	p := &models.Product{CanonicalName: "Oat milk final"}
	if _, err := products.MapItem(ctx, int64(r.Items[0].ID), 0, p); err != nil {
		t.Fatal(err)
	}
	last = p.ID
	var n int
	var pid int64
	if err := db.QueryRowContext(ctx, `SELECT count(*), max(product_id) FROM product_aliases
		WHERE merchant_id = $1 AND item_alias_key(alias_text) = 'oat milk 1/2 gal'`, seed.MerchantID).Scan(&n, &pid); err != nil {
		t.Fatal(err)
	}
	if n != 1 || pid != last {
		t.Fatalf("rows=%d product=%d want 1 row -> %d", n, pid, last)
	}
}

// Migration 013 dedupes existing key-duplicates: oldest row kept, codes carried over.
func TestAliasKeyDedupeMigration(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	migrateTo(t, db, 12)
	for _, q := range []string{
		`INSERT INTO merchants (id, name) VALUES (1,'acme')`,
		`INSERT INTO products (id, canonical_name) VALUES (1,'A'),(2,'B')`,
		`INSERT INTO product_aliases (id, product_id, merchant_id, alias_text, item_number, upc) VALUES
			(10,1,1,'MILK  GAL',NULL,NULL),(11,2,1,'milk gal','77','012345678905'),(12,2,1,'Milk Gal ','78',NULL),(13,1,1,'MILK 1/2 GAL',NULL,NULL)`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := database.RunMigrations(db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	var ids []int64
	rows, _ := db.QueryContext(ctx, `SELECT id FROM product_aliases ORDER BY id`)
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	var item, upc string
	var pid int64
	db.QueryRowContext(ctx, `SELECT product_id, coalesce(item_number,''), coalesce(upc,'') FROM product_aliases WHERE id = 10`).Scan(&pid, &item, &upc)
	if len(ids) != 2 || ids[0] != 10 || ids[1] != 13 || pid != 1 || item != "77" || upc != "012345678905" {
		t.Fatalf("ids=%v keeper product=%d item=%q upc=%q", ids, pid, item, upc)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO product_aliases (product_id, merchant_id, alias_text) VALUES (1,1,'milk   GAL')`); err == nil {
		t.Fatal("unique (merchant, item_alias_key) missing")
	}
}
