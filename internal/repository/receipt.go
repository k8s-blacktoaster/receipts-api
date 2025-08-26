package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"receipts-db/internal/database"
	"receipts-db/internal/models"
)

type ReceiptRepository interface {
	Create(ctx context.Context, receipt *models.Receipt) error
	GetByID(ctx context.Context, id int) (*models.Receipt, error)
	Update(ctx context.Context, receipt *models.Receipt) error
	Delete(ctx context.Context, id int) error
	List(ctx context.Context, limit, offset int) ([]*models.Receipt, error)
	Count(ctx context.Context) (int, error)
}

type receiptRepo struct {
	db *database.DB
}

func NewReceiptRepository(db *database.DB) ReceiptRepository {
	return &receiptRepo{db: db}
}

func (r *receiptRepo) Create(ctx context.Context, receipt *models.Receipt) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Insert receipt
	receiptQuery := `
		INSERT INTO receipts (
			store_name, store_category, store_address, store_phone, date, subtotal, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8
		) RETURNING id
	`

	now := time.Now()
	receipt.CreatedAt = now
	receipt.UpdatedAt = now

	err = tx.QueryRowContext(ctx, receiptQuery,
		receipt.StoreName,
		receipt.StoreCategory,
		receipt.StoreAddress,
		receipt.StorePhone,
		receipt.Date,
		receipt.Subtotal,
		receipt.CreatedAt,
		receipt.UpdatedAt,
	).Scan(&receipt.ID)

	if err != nil {
		return fmt.Errorf("failed to create receipt: %w", err)
	}

	// Insert receipt items
	if len(receipt.Items) > 0 {
		itemQuery := `
			INSERT INTO receipt_items (
				receipt_id, sku, name, description, quantity, unit_price, created_at, updated_at
			) VALUES (
				$1, $2, $3, $4, $5, $6, $7, $8
			) RETURNING id
		`

		for i := range receipt.Items {
			item := &receipt.Items[i]
			item.ReceiptID = receipt.ID
			item.CreatedAt = now
			item.UpdatedAt = now

			err = tx.QueryRowContext(ctx, itemQuery,
				item.ReceiptID,
				item.SKU,
				item.Name,
				item.Description,
				item.Quantity,
				item.UnitPrice,
				item.CreatedAt,
				item.UpdatedAt,
			).Scan(&item.ID)

			if err != nil {
				return fmt.Errorf("failed to create receipt item: %w", err)
			}
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

func (r *receiptRepo) GetByID(ctx context.Context, id int) (*models.Receipt, error) {
	// Get receipt
	receiptQuery := `
		SELECT 
			id, store_name, store_category, store_address, store_phone, date, subtotal, created_at, updated_at
		FROM receipts
		WHERE id = $1
	`

	receipt := &models.Receipt{}
	err := r.db.QueryRowContext(ctx, receiptQuery, id).Scan(
		&receipt.ID,
		&receipt.StoreName,
		&receipt.StoreCategory,
		&receipt.StoreAddress,
		&receipt.StorePhone,
		&receipt.Date,
		&receipt.Subtotal,
		&receipt.CreatedAt,
		&receipt.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("receipt not found")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get receipt: %w", err)
	}

	// Get receipt items
	itemsQuery := `
		SELECT 
			id, receipt_id, sku, name, description, quantity, unit_price, created_at, updated_at
		FROM receipt_items
		WHERE receipt_id = $1
		ORDER BY id
	`

	rows, err := r.db.QueryContext(ctx, itemsQuery, id)
	if err != nil {
		return nil, fmt.Errorf("failed to get receipt items: %w", err)
	}
	defer rows.Close()

	var items []models.ReceiptItem
	for rows.Next() {
		item := models.ReceiptItem{}
		err := rows.Scan(
			&item.ID,
			&item.ReceiptID,
			&item.SKU,
			&item.Name,
			&item.Description,
			&item.Quantity,
			&item.UnitPrice,
			&item.CreatedAt,
			&item.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan receipt item: %w", err)
		}
		items = append(items, item)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating receipt items: %w", err)
	}

	receipt.Items = items
	return receipt, nil
}

func (r *receiptRepo) Update(ctx context.Context, receipt *models.Receipt) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Update receipt
	receiptQuery := `
		UPDATE receipts SET
			store_name = $2,
			store_category = $3,
			store_address = $4,
			store_phone = $5,
			date = $6,
			subtotal = $7,
			updated_at = $8
		WHERE id = $1
	`

	receipt.UpdatedAt = time.Now()

	result, err := tx.ExecContext(ctx, receiptQuery,
		receipt.ID,
		receipt.StoreName,
		receipt.StoreCategory,
		receipt.StoreAddress,
		receipt.StorePhone,
		receipt.Date,
		receipt.Subtotal,
		receipt.UpdatedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to update receipt: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("receipt not found")
	}

	// Delete existing items and recreate (simple approach)
	if _, err := tx.ExecContext(ctx, "DELETE FROM receipt_items WHERE receipt_id = $1", receipt.ID); err != nil {
		return fmt.Errorf("failed to delete existing items: %w", err)
	}

	// Insert updated items
	if len(receipt.Items) > 0 {
		itemQuery := `
			INSERT INTO receipt_items (
				receipt_id, sku, name, description, quantity, unit_price, created_at, updated_at
			) VALUES (
				$1, $2, $3, $4, $5, $6, $7, $8
			) RETURNING id
		`

		now := time.Now()
		for i := range receipt.Items {
			item := &receipt.Items[i]
			item.ReceiptID = receipt.ID
			item.CreatedAt = now
			item.UpdatedAt = now

			err = tx.QueryRowContext(ctx, itemQuery,
				item.ReceiptID,
				item.SKU,
				item.Name,
				item.Description,
				item.Quantity,
				item.UnitPrice,
				item.CreatedAt,
				item.UpdatedAt,
			).Scan(&item.ID)

			if err != nil {
				return fmt.Errorf("failed to create receipt item: %w", err)
			}
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

func (r *receiptRepo) Delete(ctx context.Context, id int) error {
	// Items will be deleted automatically due to CASCADE foreign key
	query := `DELETE FROM receipts WHERE id = $1`

	result, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete receipt: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("receipt not found")
	}

	return nil
}

func (r *receiptRepo) List(ctx context.Context, limit, offset int) ([]*models.Receipt, error) {
	query := `
		SELECT 
			id, store_name, store_category, store_address, store_phone, date, subtotal, created_at, updated_at
		FROM receipts
		ORDER BY date DESC, created_at DESC
		LIMIT $1 OFFSET $2
	`

	rows, err := r.db.QueryContext(ctx, query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list receipts: %w", err)
	}
	defer rows.Close()

	var receipts []*models.Receipt
	for rows.Next() {
		receipt := &models.Receipt{}
		err := rows.Scan(
			&receipt.ID,
			&receipt.StoreName,
			&receipt.StoreCategory,
			&receipt.StoreAddress,
			&receipt.StorePhone,
			&receipt.Date,
			&receipt.Subtotal,
			&receipt.CreatedAt,
			&receipt.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan receipt: %w", err)
		}
		receipts = append(receipts, receipt)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return receipts, nil
}

func (r *receiptRepo) Count(ctx context.Context) (int, error) {
	var count int
	query := `SELECT COUNT(*) FROM receipts`

	err := r.db.QueryRowContext(ctx, query).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count receipts: %w", err)
	}

	return count, nil
}