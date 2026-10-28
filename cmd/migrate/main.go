// Command migrate applies or reverts SQL migrations.
//
//	go run ./cmd/migrate up
//	go run ./cmd/migrate down [-steps 1]   (-steps 0 = all)
//
// Uses DATABASE_URL and MIGRATIONS_PATH (default ./migrations).
package main

import (
	"flag"
	"fmt"
	"os"

	"receipts-db/internal/database"
)

func main() {
	fs := flag.NewFlagSet("migrate", flag.ExitOnError)
	steps := fs.Int("steps", 1, "number of migrations to revert (down only; 0 = all)")
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: migrate up|down [-steps N]")
		os.Exit(2)
	}
	cmd := os.Args[1]
	_ = fs.Parse(os.Args[2:])

	url := os.Getenv("DATABASE_URL")
	if url == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL is required")
		os.Exit(2)
	}
	path := os.Getenv("MIGRATIONS_PATH")
	if path == "" {
		path = "migrations"
	}
	db, err := database.NewConnection(database.Config{URL: url})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer db.Close()

	switch cmd {
	case "up":
		err = database.RunMigrations(db, path)
	case "down":
		err = database.RollbackMigrations(db, path, *steps)
	default:
		err = fmt.Errorf("unknown command %q", cmd)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
