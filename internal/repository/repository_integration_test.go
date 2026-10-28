package repository

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"

	"receipts-db/internal/database"
	"receipts-db/internal/models"
)

// Integration tests run only when TEST_DATABASE_URL is set. They DROP and recreate the schema,
// so the database name must contain "test".
func testDB(t *testing.T) *database.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping integration test")
	}
	u, err := url.Parse(dsn)
	if err != nil || !strings.Contains(strings.TrimPrefix(u.Path, "/"), "test") {
		t.Fatalf("refusing to run destructive tests: database name must contain 'test' (%q)", dsn)
	}
	db, err := database.NewConnection(database.Config{URL: dsn})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := database.RollbackMigrations(db, "../../migrations", 0); err != nil {
		t.Fatalf("down: %v", err)
	}
	if err := database.RunMigrations(db, "../../migrations"); err != nil {
		t.Fatalf("up: %v", err)
	}
	return db
}

// migrateTo resets the schema and applies exactly the first n migrations.
func migrateTo(t *testing.T, db *database.DB, n int) {
	t.Helper()
	if err := database.RollbackMigrations(db, "../../migrations", 0); err != nil {
		t.Fatal(err)
	}
	if err := database.RunMigrationsN(db, "../../migrations", n); err != nil {
		t.Fatal(err)
	}
}

func ptr[T any](v T) *T { return &v }

func TestMigrationsUpDownUp(t *testing.T) {
	db := testDB(t)
	if err := database.RollbackMigrations(db, "../../migrations", 0); err != nil {
		t.Fatalf("down all: %v", err)
	}
	if err := database.RunMigrations(db, "../../migrations"); err != nil {
		t.Fatalf("up again: %v", err)
	}
}

