package repository

import (
	"database/sql"
	"strings"
	"testing"
	"unicode"

	"receipts-db/internal/database"
)

// TestGoBlankParity: SQL go_is_blank(s) == (strings.TrimSpace(s) == "") for every rune Go
// treats as space, plus look-alikes Go keeps (zero-width space, BOM, NUL-free controls).
func TestGoBlankParity(t *testing.T) {
	db := testDB(t)
	var cases []string
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if unicode.IsSpace(r) {
			cases = append(cases, string(r), " "+string(r)+"\t", string(r)+"x")
		}
	}
	cases = append(cases, "", "a", "\u200B", "\uFEFF", "\u180E", "\u0001", "\u00A0\u3000 x \u2028", "\t\n\v\f\r \u0085\u00A0")
	for _, s := range cases {
		var got bool
		if err := db.QueryRow(`SELECT go_is_blank($1)`, s).Scan(&got); err != nil {
			t.Fatalf("%q: %v", s, err)
		}
		if want := strings.TrimSpace(s) == ""; got != want {
			t.Fatalf("go_is_blank(%q) = %v, Go TrimSpace blank = %v", s, got, want)
		}
	}
	var null sql.NullBool
	db.QueryRow(`SELECT go_is_blank(NULL)`).Scan(&null)
	if null.Valid {
		t.Fatal("go_is_blank(NULL) must be NULL (CHECK passes for NULL)")
	}
}

// 016: Go-whitespace-only category/address/phone -> NULL, CHECKs reject them, exact down.
func TestMigration016GoWhitespaceBlank(t *testing.T) {
	db := testDB(t)
	migrateTo(t, db, 15)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	var m1, m2, s1 int64
	must(db.QueryRow(`INSERT INTO merchants (name, category) VALUES ('tab_cat', E'\t\n') RETURNING id`).Scan(&m1))
	must(db.QueryRow(`INSERT INTO merchants (name, category) VALUES ('nbsp_ok', E'\u00A0deli\u00A0') RETURNING id`).Scan(&m2))
	must(db.QueryRow(`INSERT INTO stores (merchant_id, store_number, address, phone) VALUES ($1, '1', E'\u00A0', E'\u3000') RETURNING id`, m2).Scan(&s1))
	var upd0 string
	db.QueryRow(`SELECT updated_at::text FROM merchants WHERE id = $1`, m2).Scan(&upd0)

	must(database.RunMigrations(db, "../../migrations"))
	var c1, c2, addr, phone sql.NullString
	db.QueryRow(`SELECT category FROM merchants WHERE id = $1`, m1).Scan(&c1)
	db.QueryRow(`SELECT category FROM merchants WHERE id = $1`, m2).Scan(&c2)
	db.QueryRow(`SELECT address, phone FROM stores WHERE id = $1`, s1).Scan(&addr, &phone)
	if c1.Valid || c2.String != "\u00A0deli\u00A0" || addr.Valid || phone.Valid {
		t.Fatalf("up: %v %v %v %v", c1, c2, addr, phone)
	}
	var upd1 string
	db.QueryRow(`SELECT updated_at::text FROM merchants WHERE id = $1`, m2).Scan(&upd1)
	if upd1 != upd0 {
		t.Fatal("unchanged merchant got updated_at bumped")
	}
	for _, v := range []string{"\t", "\u00A0", "\u2028 ", " "} {
		if _, err := db.Exec(`UPDATE merchants SET category = $1 WHERE id = $2`, v, m2); err == nil {
			t.Fatalf("CHECK accepted %q", v)
		}
	}

	must(database.RollbackMigrations(db, "../../migrations", 1))
	db.QueryRow(`SELECT category FROM merchants WHERE id = $1`, m1).Scan(&c1)
	db.QueryRow(`SELECT address, phone FROM stores WHERE id = $1`, s1).Scan(&addr, &phone)
	if c1.String != "\t\n" || addr.String != "\u00A0" || phone.String != "\u3000" {
		t.Fatalf("down: %q %q %q", c1.String, addr.String, phone.String)
	}
	var n int
	db.QueryRow(`SELECT count(*) FROM pg_proc WHERE proname = 'go_is_blank'`).Scan(&n)
	if n != 0 {
		t.Fatal("down left go_is_blank")
	}
	if _, err := db.Exec(`UPDATE merchants SET category = ' ' WHERE id = $1`, m2); err == nil {
		t.Fatal("down: 015 CHECK not restored")
	}
	must(database.RunMigrations(db, "../../migrations"))
	db.QueryRow(`SELECT category FROM merchants WHERE id = $1`, m1).Scan(&c1)
	if c1.Valid {
		t.Fatal("re-up")
	}
}
