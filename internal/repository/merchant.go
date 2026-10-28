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
	// Merge moves everything from merchant `from` into `into` and deletes `from` (NORM-03):
	// receipts; stores (a store matching one of into's by number or address is folded into it);
	// aliases (a text collision keeps into's alias; an item-number collision keeps the moved
	// alias with item_number cleared). Refused (ErrReceiptBusy) while any of from's receipts is
	// uploaded/parsing.
	Merge(ctx context.Context, from, into int64) (*MergeResult, error)
}

// ErrSelfMerge: a merchant cannot be merged into itself.
var ErrSelfMerge = errors.New("cannot merge a merchant into itself")

// MergeResult reports what a merchant merge moved.
type MergeResult struct {
	Into               models.Merchant `json:"into"`
	Receipts           int             `json:"receipts"`
	StoresMoved        int             `json:"stores_moved"`
	StoresFolded       int             `json:"stores_folded"`
	AliasesMoved       int             `json:"aliases_moved"`
	AliasesDropped     int             `json:"aliases_dropped"`      // same text already on `into`
	ItemNumbersCleared int             `json:"item_numbers_cleared"` // kept, item_number collided
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

func (r *merchantRepo) Merge(ctx context.Context, from, into int64) (*MergeResult, error) {
	if from == into {
		return nil, ErrSelfMerge
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM (SELECT id FROM merchants WHERE id IN ($1,$2) ORDER BY id FOR UPDATE) m`, from, into).Scan(&n); err != nil {
		return nil, fmt.Errorf("failed to lock merchants: %w", err)
	}
	if n != 2 {
		return nil, ErrNotFound
	}
	var busy bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM receipts WHERE merchant_id = $1 AND status IN ('uploaded','parsing'))`, from).Scan(&busy); err != nil {
		return nil, err
	}
	if busy {
		return nil, ErrReceiptBusy
	}
	res := &MergeResult{}
	exec := func(q string, args ...any) (int, error) {
		out, err := tx.ExecContext(ctx, q, args...)
		if err != nil {
			return 0, fmt.Errorf("merge: %w", err)
		}
		c, _ := out.RowsAffected()
		return int(c), nil
	}
	// Stores: fold a from-store into the into-store with the same number, else the same address.
	if _, err := exec(`CREATE TEMP TABLE _fold ON COMMIT DROP AS
		SELECT s.id, coalesce(
			(SELECT t.id FROM stores t WHERE t.merchant_id = $2 AND s.store_number IS NOT NULL AND t.store_number = s.store_number),
			(SELECT t.id FROM stores t WHERE t.merchant_id = $2 AND s.address IS NOT NULL AND lower(t.address) = lower(s.address))) AS keep
		FROM stores s WHERE s.merchant_id = $1`, from, into); err != nil {
		return nil, err
	}
	// receipts reference stores by (store_id, merchant_id): detach, move, re-attach.
	if _, err := exec(`CREATE TEMP TABLE _rs ON COMMIT DROP AS
		SELECT r.id, coalesce(f.keep, r.store_id) AS store_id FROM receipts r
		LEFT JOIN _fold f ON f.id = r.store_id WHERE r.merchant_id = $1 AND r.store_id IS NOT NULL`, from); err != nil {
		return nil, err
	}
	if _, err := exec(`UPDATE receipts SET store_id = NULL WHERE id IN (SELECT id FROM _rs)`); err != nil {
		return nil, err
	}
	if res.StoresFolded, err = exec(`DELETE FROM stores s USING _fold f WHERE s.id = f.id AND f.keep IS NOT NULL`); err != nil {
		return nil, err
	}
	if res.StoresMoved, err = exec(`UPDATE stores SET merchant_id = $2 WHERE merchant_id = $1`, from, into); err != nil {
		return nil, err
	}
	// Aliases.
	if res.AliasesDropped, err = exec(`DELETE FROM product_aliases a USING product_aliases k
		WHERE a.merchant_id = $1 AND k.merchant_id = $2 AND lower(k.alias_text) = lower(a.alias_text)`, from, into); err != nil {
		return nil, err
	}
	if res.ItemNumbersCleared, err = exec(`UPDATE product_aliases a SET item_number = NULL
		WHERE a.merchant_id = $1 AND a.item_number IS NOT NULL
		  AND EXISTS (SELECT 1 FROM product_aliases k WHERE k.merchant_id = $2 AND k.item_number = a.item_number)`, from, into); err != nil {
		return nil, err
	}
	if res.AliasesMoved, err = exec(`UPDATE product_aliases SET merchant_id = $2 WHERE merchant_id = $1`, from, into); err != nil {
		return nil, err
	}
	if res.Receipts, err = exec(`UPDATE receipts SET merchant_id = $2, updated_at = now() WHERE merchant_id = $1`, from, into); err != nil {
		return nil, err
	}
	if _, err := exec(`UPDATE receipts r SET store_id = x.store_id FROM _rs x WHERE x.id = r.id`); err != nil {
		return nil, err
	}
	if _, err := exec(`DELETE FROM merchants WHERE id = $1`, from); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit merge: %w", err)
	}
	m, err := r.GetByID(ctx, into)
	if err != nil {
		return nil, err
	}
	res.Into = *m
	return res, nil
}
