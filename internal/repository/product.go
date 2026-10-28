package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"receipts-db/internal/database"
	"receipts-db/internal/models"
)

// ProductRepository manages canonical products and merchant-scoped aliases.
type ProductRepository interface {
	CreateProduct(ctx context.Context, p *models.Product) error
	// UpsertAlias records how a merchant prints a product. Unique per (merchant, alias text).
	UpsertAlias(ctx context.Context, a *models.ProductAlias) error
	// ResolveAlias maps a merchant's item number (preferred) or printed text to a product.
	ResolveAlias(ctx context.Context, merchantID int64, itemNumber, aliasText string) (int64, error)
	// WhereBought answers "where did I buy X": merchant + store location + date, newest first.
	WhereBought(ctx context.Context, productID int64, limit int) ([]models.PurchaseLocation, error)
}

type productRepo struct{ db *database.DB }

func NewProductRepository(db *database.DB) ProductRepository { return &productRepo{db: db} }

func (r *productRepo) CreateProduct(ctx context.Context, p *models.Product) error {
	return r.db.QueryRowContext(ctx, `
		INSERT INTO products (canonical_name, brand, category, upc) VALUES ($1,$2,$3,$4)
		RETURNING id, created_at`,
		p.CanonicalName, nullStr(p.Brand), nullStr(p.Category), nullStr(p.UPC)).Scan(&p.ID, &p.CreatedAt)
}

func (r *productRepo) UpsertAlias(ctx context.Context, a *models.ProductAlias) error {
	if a.Source == "" {
		a.Source = "manual"
	}
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO product_aliases (product_id, merchant_id, alias_text, item_number, source, confidence)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (merchant_id, (lower(alias_text))) DO UPDATE SET
			product_id = EXCLUDED.product_id,
			item_number = COALESCE(EXCLUDED.item_number, product_aliases.item_number),
			source = EXCLUDED.source, confidence = EXCLUDED.confidence
		RETURNING id`,
		a.ProductID, a.MerchantID, a.AliasText, nullStr(a.ItemNumber), a.Source, a.Confidence).Scan(&a.ID)
	if err != nil {
		return fmt.Errorf("failed to upsert alias: %w", err)
	}
	return nil
}

func (r *productRepo) ResolveAlias(ctx context.Context, merchantID int64, itemNumber, aliasText string) (int64, error) {
	var id int64
	err := r.db.QueryRowContext(ctx, `
		SELECT product_id FROM product_aliases
		WHERE merchant_id = $1
		  AND (($2 <> '' AND item_number = $2) OR lower(alias_text) = lower($3))
		ORDER BY (item_number = $2) DESC NULLS LAST
		LIMIT 1`, merchantID, itemNumber, aliasText).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return id, err
}

func (r *productRepo) WhereBought(ctx context.Context, productID int64, limit int) ([]models.PurchaseLocation, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT product_id, canonical_name, merchant_id, merchant_name, store_id, store_number,
		       store_address, receipt_id, to_char(purchase_date, 'YYYY-MM-DD'), quantity::float8,
		       unit_price_cents, net_total_cents
		FROM product_purchase_locations_view
		WHERE product_id = $1
		ORDER BY purchase_date DESC, receipt_id DESC
		LIMIT $2`, productID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query purchase locations: %w", err)
	}
	defer rows.Close()
	var out []models.PurchaseLocation
	for rows.Next() {
		var p models.PurchaseLocation
		var num, addr sql.NullString
		if err := rows.Scan(&p.ProductID, &p.ProductName, &p.MerchantID, &p.MerchantName, &p.StoreID, &num,
			&addr, &p.ReceiptID, &p.PurchaseDate, &p.Quantity, &p.UnitPriceCents, &p.NetTotalCents); err != nil {
			return nil, err
		}
		p.StoreNumber, p.StoreAddress = num.String, addr.String
		out = append(out, p)
	}
	return out, rows.Err()
}
