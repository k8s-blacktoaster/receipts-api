package database

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Migration struct {
	Version string
	Name    string
	Up      string
	Down    string
}

func RunMigrations(db *DB, migrationsPath string) error {
	if err := createMigrationsTable(db); err != nil {
		return fmt.Errorf("failed to create migrations table: %w", err)
	}

	migrations, err := loadMigrations(migrationsPath)
	if err != nil {
		return fmt.Errorf("failed to load migrations: %w", err)
	}

	applied, err := getAppliedMigrations(db)
	if err != nil {
		return fmt.Errorf("failed to get applied migrations: %w", err)
	}

	for _, migration := range migrations {
		if _, ok := applied[migration.Version]; ok {
			continue // Already applied
		}

		slog.Info("applying migration", "version", migration.Version, "name", migration.Name)

		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("failed to begin transaction: %w", err)
		}

		if _, err := tx.Exec(migration.Up); err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to execute migration %s: %w", migration.Version, err)
		}

		if _, err := tx.Exec(
			"INSERT INTO schema_migrations (version) VALUES ($1)",
			migration.Version,
		); err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to record migration %s: %w", migration.Version, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit migration %s: %w", migration.Version, err)
		}

		slog.Info("migration applied successfully", "version", migration.Version)
	}

	return nil
}

func createMigrationsTable(db *DB) error {
	query := `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)
	`
	_, err := db.Exec(query)
	return err
}

func getAppliedMigrations(db *DB) (map[string]bool, error) {
	rows, err := db.Query("SELECT version FROM schema_migrations")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	applied := make(map[string]bool)
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			return nil, err
		}
		applied[version] = true
	}

	return applied, rows.Err()
}

func loadMigrations(path string) ([]Migration, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read migrations directory: %w", err)
	}

	var migrations []Migration
	migrationMap := make(map[string]*Migration)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		filename := entry.Name()
		if !strings.HasSuffix(filename, ".sql") {
			continue
		}

		parts := strings.Split(filename, "_")
		if len(parts) < 2 {
			continue
		}

		version := parts[0]

		isUp := strings.Contains(filename, ".up.sql")
		isDown := strings.Contains(filename, ".down.sql")

		if !isUp && !isDown {
			continue
		}

		content, err := os.ReadFile(filepath.Join(path, filename))
		if err != nil {
			return nil, fmt.Errorf("failed to read migration file %s: %w", filename, err)
		}

		migration, ok := migrationMap[version]
		if !ok {
			name := filename
			name = strings.TrimSuffix(name, ".up.sql")
			name = strings.TrimSuffix(name, ".down.sql")
			migration = &Migration{
				Version: version,
				Name:    name,
			}
			migrationMap[version] = migration
		}

		if isUp {
			migration.Up = string(content)
		} else {
			migration.Down = string(content)
		}
	}

	for _, m := range migrationMap {
		migrations = append(migrations, *m)
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Version < migrations[j].Version
	})

	return migrations, nil
}
