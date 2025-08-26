package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"receipts-db/internal/database"
	"receipts-db/internal/models"
)

type ProductRepository interface {
	Create(ctx context.Context, product *models.Product) error

	GetByID(ctx context.Context, id int) (*models.Product, error)

	GetBySKU(ctx context.Context, sku string) (*models.Product, error)

	Update(ctx context.Context, product *models.Product) error

	Delete(ctx context.Context, id int) error

	List(ctx context.Context, limit, offset int) ([]*models.Product, error)

	Count(ctx context.Context) (int, error)
}

type productRepo struct {
	db *database.DB
}

func NewProductRepository(db *database.DB) ProductRepository {
	return &productRepo{db: db}
}


func (r *productRepo) Create(ctx context.Context, product *models.Product) error {
	query := `
		INSERT INTO products (
			sku, name, description, quantity, unit_price, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7
		) RETURNING id
	`

	now := time.Now()
	product.CreatedAt = now
	product.UpdatedAt = now

	err := r.db.QueryRowContext(ctx, query,
		product.SKU,
		product.Name,
		product.Description,
		product.Quantity,
		product.UnitPrice,
		product.CreatedAt,
		product.UpdatedAt,
	).Scan(&product.ID)

	if err != nil {
		return fmt.Errorf("failed to create product: %w", err)
	}

	return nil
}

func (r *productRepo) GetByID(ctx context.Context, id int) (*models.Product, error) {
	query := `
		SELECT 
			id, sku, name, description, quantity, unit_price, created_at, updated_at
		FROM products
		WHERE id = $1
	`

	product := &models.Product{}
	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&product.ID,
		&product.SKU,
		&product.Name,
		&product.Description,
		&product.Quantity,
		&product.UnitPrice,
		&product.CreatedAt,
		&product.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("product not found")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get product: %w", err)
	}

	return product, nil
}

func (r *productRepo) GetBySKU(ctx context.Context, sku string) (*models.Product, error) {
	query := `
		SELECT 
			id, sku, name, description, quantity, unit_price, created_at, updated_at
		FROM products
		WHERE sku = $1
	`

	product := &models.Product{}
	err := r.db.QueryRowContext(ctx, query, sku).Scan(
		&product.ID,
		&product.SKU,
		&product.Name,
		&product.Description,
		&product.Quantity,
		&product.UnitPrice,
		&product.CreatedAt,
		&product.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("product not found")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get product: %w", err)
	}

	return product, nil
}

func (r *productRepo) Update(ctx context.Context, product *models.Product) error {
	query := `
		UPDATE products SET
			sku = $2,
			name = $3,
			description = $4,
			quantity = $5,
			unit_price = $6,
			updated_at = $7
		WHERE id = $1
	`

	product.UpdatedAt = time.Now()

	result, err := r.db.ExecContext(ctx, query,
		product.ID,
		product.SKU,
		product.Name,
		product.Description,
		product.Quantity,
		product.UnitPrice,
		product.UpdatedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to update product: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("product not found")
	}

	return nil
}

func (r *productRepo) Delete(ctx context.Context, id int) error {
	query := `DELETE FROM products WHERE id = $1`

	result, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete product: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("product not found")
	}

	return nil
}

func (r *productRepo) List(ctx context.Context, limit, offset int) ([]*models.Product, error) {
	query := `
		SELECT 
			id, sku, name, description, quantity, unit_price, created_at, updated_at
		FROM products
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2
	`

	rows, err := r.db.QueryContext(ctx, query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list products: %w", err)
	}
	defer rows.Close()

	var products []*models.Product
	for rows.Next() {
		product := &models.Product{}
		err := rows.Scan(
			&product.ID,
			&product.SKU,
			&product.Name,
			&product.Description,
			&product.Quantity,
			&product.UnitPrice,
			&product.CreatedAt,
			&product.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan product: %w", err)
		}
		products = append(products, product)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return products, nil
}

func (r *productRepo) Count(ctx context.Context) (int, error) {
	var count int
	query := `SELECT COUNT(*) FROM products`

	err := r.db.QueryRowContext(ctx, query).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count products: %w", err)
	}

	return count, nil
}
