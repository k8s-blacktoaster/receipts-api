package repository

import (
	"database/sql"
	"strconv"
	"testing"
	"time"

	"receipts-db/internal/database"
)

// 015: blank merchant category / store address+phone become NULL (only changed rows touched),
// CHECKs keep blanks out, and down restores exactly the blanks it replaced.
func TestMigration015BlankStoreFields(t *testing.T) {
	db := testDB(t)
	migrateTo(t, db, 14)
	var blankM, okM, blankS, okS int64
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(db.QueryRow(`INSERT INTO merchants (name, category) VALUES ('blank_cat', '  ') RETURNING id`).Scan(&blankM))
	must(db.QueryRow(`INSERT INTO merchants (name, category) VALUES ('ok_cat', 'grocery') RETURNING id`).Scan(&okM))
	must(db.QueryRow(`INSERT INTO stores (merchant_id, store_number, address, phone) VALUES ($1, '1', '', ' 555-0100 ') RETURNING id`, okM).Scan(&blankS))
	must(db.QueryRow(`INSERT INTO stores (merchant_id, store_number, address, phone) VALUES ($1, '2', '9 Elm', '555-0101') RETURNING id`, okM).Scan(&okS))
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	// the update trigger would set now(); disable it just to plant an old timestamp
	must2(t, db, `ALTER TABLE merchants DISABLE TRIGGER update_merchants_updated_at`)
	must2(t, db, `ALTER TABLE stores DISABLE TRIGGER update_stores_updated_at`)
	_, err := db.Exec(`UPDATE merchants SET updated_at = $1`, old)
	must(err)
	_, err = db.Exec(`UPDATE stores SET updated_at = $1`, old)
	must(err)
	must2(t, db, `ALTER TABLE merchants ENABLE TRIGGER update_merchants_updated_at`)
	must2(t, db, `ALTER TABLE stores ENABLE TRIGGER update_stores_updated_at`)

	must(database.RunMigrations(db, "../../migrations"))
	var cat, addr, phone sql.NullString
	var mUpd, sUpd time.Time
	db.QueryRow(`SELECT category FROM merchants WHERE id = $1`, blankM).Scan(&cat)
	db.QueryRow(`SELECT address, phone FROM stores WHERE id = $1`, blankS).Scan(&addr, &phone)
	if cat.Valid || addr.Valid || phone.String != " 555-0100 " {
		t.Fatalf("up: cat %v addr %v phone %v", cat, addr, phone)
	}
	db.QueryRow(`SELECT updated_at FROM merchants WHERE id = $1`, okM).Scan(&mUpd)
	db.QueryRow(`SELECT updated_at FROM stores WHERE id = $1`, okS).Scan(&sUpd)
	if !mUpd.Equal(old) || !sUpd.Equal(old) {
		t.Fatalf("unchanged rows got updated_at bumped: %v %v", mUpd, sUpd)
	}
	db.QueryRow(`SELECT updated_at FROM merchants WHERE id = $1`, blankM).Scan(&mUpd)
	if mUpd.Equal(old) {
		t.Fatal("changed merchant kept the old updated_at")
	}
	for _, q := range []string{
		`UPDATE merchants SET category = ' ' WHERE id = ` + itoa(okM),
		`UPDATE stores SET address = '' WHERE id = ` + itoa(okS),
		`UPDATE stores SET phone = '  ' WHERE id = ` + itoa(okS),
	} {
		if _, err := db.Exec(q); err == nil {
			t.Fatalf("blank accepted: %s", q)
		}
	}

	must(rollbackTo(t, db, 14))
	db.QueryRow(`SELECT category FROM merchants WHERE id = $1`, blankM).Scan(&cat)
	db.QueryRow(`SELECT address FROM stores WHERE id = $1`, blankS).Scan(&addr)
	if cat.String != "  " || !addr.Valid || addr.String != "" {
		t.Fatalf("down did not restore blanks: %q %v", cat.String, addr)
	}
	var n int
	db.QueryRow(`SELECT count(*) FROM information_schema.tables WHERE table_name = 'migration_015_blanks'`).Scan(&n)
	if n != 0 {
		t.Fatal("down left migration_015_blanks")
	}
	must(database.RunMigrations(db, "../../migrations"))
	db.QueryRow(`SELECT category FROM merchants WHERE id = $1`, blankM).Scan(&cat)
	if cat.Valid {
		t.Fatalf("re-up: %v", cat)
	}
}

func must2(t *testing.T, db *database.DB, q string) {
	t.Helper()
	if _, err := db.Exec(q); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
