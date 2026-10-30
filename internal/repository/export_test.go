package repository

import (
	"context"

	"receipts-db/internal/database"
	"receipts-db/internal/models"
)

// TestDB exposes the integration-test DB helper to the external repository_test package
// (which may import handlers without an import cycle).
var TestDB = testDB

// WriteReceiptLenientForTest runs the extraction write path (strict=false) for one receipt.
func WriteReceiptLenientForTest(ctx context.Context, db *database.DB, rc *models.Receipt) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := writeReceiptTx(ctx, tx, rc, false, false); err != nil {
		return err
	}
	return tx.Commit()
}
