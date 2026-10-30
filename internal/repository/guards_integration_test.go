package repository

import (
	"context"
	"errors"
	"testing"

	"receipts-db/internal/models"
)

// Edits are refused while uploaded/parsing; DELETE while parsing; RequestReextract rules.
func TestEditGuardsAndReextractRequest(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	repo, docs := NewReceiptRepository(db), NewSourceDocumentRepository(db)
	rc := &models.Receipt{Source: models.SourcePhoto, Status: models.StatusUploaded}
	if err := docs.CreateUpload(ctx, &models.SourceDocument{ObjectKey: "receipts/g.png", SHA256: []byte("guard-sha-00000000000000000000000"), MimeType: "image/png"}, rc); err != nil {
		t.Fatal(err)
	}
	setStatus := func(st string) {
		t.Helper()
		if _, err := db.Exec(`UPDATE receipts SET status=$2, printed_merchant_name='Sprouts', date='2026-10-01',
			merchant_id=(SELECT id FROM merchants LIMIT 1) WHERE id=$1`, rc.ID, st); err != nil {
			t.Fatal(err)
		}
	}
	// a parsed baseline with one item so there is a merchant and an item id
	full := &models.Receipt{StoreName: "Sprouts", Date: "2026-10-01", Source: models.SourcePhoto, Status: models.StatusParsed,
		Items: []models.ReceiptItem{{Name: "Milk", Quantity: 1, UnitPriceCents: 399}}}
	if err := repo.Create(ctx, full); err != nil {
		t.Fatal(err)
	}
	got, _ := repo.GetByID(ctx, full.ID)
	item := got.Items[0]

	for _, st := range []string{models.StatusUploaded, models.StatusParsing} {
		if _, err := db.Exec(`UPDATE receipts SET status=$2 WHERE id=$1`, full.ID, st); err != nil {
			t.Fatal(err)
		}
		edit, _ := repo.GetByID(ctx, full.ID)
		edit.Status, edit.StoreName = "", "EDITED"
		edit.Adjustments = append(edit.Adjustments, models.ReceiptAdjustment{Kind: "coupon", AmountCents: -50})
		if err := repo.Update(ctx, edit); !errors.Is(err, ErrReceiptBusy) {
			t.Fatalf("Update while %s: %v", st, err)
		}
		it := item
		it.Name = "EDITED"
		if err := repo.UpdateItem(ctx, &it); !errors.Is(err, ErrReceiptBusy) {
			t.Fatalf("UpdateItem while %s: %v", st, err)
		}
		after, _ := repo.GetByID(ctx, full.ID)
		if after.StoreName == "EDITED" || after.Items[0].Name == "EDITED" || len(after.Adjustments) != 0 {
			t.Fatalf("guarded edit leaked while %s: %+v", st, after)
		}
	}
	// DELETE: refused while parsing, allowed otherwise
	if err := repo.Delete(ctx, full.ID); !errors.Is(err, ErrReceiptBusy) {
		t.Fatalf("delete while parsing: %v", err)
	}
	if _, err := db.Exec(`UPDATE receipts SET status='parsed' WHERE id=$1`, full.ID); err != nil {
		t.Fatal(err)
	}
	it := item
	it.Name = "Oat milk"
	if err := repo.UpdateItem(ctx, &it); err != nil {
		t.Fatalf("UpdateItem when parsed: %v", err)
	}

	// RequestReextract
	if err := repo.RequestReextract(ctx, full.ID); !errors.Is(err, ErrNoPhoto) {
		t.Fatalf("manual-style receipt without photo: %v", err)
	}
	setStatus(models.StatusParsed)
	if err := repo.RequestReextract(ctx, rc.ID); err != nil {
		t.Fatal(err)
	}
	if g, _ := repo.GetByID(ctx, rc.ID); g.Status != models.StatusUploaded {
		t.Fatalf("after request: %s", g.Status)
	}
	setStatus(models.StatusParsing)
	if err := repo.RequestReextract(ctx, rc.ID); !errors.Is(err, ErrReceiptBusy) {
		t.Fatalf("while parsing: %v", err)
	}
	setStatus(models.StatusReviewed)
	if err := repo.RequestReextract(ctx, rc.ID); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("reviewed: %v", err)
	}
	if err := repo.RequestReextract(ctx, 999999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if err := repo.Delete(ctx, full.ID); err != nil {
		t.Fatalf("delete parsed: %v", err)
	}
}
