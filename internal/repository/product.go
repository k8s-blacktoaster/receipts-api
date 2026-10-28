package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

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
	GetProduct(ctx context.Context, id int64) (*models.Product, error)
	// SearchProducts finds products by canonical name or any alias text (substring or trigram).
	SearchProducts(ctx context.Context, q string, limit int) ([]models.Product, error)
	// ListReviewItems is the NORM-02 queue: unmapped lines on receipts that are not
	// uploaded/parsing, newest receipt first. Returns the page and the total count.
	ListReviewItems(ctx context.Context, limit, offset int) ([]models.ReviewItem, int, error)
	// MapItem maps a line to a product, learns a merchant-scoped alias (text + item number)
	// and applies it to the merchant's other unmapped lines. ErrReceiptBusy while the receipt
	// is uploaded/parsing; ErrNotFound / ErrProductNotFound for unknown ids. With newProduct set
	// (productID 0) the product is created in the same transaction, after the item checks, so a
	// refused map leaves no orphan product.
	MapItem(ctx context.Context, itemID, productID int64, newProduct *models.Product) (*models.MapResult, error)
}

// ErrProductNotFound is returned when a product id does not exist.
var ErrProductNotFound = errors.New("product not found")

type productRepo struct{ db *database.DB }

func NewProductRepository(db *database.DB) ProductRepository { return &productRepo{db: db} }

func (r *productRepo) GetProduct(ctx context.Context, id int64) (*models.Product, error) {
	var p models.Product
	var brand, cat, upc sql.NullString
	err := r.db.QueryRowContext(ctx, `SELECT id, canonical_name, brand, category, upc, created_at FROM products WHERE id = $1`, id).
		Scan(&p.ID, &p.CanonicalName, &brand, &cat, &upc, &p.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrProductNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get product: %w", err)
	}
	p.Brand, p.Category, p.UPC = brand.String, cat.String, upc.String
	return &p, nil
}

func (r *productRepo) SearchProducts(ctx context.Context, q string, limit int) ([]models.Product, error) {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	q = strings.TrimSpace(q)
	rows, err := r.db.QueryContext(ctx, `
		SELECT p.id, p.canonical_name, p.brand, p.category, p.upc, p.created_at
		FROM products p
		LEFT JOIN LATERAL (
			SELECT max(similarity(lower(a.alias_text), lower($1))) AS s,
			       bool_or(strpos(lower(a.alias_text), lower($1)) > 0) AS sub
			FROM product_aliases a WHERE a.product_id = p.id) al ON true
		WHERE $1 = '' OR strpos(lower(p.canonical_name), lower($1)) > 0 OR lower(p.canonical_name) % lower($1)
		   OR al.sub OR al.s >= 0.3 OR p.upc = $1
		ORDER BY greatest(similarity(lower(p.canonical_name), lower($1)), coalesce(al.s, 0)) DESC, p.canonical_name, p.id
		LIMIT $2`, q, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to search products: %w", err)
	}
	defer rows.Close()
	out := []models.Product{}
	for rows.Next() {
		var p models.Product
		var brand, cat, upc sql.NullString
		if err := rows.Scan(&p.ID, &p.CanonicalName, &brand, &cat, &upc, &p.CreatedAt); err != nil {
			return nil, err
		}
		p.Brand, p.Category, p.UPC = brand.String, cat.String, upc.String
		out = append(out, p)
	}
	return out, rows.Err()
}

const reviewWhere = `ri.product_id IS NULL AND r.status NOT IN ('uploaded','parsing')`

