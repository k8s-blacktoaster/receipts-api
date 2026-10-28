package repository

import (
	"context"
	"errors"
	"testing"

	"receipts-db/internal/models"
	"receipts-db/internal/storage"
)

// QA: marking an incomplete (no merchant/date) failed receipt reviewed is ErrIncomplete, not a raw DB error.
func TestSetStatusIncompleteReceipt(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	rc := uploadFixture(t, NewSourceDocumentRepository(db), storage.NewMemory(), []byte{77})
	repo := NewReceiptRepository(db)
	if err := repo.SetStatus(ctx, rc.ID, models.StatusFailed, "boom", nil); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetStatus(ctx, rc.ID, models.StatusReviewed, "", nil); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("want ErrIncomplete, got %v", err)
	}
	got, _ := repo.GetByID(ctx, rc.ID)
	if got.Status != models.StatusFailed {
		t.Fatalf("status changed: %s", got.Status)
	}
}
