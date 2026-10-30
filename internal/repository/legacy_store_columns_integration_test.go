package repository

import (
	"context"
	"database/sql"
	"testing"

	"receipts-db/internal/database"
	"receipts-db/internal/models"
)

func receiptColumns(t *testing.T, db *database.DB) map[string]bool {
	t.Helper()
	rows, err := db.Query(`SELECT column_name FROM information_schema.columns WHERE table_name = 'receipts'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols := map[string]bool{}
	for rows.Next() {
		var c string
		rows.Scan(&c)
		cols[c] = true
	}
	return cols
}

// Phase 7 / migration 014: legacy receipts.store_* dropped (data copied into merchants/stores
// first), store_name kept as printed_merchant_name; down restores and refills the columns.
func TestMigration014LegacyStoreColumns(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	migrateTo(t, db, 13)
	var mid, sid, rid int64
	if err := db.QueryRow(`INSERT INTO merchants (name) VALUES ('corner_shop') RETURNING id`).Scan(&mid); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO stores (merchant_id, store_number) VALUES ($1, '12') RETURNING id`, mid).Scan(&sid); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO receipts (store_name, store_category, store_address, store_phone, merchant_id, store_id, date, status)
		VALUES ('Corner Shop', 'grocery', '1 Main St', '555-0100', $1, $2, '2026-01-02', 'reviewed') RETURNING id`, mid, sid).Scan(&rid); err != nil {
		t.Fatal(err)
	}

	if err := database.RunMigrations(db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	cols := receiptColumns(t, db)
	for _, c := range []string{"store_name", "store_category", "store_address", "store_phone"} {
		if cols[c] {
			t.Fatalf("column %s still present after 014", c)
		}
	}
	if !cols["printed_merchant_name"] {
		t.Fatal("printed_merchant_name missing")
	}
	var cat, addr, phone sql.NullString
	db.QueryRow(`SELECT category FROM merchants WHERE id = $1`, mid).Scan(&cat)
	db.QueryRow(`SELECT address, phone FROM stores WHERE id = $1`, sid).Scan(&addr, &phone)
	if cat.String != "grocery" || addr.String != "1 Main St" || phone.String != "555-0100" {
		t.Fatalf("014 did not copy legacy values: %v %v %v", cat, addr, phone)
	}
	// The repository reads them back from merchants/stores.
	rc, err := NewReceiptRepository(db).GetByID(ctx, int(rid))
	if err != nil {
		t.Fatal(err)
	}
	if rc.StoreName != "Corner Shop" || rc.StoreCategory != "grocery" || rc.StoreAddress != "1 Main St" ||
		rc.StorePhone != "555-0100" || rc.StoreNumber != "12" {
		t.Fatalf("receipt read %+v", rc)
	}

	// down 1: columns come back, refilled from the normalized tables.
	if err := database.RollbackMigrations(db, "../../migrations", 1); err != nil {
		t.Fatal(err)
	}
	var name string
	if err := db.QueryRow(`SELECT store_name, store_category, store_address, store_phone FROM receipts WHERE id = $1`, rid).
		Scan(&name, &cat, &addr, &phone); err != nil {
		t.Fatal(err)
	}
	if name != "Corner Shop" || cat.String != "grocery" || addr.String != "1 Main St" || phone.String != "555-0100" {
		t.Fatalf("down: %q %v %v %v", name, cat, addr, phone)
	}
	var idx int
	db.QueryRow(`SELECT count(*) FROM pg_indexes WHERE tablename = 'receipts' AND indexname IN ('idx_receipts_store_name','idx_receipts_store_date')`).Scan(&idx)
	if idx != 2 {
		t.Fatalf("down: legacy indexes = %d", idx)
	}
	// and up again
	if err := database.RunMigrations(db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	if cols := receiptColumns(t, db); cols["store_name"] || !cols["printed_merchant_name"] {
		t.Fatalf("re-up columns %v", cols)
	}
}

// Create/Update round-trip the API's store_* fields through merchants/stores.
func TestReceiptStoreFieldsViaNormalizedTables(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	repo := NewReceiptRepository(db)
	r := &models.Receipt{StoreName: "Elm Market", StoreCategory: "grocery", StoreNumber: "7", StoreAddress: "9 Elm",
		StorePhone: "555-0199", Date: "2026-03-01"}
	if err := repo.Create(ctx, r); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetByID(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.StoreName != "Elm Market" || got.StoreCategory != "grocery" || got.StoreAddress != "9 Elm" || got.StorePhone != "555-0199" || got.StoreNumber != "7" {
		t.Fatalf("got %+v", got)
	}
	list, err := repo.List(ctx, 10, 0)
	if err != nil || len(list) == 0 || list[0].StoreAddress != "9 Elm" {
		t.Fatalf("list %+v %v", list, err)
	}
}