func (r *productRepo) ListReviewItems(ctx context.Context, limit, offset int) ([]models.ReviewItem, int, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT count(*) FROM receipt_items ri JOIN receipts r ON r.id = ri.receipt_id WHERE `+reviewWhere).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("failed to count review items: %w", err)
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT ri.id, r.id, coalesce(to_char(r.date, 'YYYY-MM-DD'), ''), r.status, r.merchant_id, coalesce(m.name, ''),
		       ri.line_no, coalesce(ri.raw_text, ''), ri.name, coalesce(ri.item_number, ''), coalesce(ri.upc, ''),
		       ri.line_total_cents, ri.suggested_product_id, coalesce(p.canonical_name, ''), ri.match_confidence::float8
		FROM receipt_items ri
		JOIN receipts r ON r.id = ri.receipt_id
		LEFT JOIN merchants m ON m.id = r.merchant_id
		LEFT JOIN products p ON p.id = ri.suggested_product_id
		WHERE `+reviewWhere+`
		ORDER BY r.date DESC NULLS LAST, r.id DESC, ri.line_no NULLS LAST, ri.id
		LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list review items: %w", err)
	}
	defer rows.Close()
	out := []models.ReviewItem{}
	for rows.Next() {
		var it models.ReviewItem
		if err := rows.Scan(&it.ItemID, &it.ReceiptID, &it.ReceiptDate, &it.ReceiptStatus, &it.MerchantID, &it.MerchantName,
			&it.LineNo, &it.RawText, &it.Name, &it.ItemNumber, &it.UPC, &it.LineTotalCents,
			&it.SuggestedProductID, &it.SuggestedProductName, &it.MatchConfidence); err != nil {
			return nil, 0, err
		}
		if it.SuggestedProductID == nil {
			it.MatchConfidence = nil
		}
		out = append(out, it)
	}
	return out, total, rows.Err()
}

func (r *productRepo) MapItem(ctx context.Context, itemID, productID int64, newProduct *models.Product) (*models.MapResult, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()
	var status string
	var merchantID sql.NullInt64
	var it models.ReceiptItem
	var raw, itemNo, upc sql.NullString
	err = tx.QueryRowContext(ctx, `
		SELECT r.status, r.merchant_id, coalesce(i.raw_text, ''), i.name, coalesce(i.item_number, ''), i.upc
		FROM receipt_items i JOIN receipts r ON r.id = i.receipt_id
		WHERE i.id = $1 FOR UPDATE OF r, i`, itemID).Scan(&status, &merchantID, &raw, &it.Name, &itemNo, &upc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to load item: %w", err)
	}
	if status == models.StatusUploaded || status == models.StatusParsing {
		return nil, ErrReceiptBusy
	}
	it.RawText, it.ItemNumber = raw.String, strings.TrimSpace(itemNo.String)
	if models.ValidUPC(strings.TrimSpace(upc.String)) {
		it.UPC = strings.TrimSpace(upc.String)
	}
	if newProduct != nil {
		if err := insertProduct(ctx, tx, newProduct); err != nil {
			return nil, err
		}
		productID = newProduct.ID
	} else {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM products WHERE id = $1)`, productID).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			return nil, ErrProductNotFound
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE receipt_items SET product_id = $2, match_source = 'manual', match_confidence = 1,
		       suggested_product_id = NULL, updated_at = now()
		WHERE id = $1`, itemID, productID); err != nil {
		return nil, fmt.Errorf("failed to map item: %w", err)
	}
	res := &models.MapResult{ItemID: itemID, ProductID: productID}
	text := aliasText(&it)
	if merchantID.Valid && text != "" {
		if res.AliasID, err = learnAlias(ctx, tx, merchantID.Int64, text, it.ItemNumber, productID); err != nil {
			return nil, err
		}
		if err := learnUPC(ctx, tx, merchantID.Int64, res.AliasID, it.UPC, productID); err != nil {
			return nil, err
		}
		// Reuse it right away on the merchant's other unmapped lines.
		n, err := tx.ExecContext(ctx, `
			UPDATE receipt_items ri SET product_id = $2,
			       match_source = CASE WHEN $3 <> '' AND ri.item_number = $3 THEN 'item_number'
			                           WHEN $5 <> '' AND ri.upc = $5 THEN 'upc' ELSE 'exact' END,
			       match_confidence = 1, suggested_product_id = NULL, updated_at = now()
			FROM receipts r
			WHERE r.id = ri.receipt_id AND r.merchant_id = $1 AND ri.product_id IS NULL
			  AND r.status NOT IN ('uploaded','parsing')
			  AND (($3 <> '' AND ri.item_number = $3) OR ($5 <> '' AND ri.upc = $5)
			       OR (merchant_name_key($4) <> '' AND
			           merchant_name_key(coalesce(nullif(btrim(ri.raw_text), ''), btrim(ri.name))) = merchant_name_key($4)))`,
			merchantID.Int64, productID, it.ItemNumber, text, it.UPC)
		if err != nil {
			return nil, fmt.Errorf("failed to apply alias: %w", err)
		}
		c, _ := n.RowsAffected()
		res.AlsoMapped = int(c)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit: %w", err)
	}
	return res, nil
}

// learnUPC records a line's UPC for the merchant: an alias that already owns the UPC is
// re-pointed to the product; otherwise the UPC goes on the learned alias if it has none.
func learnUPC(ctx context.Context, q txExec, merchantID, aliasID int64, upc string, productID int64) error {
	if upc == "" {
		return nil
	}
	res, err := q.ExecContext(ctx, `UPDATE product_aliases SET product_id = $3, source = 'manual', confidence = 1
		WHERE merchant_id = $1 AND upc = $2`, merchantID, upc, productID)
	if err != nil {
		return fmt.Errorf("learn upc: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	if _, err := q.ExecContext(ctx, `UPDATE product_aliases SET upc = $2 WHERE id = $1 AND upc IS NULL`, aliasID, upc); err != nil {
		return fmt.Errorf("learn upc: %w", err)
	}
	return nil
}

// learnAlias records (merchant, text[, item number]) -> product as a manual alias, respecting
// both unique keys: the text alias and the item-number alias may be different rows.
func learnAlias(ctx context.Context, q txExec, merchantID int64, text, itemNo string, productID int64) (int64, error) {
	var byText, byNum int64
	err := q.QueryRowContext(ctx, `SELECT id FROM product_aliases WHERE merchant_id = $1
		AND merchant_name_key(alias_text) = merchant_name_key($2) ORDER BY id LIMIT 1`, merchantID, text).Scan(&byText)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	if itemNo != "" {
		err = q.QueryRowContext(ctx, `SELECT id FROM product_aliases WHERE merchant_id = $1 AND item_number = $2`, merchantID, itemNo).Scan(&byNum)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
	}
	upd := `UPDATE product_aliases SET product_id = $2, source = 'manual', confidence = 1 WHERE id = $1`
	switch {
	case byText != 0:
		if _, err := q.ExecContext(ctx, upd, byText, productID); err != nil {
			return 0, err
		}
		if byNum == 0 && itemNo != "" {
			if _, err := q.ExecContext(ctx, `UPDATE product_aliases SET item_number = $2 WHERE id = $1`, byText, itemNo); err != nil {
				return 0, err
			}
		} else if byNum != 0 && byNum != byText {
			if _, err := q.ExecContext(ctx, upd, byNum, productID); err != nil {
				return 0, err
			}
		}
		return byText, nil
	case byNum != 0:
		if _, err := q.ExecContext(ctx, upd, byNum, productID); err != nil {
			return 0, err
		}
		// also remember this printed text (no item number: that key is taken by byNum)
		var id int64
		err := q.QueryRowContext(ctx, `INSERT INTO product_aliases (product_id, merchant_id, alias_text, source, confidence)
			VALUES ($1,$2,$3,'manual',1) RETURNING id`, productID, merchantID, text).Scan(&id)
		return id, err
	default:
		var id int64
		err := q.QueryRowContext(ctx, `INSERT INTO product_aliases (product_id, merchant_id, alias_text, item_number, source, confidence)
			VALUES ($1,$2,$3,$4,'manual',1) RETURNING id`, productID, merchantID, text, nullStr(itemNo)).Scan(&id)
		return id, err
	}
}

func (r *productRepo) CreateProduct(ctx context.Context, p *models.Product) error {
	return insertProduct(ctx, r.db, p)
}

func insertProduct(ctx context.Context, q txExec, p *models.Product) error {
	return q.QueryRowContext(ctx, `
		INSERT INTO products (canonical_name, brand, category, upc) VALUES ($1,$2,$3,$4)
		RETURNING id, created_at`,
		p.CanonicalName, nullStr(p.Brand), nullStr(p.Category), nullStr(p.UPC)).Scan(&p.ID, &p.CreatedAt)
}

func (r *productRepo) UpsertAlias(ctx context.Context, a *models.ProductAlias) error {
	if a.Source == "" {
		a.Source = "manual"
	}
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO product_aliases (product_id, merchant_id, alias_text, item_number, source, confidence, upc)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (merchant_id, (lower(alias_text))) DO UPDATE SET
			product_id = EXCLUDED.product_id,
			item_number = COALESCE(EXCLUDED.item_number, product_aliases.item_number),
			upc = COALESCE(EXCLUDED.upc, product_aliases.upc),
			source = EXCLUDED.source, confidence = EXCLUDED.confidence
		RETURNING id`,
		a.ProductID, a.MerchantID, a.AliasText, nullStr(a.ItemNumber), a.Source, a.Confidence, nullStr(a.UPC)).Scan(&a.ID)
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
