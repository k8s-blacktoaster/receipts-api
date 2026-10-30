package database

import (
	"context"
	"fmt"
	"slices"
	"sort"
)

// MigrationVersions lists the migration versions found in path (the set this binary expects).
func MigrationVersions(path string) ([]string, error) {
	ms, err := loadMigrations(path)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Version
	}
	return out, nil
}

// SchemaMismatchError: the applied migrations differ from what the binary expects.
type SchemaMismatchError struct {
	Missing []string // expected but not applied (DB down-level, or a gap)
	Unknown []string // applied but unknown to this binary (DB ahead, e.g. after an image rollback)
}

func (e *SchemaMismatchError) Error() string {
	return fmt.Sprintf("schema mismatch: missing migrations %v, unknown applied migrations %v", e.Missing, e.Unknown)
}

// CheckSchema compares schema_migrations with expected. Any difference (down-level, ahead or a
// gap, i.e. a DB not in a state this binary was built for) is a *SchemaMismatchError.
func CheckSchema(ctx context.Context, db *DB, expected []string) error {
	rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("read schema_migrations: %w", err)
	}
	defer rows.Close()
	applied := map[string]bool{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return err
		}
		applied[v] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	e := &SchemaMismatchError{}
	for _, v := range expected {
		if !applied[v] {
			e.Missing = append(e.Missing, v)
		}
	}
	for v := range applied {
		if !slices.Contains(expected, v) {
			e.Unknown = append(e.Unknown, v)
		}
	}
	if len(e.Missing) == 0 && len(e.Unknown) == 0 {
		return nil
	}
	sort.Strings(e.Unknown)
	return e
}
