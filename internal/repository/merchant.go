package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/lib/pq"

	"receipts-db/internal/database"
	"receipts-db/internal/models"
)

// ErrMerchantNameTaken is returned (wrapped in *MerchantConflict) when a rename's normalized
// key belongs to another merchant. v1 refuses instead of merging.
var ErrMerchantNameTaken = errors.New("merchant name already in use")

// ErrInvalidMerchantName: the name normalizes to an empty key.
var ErrInvalidMerchantName = errors.New("merchant name must contain a letter or digit")

// MerchantConflict carries the merchant that already owns the key.
type MerchantConflict struct{ Existing models.Merchant }

func (c *MerchantConflict) Error() string {
	return fmt.Sprintf("%s: %q (id %d)", ErrMerchantNameTaken, c.Existing.Name, c.Existing.ID)
}
func (c *MerchantConflict) Unwrap() error { return ErrMerchantNameTaken }

type MerchantRepository interface {
	List(ctx context.Context) ([]models.Merchant, error)
	GetByID(ctx context.Context, id int64) (*models.Merchant, error)
	// Rename sets name = NormalizeMerchantName(newName). Same key is a no-op; a key owned by
	// another merchant returns *MerchantConflict (errors.Is ErrMerchantNameTaken).
	Rename(ctx context.Context, id int64, newName string) (*models.Merchant, error)
}

type merchantRepo struct{ db *database.DB }

func NewMerchantRepository(db *database.DB) MerchantRepository { return &merchantRepo{db: db} }

const merchantCols = `m.id, m.name, COALESCE(m.category, ''),
	(SELECT count(*) FROM receipts r WHERE r.merchant_id = m.id)`

func (r *merchantRepo) List(ctx context.Context) ([]models.Merchant, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+merchantCols+` FROM merchants m ORDER BY m.name`)
	if err != nil {
		return nil, fmt.Errorf("failed to list merchants: %w", err)
	}
	defer rows.Close()
	var out []models.Merchant
	for rows.Next() {
		var m models.Merchant
		if err := rows.Scan(&m.ID, &m.Name, &m.Category, &m.ReceiptCount); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *merchantRepo) GetByID(ctx context.Context, id int64) (*models.Merchant, error) {
	var m models.Merchant
	err := r.db.QueryRowContext(ctx, `SELECT `+merchantCols+` FROM merchants m WHERE m.id = $1`, id).
		Scan(&m.ID, &m.Name, &m.Category, &m.ReceiptCount)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get merchant: %w", err)
	}
	return &m, nil
}

func (r *merchantRepo) Rename(ctx context.Context, id int64, newName string) (*models.Merchant, error) {
	key := models.NormalizeMerchantName(newName)
	if key == "" {
		return nil, ErrInvalidMerchantName
	}
	cur, err := r.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if cur.Name == key {
		return cur, nil
	}
	_, err = r.db.ExecContext(ctx, `UPDATE merchants SET name = $2, updated_at = now() WHERE id = $1`, id, key)
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr.Code == "23505" {
		var ex models.Merchant
		if e := r.db.QueryRowContext(ctx, `SELECT `+merchantCols+` FROM merchants m WHERE m.name = $1`, key).
			Scan(&ex.ID, &ex.Name, &ex.Category, &ex.ReceiptCount); e != nil {
			return nil, ErrMerchantNameTaken
		}
		return nil, &MerchantConflict{Existing: ex}
	}
	if err != nil {
		return nil, fmt.Errorf("failed to rename merchant: %w", err)
	}
	return r.GetByID(ctx, id)
}
