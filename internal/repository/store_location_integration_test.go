package repository

import (
	"context"
	"testing"

	"receipts-db/internal/models"
)

// B: extracted store location (number, street, city, state, zip, phone) is upserted on the
// stores row by the F6 key and linked; a repeat receipt reuses the row, and a later receipt
// missing some fields does not wipe them.
func TestStoreLocationUpsertAndDedupe(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	repo := NewReceiptRepository(db)
	first := &models.Receipt{StoreName: "Fake Grocer", StoreNumber: "0042", StoreAddress: "100 Test Ave",
		StoreCity: "Springfield", StoreState: "ZZ", StoreZip: "00001", StorePhone: "555-0100", Date: "2026-10-01"}
	repeat := &models.Receipt{StoreName: "FAKE GROCER", StoreNumber: "0042", StoreCity: "Springfield", Date: "2026-10-02"}
	for _, r := range []*models.Receipt{first, repeat} {
		if err := repo.Create(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	if first.StoreID == nil || repeat.StoreID == nil || *first.StoreID != *repeat.StoreID {
		t.Fatalf("expected one store row, got %v vs %v", first.StoreID, repeat.StoreID)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM stores WHERE merchant_id = $1`, first.MerchantID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("stores for merchant: %d %v", n, err)
	}
	got, err := repo.GetByID(ctx, repeat.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.StoreNumber != "0042" || got.StoreCity != "Springfield" || got.StoreState != "ZZ" || got.StoreZip != "00001" {
		t.Fatalf("location not kept: %+v", got)
	}
	// address-keyed store (no number) also carries city/state/zip
	c := &models.Receipt{StoreName: "Fake Grocer", StoreAddress: "200 Other Rd", StoreCity: "Shelbyville", StoreState: "ZZ",
		StoreZip: "00002", Date: "2026-10-03"}
	if err := repo.Create(ctx, c); err != nil {
		t.Fatal(err)
	}
	if got, _ = repo.GetByID(ctx, c.ID); got.StoreCity != "Shelbyville" || got.StoreZip != "00002" || *got.StoreID == *first.StoreID {
		t.Fatalf("address store: %+v", got)
	}
}
