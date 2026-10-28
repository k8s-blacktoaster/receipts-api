package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"receipts-db/internal/database"
	"receipts-db/internal/models"
)

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("receipt not found")

type ReceiptRepository interface {
	Create(ctx context.Context, receipt *models.Receipt) error
	GetByID(ctx context.Context, id int) (*models.Receipt, error)
	Update(ctx context.Context, receipt *models.Receipt) error
	Delete(ctx context.Context, id int) error
	List(ctx context.Context, limit, offset int) ([]*models.Receipt, error)
	Count(ctx context.Context) (int, error)
	// UpdateItem edits one parsed line item during review (marks edited_at).
	UpdateItem(ctx context.Context, item *models.ReceiptItem) error
	// SetStatus moves a receipt through the photo pipeline.
	SetStatus(ctx context.Context, id int, status, parseError string, reviewerID *int64) error
}

type receiptRepo struct {
	db *database.DB
}

func NewReceiptRepository(db *database.DB) ReceiptRepository {
	return &receiptRepo{db: db}
}

type txExec interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// resolveMerchantStore finds or creates the merchant (by case-insensitive name) and,
// when a store number or address is given, the store location.
func resolveMerchantStore(ctx context.Context, q txExec, r *models.Receipt) error {
	r.StoreName = strings.TrimSpace(r.StoreName)
	r.StoreNumber = strings.TrimSpace(r.StoreNumber)
	r.StoreAddress = strings.TrimSpace(r.StoreAddress)
	if r.StoreName == "" {
		return errors.New("store name is required")
	}
	err := q.QueryRowContext(ctx, `
		INSERT INTO merchants (name, category) VALUES ($1, $2)
		ON CONFLICT ((lower(btrim(name)))) DO UPDATE SET category = COALESCE(merchants.category, EXCLUDED.category)
		RETURNING id`, r.StoreName, nullStr(r.StoreCategory)).Scan(&r.MerchantID)
	if err != nil {
		return fmt.Errorf("failed to resolve merchant: %w", err)
	}
	if r.StoreNumber == "" && r.StoreAddress == "" {
		r.StoreID = nil
		return nil
	}
	var id int64
	if r.StoreNumber != "" {
		err = q.QueryRowContext(ctx, `
			INSERT INTO stores (merchant_id, store_number, address, phone) VALUES ($1, $2, $3, $4)
			ON CONFLICT (merchant_id, store_number) WHERE store_number IS NOT NULL
			DO UPDATE SET address = COALESCE(EXCLUDED.address, stores.address),
			              phone = COALESCE(EXCLUDED.phone, stores.phone)
			RETURNING id`, r.MerchantID, r.StoreNumber, nullStr(r.StoreAddress), nullStr(r.StorePhone)).Scan(&id)
	} else {
		err = q.QueryRowContext(ctx, `
			INSERT INTO stores (merchant_id, address, phone) VALUES ($1, $2, $3)
			ON CONFLICT (merchant_id, lower(address)) WHERE store_number IS NULL AND address IS NOT NULL
			DO UPDATE SET phone = COALESCE(EXCLUDED.phone, stores.phone)
			RETURNING id`, r.MerchantID, r.StoreAddress, nullStr(r.StorePhone)).Scan(&id)
	}
	if err != nil {
		return fmt.Errorf("failed to resolve store: %w", err)
	}
	r.StoreID = &id
	return nil
}

