package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/lib/pq"

	"receipts-db/internal/database"
	"receipts-db/internal/models"
)

// QueryRepository is the read-only query surface used by the MCP server (phase 5).
// Every method runs in a READ ONLY transaction and only counts receipts whose status is
// parsed, needs_review or reviewed (CountedStatuses).
type QueryRepository interface {
	// ResolveMerchant normalizes name with models.NormalizeMerchantName (MCP-04) and returns the
	// exact match (or merge redirect). With no exact match it returns nil plus up to `limit`
	// pg_trgm candidates ranked by similarity (MCP-06). ErrInvalidMerchantName for an empty key.
	ResolveMerchant(ctx context.Context, name string, limit int) (*models.MerchantSummary, []models.MerchantCandidate, error)
	ListMerchants(ctx context.Context) ([]models.MerchantSummary, error)
	// SearchPurchases finds line items whose name, raw text or mapped product name contains q,
	// newest first. truncated reports that more than f.Limit rows matched.
	SearchPurchases(ctx context.Context, f PurchaseFilter) (lines []models.PurchaseLine, truncated bool, err error)
	// PriceHistory returns the newest `limit` purchases of a product (returns excluded), sorted
	// oldest first for display. truncated reports that older purchases were left out.
	PriceHistory(ctx context.Context, productID, merchantID int64, limit int) (lines []models.PurchaseLine, truncated bool, err error)
	// WhereBought aggregates every counted purchase of a product per merchant + store in SQL
	// (newest location first) and returns the newest `limit` purchases.
	WhereBought(ctx context.Context, productID int64, limit int) (*models.WhereBought, error)
	// Spend totals receipts.total_cents grouped by "merchant" or "month".
	Spend(ctx context.Context, f SpendFilter) ([]models.SpendGroup, error)
	// LastPurchases returns the newest counted purchase of each product id.
	LastPurchases(ctx context.Context, productIDs []int64) (map[int64]models.PurchaseLine, error)
}

// CountedStatuses are the receipt statuses whose data the query tools report.
var CountedStatuses = []string{models.StatusParsed, models.StatusNeedsReview, models.StatusReviewed}

type PurchaseFilter struct {
	Query      string
	MerchantID int64  // 0 = any
	From, To   string // YYYY-MM-DD, inclusive; "" = open
	Limit      int
}

type SpendFilter struct {
	MerchantID int64
	From, To   string
	GroupBy    string // "merchant" | "month"
}

type queryRepo struct{ db *database.DB }

func NewQueryRepository(db *database.DB) QueryRepository { return &queryRepo{db: db} }

func (r *queryRepo) readOnly(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return fmt.Errorf("begin read-only tx: %w", err)
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

const merchantSummarySQL = `
	SELECT m.id, m.name, count(r.id), COALESCE(sum(r.total_cents), 0),
	       COALESCE(to_char(max(r.date), 'YYYY-MM-DD'), ''),
	       COALESCE((array_agg(r.id ORDER BY r.date DESC NULLS LAST, r.id DESC))[1], 0)
	FROM merchants m
	LEFT JOIN receipts r ON r.merchant_id = m.id AND r.status = ANY($1)`

func scanMerchantSummary(s rowScanner) (models.MerchantSummary, error) {
	var m models.MerchantSummary
	err := s.Scan(&m.ID, &m.Name, &m.ReceiptCount, &m.TotalSpentCents, &m.LastVisit, &m.LastReceiptID)
	return m, err
}

func (r *queryRepo) ListMerchants(ctx context.Context) ([]models.MerchantSummary, error) {
	out := []models.MerchantSummary{}
	err := r.readOnly(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, merchantSummarySQL+` GROUP BY m.id ORDER BY m.name`, pq.Array(CountedStatuses))
		if err != nil {
			return fmt.Errorf("list merchants: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			m, err := scanMerchantSummary(rows)
			if err != nil {
				return err
			}
			out = append(out, m)
		}
		return rows.Err()
	})
	return out, err
}