func TestReceiptRoundTripExactCents(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	members := NewMemberRepository(db)
	jack := &models.Member{DisplayName: "Jack", Email: "jack@example.com"}
	if err := members.Create(ctx, jack); err != nil {
		t.Fatal(err)
	}
	repo := NewReceiptRepository(db)
	in := &models.Receipt{
		StoreName: "Home Depot", StoreCategory: "hardware", StoreNumber: "0617", StoreAddress: "1 Main St",
		Date: "2026-10-01", SubtotalCents: 1999, TaxCents: 165, SavingsCents: 300, Source: models.SourcePhoto,
		MemberID: &jack.ID, UploadedByID: &jack.ID,
		Items: []models.ReceiptItem{
			{LineNo: ptr(1), RawText: "WOOD SCRW 2IN", SKU: "1001", ItemNumber: "305678", UPC: "012345678905",
				Name: "Wood screws", Quantity: 2, UnitPriceCents: 649, DiscountCents: 300},
			{LineNo: ptr(2), Name: "Bananas", Quantity: 1.42, Unit: "lb", UnitPriceCents: 69},
		},
		Adjustments: []models.ReceiptAdjustment{{Kind: "instant_savings", RawText: "INST SV", AmountCents: -300}},
	}
	if err := repo.Create(ctx, in); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetByID(ctx, in.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.TotalCents != 2164 || got.TaxCents != 165 || got.SavingsCents != 300 {
		t.Fatalf("totals: %+v", got)
	}
	if got.StoreID == nil || got.StoreNumber != "0617" || got.MemberID == nil || *got.MemberID != jack.ID {
		t.Fatalf("store/member not linked: %+v", got)
	}
	if got.Status != models.StatusUploaded {
		t.Fatalf("status = %q", got.Status)
	}
	if len(got.Items) != 2 || got.Items[0].NetTotalCents != 998 || got.Items[1].LineTotalCents != 98 ||
		got.Items[1].Quantity != 1.42 || got.Items[0].UPC != "012345678905" || got.Items[0].ItemNumber != "305678" {
		t.Fatalf("items: %+v", got.Items)
	}
	if len(got.Adjustments) != 1 || got.Adjustments[0].AmountCents != -300 {
		t.Fatalf("adjustments: %+v", got.Adjustments)
	}

	// second receipt at the same store number reuses the store row
	in2 := &models.Receipt{StoreName: "home depot", StoreNumber: "0617", Date: "2026-10-05", SubtotalCents: 100}
	if err := repo.Create(ctx, in2); err != nil {
		t.Fatal(err)
	}
	if in2.MerchantID != in.MerchantID || *in2.StoreID != *in.StoreID {
		t.Fatalf("merchant/store not reused: %d/%d vs %d/%d", in2.MerchantID, *in2.StoreID, in.MerchantID, *in.StoreID)
	}
	if in2.Status != models.StatusReviewed { // manual entry default
		t.Fatalf("manual status = %q", in2.Status)
	}

	// review: edit a line item (photo receipt must be past extraction: edits are refused while uploaded/parsing)
	if _, err := db.Exec(`UPDATE receipts SET status = 'parsed' WHERE id = $1`, in.ID); err != nil {
		t.Fatal(err)
	}
	it := got.Items[1]
	it.Quantity, it.LineTotalCents = 2.0, 0
	if err := repo.UpdateItem(ctx, &it); err != nil {
		t.Fatal(err)
	}
	if it.NetTotalCents != 138 || it.EditedAt == nil {
		t.Fatalf("edited item: %+v", it)
	}
	if _, err := db.Exec(`UPDATE receipts SET status = 'uploaded' WHERE id = $1`, in.ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetStatus(ctx, in.ID, models.StatusFailed, "could not read total", nil); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetStatus(ctx, in.ID, models.StatusReviewed, "", &jack.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = repo.GetByID(ctx, in.ID)
	if got.Status != models.StatusReviewed || got.ReviewedAt == nil || *got.ReviewedByID != jack.ID || got.ParseError != "" {
		t.Fatalf("review status: %+v", got)
	}
	if err := repo.SetStatus(ctx, in.ID, "bogus", "", nil); err == nil {
		t.Fatal("check constraint should reject unknown status")
	}

	list, err := repo.List(ctx, 10, 0)
	if err != nil || len(list) != 2 {
		t.Fatalf("list: %v %d", err, len(list))
	}
	if err := repo.Delete(ctx, in2.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetByID(ctx, in2.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestWhereDidIBuyAndMerchantScopedAliases(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	receipts := NewReceiptRepository(db)
	products := NewProductRepository(db)

	eggs := &models.Product{CanonicalName: "Organic Eggs, 24 ct", Category: "grocery"}
	if err := products.CreateProduct(ctx, eggs); err != nil {
		t.Fatal(err)
	}
	mk := func(store, num, addr, date string, price int64) *models.Receipt {
		r := &models.Receipt{StoreName: store, StoreNumber: num, StoreAddress: addr, Date: date, SubtotalCents: price,
			Items: []models.ReceiptItem{{Name: "eggs", ProductID: &eggs.ID, Quantity: 1, UnitPriceCents: price}}}
		if err := receipts.Create(ctx, r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	a := mk("Costco", "123", "Tustin", "2026-08-01", 899)
	b := mk("Safeway", "", "2 Elm St", "2026-09-15", 1099)
	mk("Costco", "456", "Irvine", "2026-10-20", 949)

	// merchant-scoped aliases: same text at two merchants is fine; duplicate within a merchant upserts
	for _, al := range []models.ProductAlias{
		{ProductID: eggs.ID, MerchantID: a.MerchantID, AliasText: "KS ORG EGGS", ItemNumber: "1234"},
		{ProductID: eggs.ID, MerchantID: b.MerchantID, AliasText: "KS ORG EGGS"},
		{ProductID: eggs.ID, MerchantID: a.MerchantID, AliasText: "ks org eggs", Source: "exact"},
	} {
		if err := products.UpsertAlias(ctx, &al); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	db.QueryRow(`SELECT count(*) FROM product_aliases`).Scan(&n)
	if n != 2 {
		t.Fatalf("aliases = %d, want 2", n)
	}
	if id, err := products.ResolveAlias(ctx, a.MerchantID, "1234", ""); err != nil || id != eggs.ID {
		t.Fatalf("resolve by item number: %d %v", id, err)
	}
	if id, err := products.ResolveAlias(ctx, b.MerchantID, "", "ks org eggs"); err != nil || id != eggs.ID {
		t.Fatalf("resolve by text: %d %v", id, err)
	}
	if _, err := products.ResolveAlias(ctx, b.MerchantID, "1234", "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("item number must be merchant-scoped, got %v", err)
	}

	locs, err := products.WhereBought(ctx, eggs.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(locs) != 3 {
		t.Fatalf("locations = %d", len(locs))
	}
	if locs[0].MerchantName != "costco" || locs[0].StoreNumber != "456" || locs[0].PurchaseDate != "2026-10-20" ||
		locs[1].MerchantName != "safeway" || locs[1].StoreAddress != "2 Elm St" || locs[2].UnitPriceCents != 899 {
		t.Fatalf("unexpected locations: %+v", locs)
	}

	// the where-bought path is index-backed
	db.Exec(`SET enable_seqscan = off`)
	var plan strings.Builder
	rows, _ := db.Query(`EXPLAIN SELECT * FROM product_purchase_locations_view WHERE product_id = $1`, eggs.ID)
	for rows.Next() {
		var l string
		rows.Scan(&l)
		plan.WriteString(l + "\n")
	}
	rows.Close()
	if !strings.Contains(plan.String(), "idx_receipt_items_product") {
		t.Fatalf("expected idx_receipt_items_product in plan:\n%s", plan.String())
	}
}
