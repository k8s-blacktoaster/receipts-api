package database

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	// Database driver
	_ "github.com/lib/pq" // PostgreSQL driver
)

type Config struct {
	URL      string // PostgreSQL connection string
	MaxConns int
	MaxIdle  int
}

type DB struct {
	*sql.DB
}

func NewConnection(cfg Config) (*DB, error) {
	db, err := sql.Open("postgres", cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if cfg.MaxConns > 0 {
		db.SetMaxOpenConns(cfg.MaxConns)
	} else {
		db.SetMaxOpenConns(25) // Default
	}

	if cfg.MaxIdle > 0 {
		db.SetMaxIdleConns(cfg.MaxIdle)
	} else {
		db.SetMaxIdleConns(5) // Default
	}

	db.SetConnMaxLifetime(5 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	slog.Info("database connection established",
		"type", "postgres",
		"max_conns", db.Stats().MaxOpenConnections,
	)

	return &DB{
		DB: db,
	}, nil
}

func (db *DB) Type() string {
	return "postgres"
}

func (db *DB) Close() error {
	slog.Info("closing database connection")
	return db.DB.Close()
}

// HealthCheck performs a simple health check on the database
func (db *DB) HealthCheck() error {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("database health check failed: %w", err)
	}

	// Run a simple query to ensure the database is responsive
	var result int
	query := "SELECT 1"
	if err := db.QueryRowContext(ctx, query).Scan(&result); err != nil {
		return fmt.Errorf("database query check failed: %w", err)
	}

	return nil
}