func (r *queryRepo) ResolveMerchant(ctx context.Context, name string, limit int) (*models.MerchantSummary, []models.MerchantCandidate, error) {
	key := models.NormalizeMerchantName(name)
	if key == "" {
		return nil, nil, ErrInvalidMerchantName
	}
	if limit <= 0 || limit > 20 {
		limit = 5
	}
	var found *models.MerchantSummary
	cands := []models.MerchantCandidate{}
	err := r.readOnly(ctx, func(tx *sql.Tx) error {
		m, err := scanMerchantSummary(tx.QueryRowContext(ctx, merchantSummarySQL+`
			WHERE m.id = COALESCE((SELECT id FROM merchants WHERE name = $2),
			                      (SELECT merchant_id FROM merchant_name_redirects WHERE name_key = $2))
			GROUP BY m.id`, pq.Array(CountedStatuses), key))
		if err == nil {
			found = &m
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("resolve merchant: %w", err)
		}
		rows, err := tx.QueryContext(ctx, `
			SELECT id, name, similarity(name, $1)::float8 AS s FROM merchants
			WHERE name % $1 OR strpos(name, $1) > 0
			ORDER BY s DESC, name LIMIT $2`, key, limit)
		if err != nil {
			return fmt.Errorf("merchant candidates: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var c models.MerchantCandidate
			if err := rows.Scan(&c.ID, &c.Name, &c.Similarity); err != nil {
				return err
			}
			cands = append(cands, c)
		}
		return rows.Err()
	})
	return found, cands, err
}

func clampLimit(n int) int {
	if n <= 0 {
		return 50
	}
	if n > 200 {
		return 200
	}
	return n
}

const purchaseLineSQL = `
	SELECT ri.id, r.id, to_char(r.date, 'YYYY-MM-DD'), r.status, m.id, m.name,
	       r.store_id, COALESCE(s.store_number, ''), COALESCE(s.address, ''),
	       COALESCE(ri.name, ''), COALESCE(ri.raw_text, ''), ri.product_id, COALESCE(p.canonical_name, ''),
	       ri.quantity::float8, ri.unit_price_cents, ri.net_total_cents, ri.is_return
	FROM receipt_items ri
	JOIN receipts r ON r.id = ri.receipt_id
	JOIN merchants m ON m.id = r.merchant_id
	LEFT JOIN stores s ON s.id = r.store_id
	LEFT JOIN products p ON p.id = ri.product_id`

func scanLines(rows *sql.Rows) ([]models.PurchaseLine, error) {
	defer rows.Close()
	out := []models.PurchaseLine{}
	for rows.Next() {
		var l models.PurchaseLine
		var date sql.NullString
		var pid sql.NullInt64
		if err := rows.Scan(&l.ItemID, &l.ReceiptID, &date, &l.ReceiptStatus, &l.MerchantID, &l.MerchantName,
			&l.StoreID, &l.StoreNumber, &l.StoreAddress, &l.ItemName, &l.RawText, &pid, &l.ProductName,
			&l.Quantity, &l.UnitPriceCents, &l.NetTotalCents, &l.IsReturn); err != nil {
			return nil, err
		}
		l.PurchaseDate = date.String
		if pid.Valid {
			l.ProductID = &pid.Int64
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func nullDate(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// trim cuts a limit+1 fetch to limit rows and reports whether there were more.
func trim(lines []models.PurchaseLine, limit int) ([]models.PurchaseLine, bool) {
	if len(lines) > limit {
		return lines[:limit], true
	}
	return lines, false
}

func (r *queryRepo) SearchPurchases(ctx context.Context, f PurchaseFilter) ([]models.PurchaseLine, bool, error) {
	q := strings.ToLower(strings.TrimSpace(f.Query))
	limit := clampLimit(f.Limit)
	var out []models.PurchaseLine
	err := r.readOnly(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, purchaseLineSQL+`
			WHERE r.status = ANY($1)
			  AND ($2 = '' OR strpos(lower(COALESCE(ri.name, '')), $2) > 0
			       OR strpos(lower(COALESCE(ri.raw_text, '')), $2) > 0
			       OR strpos(lower(COALESCE(p.canonical_name, '')), $2) > 0)
			  AND ($3::bigint = 0 OR r.merchant_id = $3)
			  AND ($4::date IS NULL OR r.date >= $4::date)
			  AND ($5::date IS NULL OR r.date <= $5::date)
			ORDER BY r.date DESC NULLS LAST, r.id DESC, ri.id
			LIMIT $6`, pq.Array(CountedStatuses), q, f.MerchantID, nullDate(f.From), nullDate(f.To), limit+1)
		if err != nil {
			return fmt.Errorf("search purchases: %w", err)
		}
		out, err = scanLines(rows)
		return err
	})
	out, truncated := trim(out, limit)
	return out, truncated, err
}

func (r *queryRepo) PriceHistory(ctx context.Context, productID, merchantID int64, limit int) ([]models.PurchaseLine, bool, error) {
	limit = clampLimit(limit)
	var out []models.PurchaseLine
	err := r.readOnly(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, purchaseLineSQL+`
			WHERE r.status = ANY($1) AND ri.product_id = $2 AND NOT ri.is_return
			  AND ($3::bigint = 0 OR r.merchant_id = $3)
			ORDER BY r.date DESC NULLS LAST, r.id DESC, ri.id DESC
			LIMIT $4`, pq.Array(CountedStatuses), productID, merchantID, limit+1)
		if err != nil {
			return fmt.Errorf("price history: %w", err)
		}
		out, err = scanLines(rows)
		return err
	})
	if err != nil {
		return nil, false, err
	}
	out, truncated := trim(out, limit)
	slices.Reverse(out) // picked newest, shown oldest first
	return out, truncated, nil
}

// LocationReceiptIDs caps the receipt ids listed per where_bought location.
const LocationReceiptIDs = 50

func (r *queryRepo) WhereBought(ctx context.Context, productID int64, limit int) (*models.WhereBought, error) {
	limit = clampLimit(limit)
	wb := &models.WhereBought{Locations: []models.ProductLocation{}}
	err := r.readOnly(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			WITH l AS (
				SELECT ri.id AS item_id, r.id AS receipt_id, r.date, r.merchant_id, r.store_id, ri.unit_price_cents
				FROM receipt_items ri JOIN receipts r ON r.id = ri.receipt_id
				WHERE r.status = ANY($1) AND ri.product_id = $2 AND NOT ri.is_return)
			SELECT l.merchant_id, m.name, l.store_id, COALESCE(s.store_number, ''), COALESCE(s.address, ''),
			       count(*), count(DISTINCT l.receipt_id),
			       COALESCE(to_char(max(l.date), 'YYYY-MM-DD'), ''),
			       (array_agg(l.unit_price_cents ORDER BY l.date DESC NULLS LAST, l.receipt_id DESC, l.item_id DESC))[1],
			       (array_agg(l.receipt_id ORDER BY l.date DESC NULLS LAST, l.receipt_id DESC, l.item_id DESC))
			FROM l JOIN merchants m ON m.id = l.merchant_id LEFT JOIN stores s ON s.id = l.store_id
			GROUP BY l.merchant_id, m.name, l.store_id, s.store_number, s.address
			ORDER BY max(l.date) DESC NULLS LAST, max(l.receipt_id) DESC`, pq.Array(CountedStatuses), productID)
		if err != nil {
			return fmt.Errorf("where bought: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var loc models.ProductLocation
			var ids pq.Int64Array
			var distinct int
			if err := rows.Scan(&loc.MerchantID, &loc.MerchantName, &loc.StoreID, &loc.StoreNumber, &loc.StoreAddress,
				&loc.Purchases, &distinct, &loc.LastDate, &loc.LastUnitPriceCents, &ids); err != nil {
				return err
			}
			loc.ReceiptIDs = []int64{}
			for _, id := range ids {
				if len(loc.ReceiptIDs) == LocationReceiptIDs {
					break
				}
				if !slices.Contains(loc.ReceiptIDs, id) {
					loc.ReceiptIDs = append(loc.ReceiptIDs, id)
				}
			}
			loc.ReceiptIDsTruncated = distinct > len(loc.ReceiptIDs)
			wb.Locations = append(wb.Locations, loc)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		prow, err := tx.QueryContext(ctx, purchaseLineSQL+`
			WHERE r.status = ANY($1) AND ri.product_id = $2 AND NOT ri.is_return
			ORDER BY r.date DESC NULLS LAST, r.id DESC, ri.id DESC
			LIMIT $3`, pq.Array(CountedStatuses), productID, limit+1)
		if err != nil {
			return fmt.Errorf("where bought purchases: %w", err)
		}
		lines, err := scanLines(prow)
		if err != nil {
			return err
		}
		wb.Purchases, wb.Truncated = trim(lines, limit)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return wb, nil
}

func (r *queryRepo) LastPurchases(ctx context.Context, ids []int64) (map[int64]models.PurchaseLine, error) {
	out := map[int64]models.PurchaseLine{}
	if len(ids) == 0 {
		return out, nil
	}
	err := r.readOnly(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT * FROM (`+purchaseLineSQL+`
			WHERE r.status = ANY($1) AND ri.product_id = ANY($2) AND NOT ri.is_return
			ORDER BY ri.product_id, r.date DESC NULLS LAST, r.id DESC, ri.id DESC) x`, pq.Array(CountedStatuses), pq.Array(ids))
		if err != nil {
			return fmt.Errorf("last purchases: %w", err)
		}
		lines, err := scanLines(rows)
		if err != nil {
			return err
		}
		for _, l := range lines {
			if _, seen := out[*l.ProductID]; !seen {
				out[*l.ProductID] = l
			}
		}
		return nil
	})
	return out, err
}

