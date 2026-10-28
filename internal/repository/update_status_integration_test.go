package repository

import (
	"context"
	"errors"
	"testing"

	"receipts-db/internal/models"
)

func TestUpdateRoutesStatusThroughTransitionTable(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	repo := NewReceiptRepository(db)
	rc := &models.Receipt{StoreName: "Sprouts", Date: "2026-10-01", Source: models.SourcePhoto, Status: models.StatusParsed, SubtotalCents: 100, TotalCents: 100}
	if err := repo.Create(ctx, rc); err != nil {
		t.Fatal(err)
	}
	edit := func(status, name string) error {
		got, err := repo.GetByID(ctx, rc.ID)
		if err != nil {
			t.Fatal(err)
		}
		got.Status, got.StoreName = status, name
		return repo.Update(ctx, got)
	}
	// valid change
	if err := edit(models.StatusReviewed, "Sprouts A"); err != nil {
		t.Fatal(err)
	}
	// no status = keep current (must not reset to a default)
	if err := edit("", "Sprouts B"); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.GetByID(ctx, rc.ID); got.Status != models.StatusReviewed || got.StoreName != "Sprouts B" {
		t.Fatalf("after keep: %s %s", got.Status, got.StoreName)
	}
	// same status
	if err := edit(models.StatusReviewed, "Sprouts C"); err != nil {
		t.Fatal(err)
	}
	// invalid moves leave the row unchanged
	for _, bad := range []string{models.StatusUploaded, models.StatusParsing, models.StatusParsed} {
		if err := edit(bad, "SHOULD NOT SAVE"); !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("reviewed -> %s: %v", bad, err)
		}
	}
	if got, _ := repo.GetByID(ctx, rc.ID); got.Status != models.StatusReviewed || got.StoreName != "Sprouts C" {
		t.Fatalf("row changed by rejected update: %s %s", got.Status, got.StoreName)
	}
	// entering parsing is reserved for ClaimForParsing even where the table allows it
	if err := edit(models.StatusNeedsReview, "Sprouts D"); err != nil {
		t.Fatal(err)
	}
	if err := edit(models.StatusParsing, "Sprouts E"); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("needs_review -> parsing via Update: %v", err)
	}
	// worker-only targets
	for _, bad := range []string{models.StatusFailed, models.StatusUploaded} {
		if err := edit(bad, "nope"); !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("needs_review -> %s via Update: %v", bad, err)
		}
	}
	// while parsing, every edit is refused (even without a status) and nothing changes
	if _, err := db.Exec(`UPDATE receipts SET status='parsing' WHERE id=$1`, rc.ID); err != nil {
		t.Fatal(err)
	}
	for _, st := range []string{"", models.StatusParsing, models.StatusReviewed} {
		if err := edit(st, "RACING EDIT"); !errors.Is(err, ErrReceiptBusy) {
			t.Fatalf("edit while parsing (%q): %v", st, err)
		}
	}
	if got, _ := repo.GetByID(ctx, rc.ID); got.StoreName != "Sprouts D" || got.Status != models.StatusParsing {
		t.Fatalf("busy edit changed row: %s %s", got.Status, got.StoreName)
	}
	// missing receipt
	if err := repo.Update(ctx, &models.Receipt{ID: 999999, StoreName: "X", Date: "2026-10-01"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
}