func insertChildren(ctx context.Context, q txExec, r *models.Receipt, now time.Time) error {
	for i := range r.Items {
		it := &r.Items[i]
		it.ReceiptID = r.ID
		it.CreatedAt, it.UpdatedAt = now, now
		err := q.QueryRowContext(ctx, `
			INSERT INTO receipt_items (
				receipt_id, line_no, raw_text, sku, item_number, upc, product_id, name, description,
				quantity, unit, unit_price_cents, line_total_cents, discount_cents,
				taxable, tax_flag, is_return, edited_at, created_at, updated_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$19)
			RETURNING id, net_total_cents`,
			it.ReceiptID, it.LineNo, nullStr(it.RawText), nullStr(it.SKU), nullStr(it.ItemNumber), nullStr(it.UPC),
			it.ProductID, it.Name, nullStr(it.Description), it.Quantity, nullStr(it.Unit),
			it.UnitPriceCents, it.LineTotalCents, it.DiscountCents, it.Taxable, nullStr(it.TaxFlag),
			it.IsReturn, it.EditedAt, now,
		).Scan(&it.ID, &it.NetTotalCents)
		if err != nil {
			return fmt.Errorf("failed to create receipt item: %w", err)
		}
	}
	for i := range r.Adjustments {
		a := &r.Adjustments[i]
		a.ReceiptID = r.ID
		if err := q.QueryRowContext(ctx, `
			INSERT INTO receipt_adjustments (receipt_id, applies_to_item_id, kind, raw_text, amount_cents)
			VALUES ($1,$2,$3,$4,$5) RETURNING id`,
			a.ReceiptID, a.AppliesToID, a.Kind, nullStr(a.RawText), a.AmountCents).Scan(&a.ID); err != nil {
			return fmt.Errorf("failed to create receipt adjustment: %w", err)
		}
	}
	return nil
}

func (r *receiptRepo) Create(ctx context.Context, receipt *models.Receipt) error {
	receipt.Normalize()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	if err := resolveMerchantStore(ctx, tx, receipt); err != nil {
		return err
	}
	now := time.Now()
	receipt.CreatedAt, receipt.UpdatedAt = now, now

	err = tx.QueryRowContext(ctx, `
		INSERT INTO receipts (
			store_name, store_category, store_address, store_phone, date, purchased_at,
			merchant_id, store_id, subtotal_cents, tax_cents, total_cents, savings_cents, currency,
			payment_method, payment_last4, source, external_id, source_document_id,
			member_id, uploaded_by_id, confidence, status, parse_error, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$24)
		RETURNING id`,
		receipt.StoreName, nullStr(receipt.StoreCategory), nullStr(receipt.StoreAddress), nullStr(receipt.StorePhone),
		receipt.Date, receipt.PurchasedAt, receipt.MerchantID, receipt.StoreID,
		receipt.SubtotalCents, receipt.TaxCents, receipt.TotalCents, receipt.SavingsCents, receipt.Currency,
		nullStr(receipt.PaymentMethod), nullStr(receipt.PaymentLast4), receipt.Source, receipt.ExternalID,
		receipt.SourceDocumentID, receipt.MemberID, receipt.UploadedByID, receipt.Confidence,
		receipt.Status, nullStr(receipt.ParseError), now,
	).Scan(&receipt.ID)
	if err != nil {
		return fmt.Errorf("failed to create receipt: %w", err)
	}
	if err := insertChildren(ctx, tx, receipt, now); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	return nil
}

const receiptCols = `
	r.id, r.store_name, r.store_category, r.store_address, r.store_phone, s.store_number,
	to_char(r.date, 'YYYY-MM-DD'), r.purchased_at, r.merchant_id, r.store_id,
	r.subtotal_cents, r.tax_cents, r.total_cents, r.savings_cents, r.currency,
	r.payment_method, r.payment_last4, r.source, r.external_id, r.source_document_id,
	r.member_id, r.uploaded_by_id, r.confidence::float8, r.status, r.parse_error,
	r.parse_started_at, r.parsed_at, r.reviewed_at, r.reviewed_by_id, r.created_at, r.updated_at`

type rowScanner interface{ Scan(dest ...any) error }

func scanReceipt(s rowScanner) (*models.Receipt, error) {
	var (
		rc                                     models.Receipt
		cat, addr, phone, num, pm, last4, perr sql.NullString
	)
	err := s.Scan(&rc.ID, &rc.StoreName, &cat, &addr, &phone, &num, &rc.Date, &rc.PurchasedAt,
		&rc.MerchantID, &rc.StoreID, &rc.SubtotalCents, &rc.TaxCents, &rc.TotalCents, &rc.SavingsCents,
		&rc.Currency, &pm, &last4, &rc.Source, &rc.ExternalID, &rc.SourceDocumentID,
		&rc.MemberID, &rc.UploadedByID, &rc.Confidence, &rc.Status, &perr,
		&rc.ParseStartedAt, &rc.ParsedAt, &rc.ReviewedAt, &rc.ReviewedByID, &rc.CreatedAt, &rc.UpdatedAt)
	if err != nil {
		return nil, err
	}
	rc.StoreCategory, rc.StoreAddress, rc.StorePhone, rc.StoreNumber = cat.String, addr.String, phone.String, num.String
	rc.PaymentMethod, rc.PaymentLast4, rc.ParseError = pm.String, last4.String, perr.String
	return &rc, nil
}

