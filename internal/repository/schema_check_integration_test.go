package repository

import (
	"context"
	"errors"
	"slices"
	"testing"

	"receipts-db/internal/database"
)

// /readyz schema gate: down-level, ahead (unknown version) and gaps are all a mismatch.
func TestCheckSchema(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	want, err := database.MigrationVersions("../../migrations")
	if err != nil || len(want) == 0 {
		t.Fatalf("versions %v %v", want, err)
	}
	if err := database.CheckSchema(ctx, db, want); err != nil {
		t.Fatalf("fully migrated: %v", err)
	}
	var mm *database.SchemaMismatchError
	// down-level by one
	if err := database.RollbackMigrations(db, "../../migrations", 1); err != nil {
		t.Fatal(err)
	}
	if err := database.CheckSchema(ctx, db, want); !errors.As(err, &mm) || !slices.Equal(mm.Missing, want[len(want)-1:]) || len(mm.Unknown) != 0 {
		t.Fatalf("down-level: %v", err)
	}
	if err := database.RunMigrations(db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	// ahead: an image rollback leaves a version this binary does not know
	if err := database.CheckSchema(ctx, db, want[:len(want)-1]); !errors.As(err, &mm) || !slices.Equal(mm.Unknown, want[len(want)-1:]) {
		t.Fatalf("ahead: %v", err)
	}
	// gap (a migration missing in the middle)
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version = $1`, want[3]); err != nil {
		t.Fatal(err)
	}
	if err := database.CheckSchema(ctx, db, want); !errors.As(err, &mm) || !slices.Equal(mm.Missing, []string{want[3]}) {
		t.Fatalf("gap: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO schema_migrations (version) VALUES ($1)`, want[3]); err != nil {
		t.Fatal(err)
	}
}
