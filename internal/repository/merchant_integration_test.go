package repository

import (
	"context"
	"errors"
	"testing"

	"receipts-db/internal/models"
)

func TestMerchantRename(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	receipts, merchants := NewReceiptRepository(db), NewMerchantRepository(db)
	a := &models.Receipt{StoreName: "Sprouts", Date: "2026-10-01", Items: []models.ReceiptItem{{Name: "Milk", Quantity: 1, UnitPriceCents: 399}}}
	b := &models.Receipt{StoreName: "Trader Joe's", Date: "2026-10-02"}
	for _, r := range []*models.Receipt{a, b} {
		if err := receipts.Create(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	m, err := merchants.Rename(ctx, a.MerchantID, "  Sprouts Farmers Market! ")
	if err != nil || m.Name != "sprouts_farmers_market" || m.ReceiptCount != 1 {
		t.Fatalf("rename: %+v %v", m, err)
	}
	// same key: no-op
	if m2, err := merchants.Rename(ctx, a.MerchantID, "SPROUTS-farmers market"); err != nil || m2.Name != m.Name {
		t.Fatalf("same key: %+v %v", m2, err)
	}
	// collision: 409-style conflict naming the existing merchant, nothing changed
	_, err = merchants.Rename(ctx, a.MerchantID, "Trader Joes")
	var conflict *MerchantConflict
	if !errors.Is(err, ErrMerchantNameTaken) || !errors.As(err, &conflict) || conflict.Existing.ID != b.MerchantID {
		t.Fatalf("collision: %v", err)
	}
	if got, _ := merchants.GetByID(ctx, a.MerchantID); got.Name != "sprouts_farmers_market" {
		t.Fatalf("collision changed name: %s", got.Name)
	}
	if _, err := merchants.Rename(ctx, a.MerchantID, " !! "); !errors.Is(err, ErrInvalidMerchantName) {
		t.Fatalf("empty key: %v", err)
	}
	if _, err := merchants.Rename(ctx, 999999, "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	// editing the receipt without changing its printed store name keeps the renamed merchant
	got, _ := receipts.GetByID(ctx, a.ID)
	got.Status = ""
	got.Items[0].Name = "Oat milk"
	if err := receipts.Update(ctx, got); err != nil {
		t.Fatal(err)
	}
	after, _ := receipts.GetByID(ctx, a.ID)
	if after.MerchantID != a.MerchantID {
		t.Fatalf("edit re-derived merchant: %d -> %d", a.MerchantID, after.MerchantID)
	}
	all, _ := merchants.List(ctx)
	if len(all) != 2 {
		t.Fatalf("merchants after edit: %+v", all)
	}
	// changing the printed name does re-link
	after.Status, after.StoreName = "", "Trader Joe's"
	if err := receipts.Update(ctx, after); err != nil {
		t.Fatal(err)
	}
	if g, _ := receipts.GetByID(ctx, a.ID); g.MerchantID != b.MerchantID {
		t.Fatalf("relink: %d", g.MerchantID)
	}
}

// Updating a receipt re-inserts items; adjustments tied to an item follow it (or detach if the
// item was removed) instead of failing on a dangling FK.
func TestUpdateRemapsAdjustmentItem(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	repo := NewReceiptRepository(db)
	rc := &models.Receipt{StoreName: "Sprouts", Date: "2026-10-01", Items: []models.ReceiptItem{
		{Name: "Milk", Quantity: 1, UnitPriceCents: 399}, {Name: "Eggs", Quantity: 1, UnitPriceCents: 499}}}
	if err := repo.Create(ctx, rc); err != nil {
		t.Fatal(err)
	}
	got, _ := repo.GetByID(ctx, rc.ID)
	eggs := got.Items[1].ID
	got.Adjustments = []models.ReceiptAdjustment{{Kind: "coupon", AmountCents: -100, AppliesToID: &eggs}, {Kind: "coupon", AmountCents: -50, AppliesToID: &got.Items[0].ID}}
	got.Status = ""
	if err := repo.Update(ctx, got); err != nil {
		t.Fatal(err)
	}
	got, _ = repo.GetByID(ctx, rc.ID)
	got.Status = ""
	got.Items = got.Items[1:] // remove Milk
	if err := repo.Update(ctx, got); err != nil {
		t.Fatalf("update with remapped adjustments: %v", err)
	}
	got, _ = repo.GetByID(ctx, rc.ID)
	if len(got.Adjustments) != 2 {
		t.Fatalf("adjustments: %+v", got.Adjustments)
	}
	var attached, detached int
	for _, a := range got.Adjustments {
		if a.AppliesToID != nil && *a.AppliesToID == got.Items[0].ID {
			attached++
		} else if a.AppliesToID == nil {
			detached++
		}
	}
	if attached != 1 || detached != 1 {
		t.Fatalf("attached=%d detached=%d %+v", attached, detached, got.Adjustments)
	}
}