func (r *receiptRepo) GetByID(ctx context.Context, id int) (*models.Receipt, error) {
	receipt, err := scanReceipt(r.db.QueryRowContext(ctx,
		`SELECT `+receiptCols+` FROM receipts r LEFT JOIN stores s ON s.id = r.store_id WHERE r.id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get receipt: %w", err)
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT id, receipt_id, line_no, raw_text, sku, item_number, upc, product_id, name, description,
		       quantity::float8, unit, unit_price_cents, line_total_cents, discount_cents, net_total_cents,
		       taxable, tax_flag, is_return, edited_at, created_at, updated_at
		FROM receipt_items WHERE receipt_id = $1 ORDER BY line_no NULLS LAST, id`, id)
	if err != nil {
		return nil, fmt.Errorf("failed to get receipt items: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var it models.ReceiptItem
		var raw, sku, itemNo, upc, desc, unit, flag sql.NullString
		var created, updated time.Time
		if err := rows.Scan(&it.ID, &it.ReceiptID, &it.LineNo, &raw, &sku, &itemNo, &upc, &it.ProductID,
			&it.Name, &desc, &it.Quantity, &unit, &it.UnitPriceCents, &it.LineTotalCents, &it.DiscountCents,
			&it.NetTotalCents, &it.Taxable, &flag, &it.IsReturn, &it.EditedAt, &created, &updated); err != nil {
			return nil, fmt.Errorf("failed to scan receipt item: %w", err)
		}
		it.RawText, it.SKU, it.ItemNumber, it.UPC = raw.String, sku.String, itemNo.String, upc.String
		it.Description, it.Unit, it.TaxFlag = desc.String, unit.String, flag.String
		it.CreatedAt, it.UpdatedAt = created, updated
		receipt.Items = append(receipt.Items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating receipt items: %w", err)
	}

	arows, err := r.db.QueryContext(ctx, `
		SELECT id, receipt_id, applies_to_item_id, kind, raw_text, amount_cents
		FROM receipt_adjustments WHERE receipt_id = $1 ORDER BY id`, id)
	if err != nil {
		return nil, fmt.Errorf("failed to get adjustments: %w", err)
	}
	defer arows.Close()
	for arows.Next() {
		var a models.ReceiptAdjustment
		var raw sql.NullString
		var applies sql.NullInt64
		if err := arows.Scan(&a.ID, &a.ReceiptID, &applies, &a.Kind, &raw, &a.AmountCents); err != nil {
			return nil, fmt.Errorf("failed to scan adjustment: %w", err)
		}
		if applies.Valid {
			v := int(applies.Int64)
			a.AppliesToID = &v
		}
		a.RawText = raw.String
		receipt.Adjustments = append(receipt.Adjustments, a)
	}
	return receipt, arows.Err()
}

func (r *receiptRepo) Update(ctx context.Context, receipt *models.Receipt) error {
	receipt.Normalize()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	if err := resolveMerchantStore(ctx, tx, receipt); err != nil {
		return err
	}
	receipt.UpdatedAt = time.Now()
	res, err := tx.ExecContext(ctx, `
		UPDATE receipts SET
			store_name = $2, store_category = $3, store_address = $4, store_phone = $5, date = $6,
			purchased_at = $7, merchant_id = $8, store_id = $9, subtotal_cents = $10, tax_cents = $11,
			total_cents = $12, savings_cents = $13, currency = $14, payment_method = $15,
			payment_last4 = $16, member_id = $17, status = $18, updated_at = $19
		WHERE id = $1`,
		receipt.ID, receipt.StoreName, nullStr(receipt.StoreCategory), nullStr(receipt.StoreAddress),
		nullStr(receipt.StorePhone), receipt.Date, receipt.PurchasedAt, receipt.MerchantID, receipt.StoreID,
		receipt.SubtotalCents, receipt.TaxCents, receipt.TotalCents, receipt.SavingsCents, receipt.Currency,
		nullStr(receipt.PaymentMethod), nullStr(receipt.PaymentLast4), receipt.MemberID, receipt.Status,
		receipt.UpdatedAt)
	if err != nil {
		return fmt.Errorf("failed to update receipt: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	// Replace children (simple approach, as before).
	if _, err := tx.ExecContext(ctx, "DELETE FROM receipt_adjustments WHERE receipt_id = $1", receipt.ID); err != nil {
		return fmt.Errorf("failed to delete existing adjustments: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM receipt_items WHERE receipt_id = $1", receipt.ID); err != nil {
		return fmt.Errorf("failed to delete existing items: %w", err)
	}
	if err := insertChildren(ctx, tx, receipt, receipt.UpdatedAt); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	return nil
}

func (r *receiptRepo) UpdateItem(ctx context.Context, it *models.ReceiptItem) error {
	if it.LineTotalCents == 0 && it.UnitPriceCents != 0 {
		it.LineTotalCents = it.ComputedLineTotalCents()
	}
	now := time.Now()
	it.EditedAt = &now
	err := r.db.QueryRowContext(ctx, `
		UPDATE receipt_items SET
			line_no = $2, raw_text = $3, sku = $4, item_number = $5, upc = $6, product_id = $7, name = $8,
			description = $9, quantity = $10, unit = $11, unit_price_cents = $12, line_total_cents = $13,
			discount_cents = $14, taxable = $15, tax_flag = $16, is_return = $17, edited_at = $18
		WHERE id = $1 RETURNING receipt_id, net_total_cents`,
		it.ID, it.LineNo, nullStr(it.RawText), nullStr(it.SKU), nullStr(it.ItemNumber), nullStr(it.UPC),
		it.ProductID, it.Name, nullStr(it.Description), it.Quantity, nullStr(it.Unit), it.UnitPriceCents,
		it.LineTotalCents, it.DiscountCents, it.Taxable, nullStr(it.TaxFlag), it.IsReturn, now,
	).Scan(&it.ReceiptID, &it.NetTotalCents)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("failed to update item: %w", err)
	}
	return nil
}

func (r *receiptRepo) SetStatus(ctx context.Context, id int, status, parseError string, reviewerID *int64) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE receipts SET
			status = $2,
			parse_error = $3,
			parse_started_at = CASE WHEN $2 = 'parsing' THEN now() ELSE parse_started_at END,
			parsed_at = CASE WHEN $2 IN ('parsed','needs_review') THEN now() ELSE parsed_at END,
			reviewed_at = CASE WHEN $2 = 'reviewed' THEN now() ELSE reviewed_at END,
			reviewed_by_id = CASE WHEN $2 = 'reviewed' THEN $4 ELSE reviewed_by_id END
		WHERE id = $1`, id, status, nullStr(parseError), reviewerID)
	if err != nil {
		return fmt.Errorf("failed to set status: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *receiptRepo) Delete(ctx context.Context, id int) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM receipts WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("failed to delete receipt: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *receiptRepo) List(ctx context.Context, limit, offset int) ([]*models.Receipt, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+receiptCols+`
		FROM receipts r LEFT JOIN stores s ON s.id = r.store_id
		ORDER BY r.date DESC, r.created_at DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list receipts: %w", err)
	}
	defer rows.Close()
	var out []*models.Receipt
	for rows.Next() {
		rc, err := scanReceipt(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan receipt: %w", err)
		}
		out = append(out, rc)
	}
	return out, rows.Err()
}

func (r *receiptRepo) Count(ctx context.Context) (int, error) {
	var count int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM receipts`).Scan(&count); err != nil {
		return 0, fmt.Errorf("failed to count receipts: %w", err)
	}
	return count, nil
}