// SpendReceiptIDs caps the receipt ids listed per spend group (newest first).
const SpendReceiptIDs = 100

// ErrBadGroupBy: Spend accepts group_by "merchant" or "month".
var ErrBadGroupBy = errors.New(`group_by must be "merchant" or "month"`)

func (r *queryRepo) Spend(ctx context.Context, f SpendFilter) ([]models.SpendGroup, error) {
	var key string
	switch f.GroupBy {
	case "", "merchant":
		key = "m.name"
	case "month":
		key = "COALESCE(to_char(r.date, 'YYYY-MM'), 'unknown')"
	default:
		return nil, ErrBadGroupBy
	}
	out := []models.SpendGroup{}
	err := r.readOnly(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT `+key+` AS k, count(*), sum(r.total_cents), sum(r.tax_cents), sum(r.savings_cents),
			       COALESCE(to_char(min(r.date), 'YYYY-MM-DD'), ''), COALESCE(to_char(max(r.date), 'YYYY-MM-DD'), ''),
			       (array_agg(r.id ORDER BY r.date DESC NULLS LAST, r.id DESC))[1:$5]
			FROM receipts r JOIN merchants m ON m.id = r.merchant_id
			WHERE r.status = ANY($1)
			  AND ($2::bigint = 0 OR r.merchant_id = $2)
			  AND ($3::date IS NULL OR r.date >= $3::date)
			  AND ($4::date IS NULL OR r.date <= $4::date)
			GROUP BY k ORDER BY k`, pq.Array(CountedStatuses), f.MerchantID, nullDate(f.From), nullDate(f.To), SpendReceiptIDs)
		if err != nil {
			return fmt.Errorf("spend: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var g models.SpendGroup
			var ids pq.Int64Array
			if err := rows.Scan(&g.Key, &g.ReceiptCount, &g.TotalCents, &g.TaxCents, &g.SavingsCents,
				&g.FirstDate, &g.LastDate, &ids); err != nil {
				return err
			}
			g.ReceiptIDs = []int64(ids)
			g.ReceiptIDsTruncated = g.ReceiptCount > len(g.ReceiptIDs)
			out = append(out, g)
		}
		return rows.Err()
	})
	return out, err
}
