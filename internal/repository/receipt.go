package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/lib/pq"
	"strings"
	"time"

	"receipts-db/internal/database"
	"receipts-db/internal/models"
)

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("receipt not found")

// ErrInvalidTransition is returned when a status change is not in models' transition table
// (or the receipt changed status concurrently).
var ErrInvalidTransition = errors.New("invalid receipt status transition")

// ErrReceiptBusy is returned by edits while the receipt is queued for or undergoing
// extraction (uploaded|parsing), and by Delete while parsing.
var ErrReceiptBusy = errors.New("receipt is queued for or undergoing extraction")

// ErrNoPhoto is returned by RequestReextract for a receipt without a source document.
var ErrNoPhoto = errors.New("receipt has no photo")

type ReceiptRepository interface {
	Create(ctx context.Context, receipt *models.Receipt) error
	GetByID(ctx context.Context, id int) (*models.Receipt, error)
	Update(ctx context.Context, receipt *models.Receipt) error
	Delete(ctx context.Context, id int) error
	List(ctx context.Context, limit, offset int) ([]*models.Receipt, error)
	Count(ctx context.Context) (int, error)
	// UpdateItem edits one parsed line item during review (marks edited_at).
	UpdateItem(ctx context.Context, item *models.ReceiptItem) error
	// RequestReextract moves a photo receipt in parsed|needs_review|failed|uploaded to "uploaded"
	// (queued), so user edits are refused until the new parse lands. ErrReceiptBusy while
	// parsing, ErrInvalidTransition for reviewed, ErrNoPhoto, ErrNotFound.
	RequestReextract(ctx context.Context, id int) error
	// ListIDsByStatus returns receipt ids in the given statuses, oldest first (worker recovery).
	ListIDsByStatus(ctx context.Context, statuses ...string) ([]int, error)
	// ClaimForParsing atomically moves a photo receipt into "parsing" if it is in a
	// re-extractable status. Exactly one concurrent caller gets true.
	ClaimForParsing(ctx context.Context, id int) (models.ClaimToken, bool, error)
	// CompleteParse writes the extracted receipt (fields + children) and status parsed|needs_review,
	// only if the row is still "parsing" under token; otherwise models.ErrClaimLost and nothing is written.
	CompleteParse(ctx context.Context, rc *models.Receipt, token models.ClaimToken, status, reason string) error
	// FailParse sets "failed" + parse_error under the same condition.
	FailParse(ctx context.Context, id int, token models.ClaimToken, reason string) error
	// ReleaseClaim puts an aborted job back to "uploaded" under the same condition (false = not ours anymore).
	ReleaseClaim(ctx context.Context, id int, token models.ClaimToken) (bool, error)
	// ResetStaleParsing moves receipts stuck in "parsing" longer than olderThan back to
	// "uploaded" and returns their ids.
	ResetStaleParsing(ctx context.Context, olderThan time.Duration) ([]int, error)
	// SetStatus moves a receipt through the photo pipeline (transition table enforced).
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

// resolveMerchantStore finds or creates the merchant (by models.NormalizeMerchantName key) and,
// when a store number or address is given, the store location.
// resolveMerchantStore links the receipt to merchant/store. With keepMerchant (an edit that did
// not change the printed store name) the existing merchant_id is kept, so a merchant renamed in
// the UI is not re-derived from the printed name.
func resolveMerchantStore(ctx context.Context, q txExec, r *models.Receipt, keepMerchant bool) error {
	r.StoreName = strings.TrimSpace(r.StoreName) // as printed (legacy receipts.store_name)
	r.StoreNumber = strings.TrimSpace(r.StoreNumber)
	r.StoreAddress = strings.TrimSpace(r.StoreAddress)
	key := models.NormalizeMerchantName(r.StoreName)
	if key == "" {
		// photo receipts before/without a readable merchant: no merchant yet. The DB CHECK
		// receipts_complete_when_parsed only allows that while uploaded/parsing/needs_review/failed.
		r.MerchantID, r.StoreID = 0, nil
		return nil
	}
	var err error
	if !(keepMerchant && r.MerchantID != 0) {
		err = q.QueryRowContext(ctx, `
		INSERT INTO merchants (name, category) VALUES ($1, $2)
		ON CONFLICT (name) DO UPDATE SET category = COALESCE(merchants.category, EXCLUDED.category)
		RETURNING id`, key, nullStr(r.StoreCategory)).Scan(&r.MerchantID)
		if err != nil {
			return fmt.Errorf("failed to resolve merchant: %w", err)
		}
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
	// Items are re-inserted on update, so remember old ids to re-point adjustments.
	remap := map[int]int{}
	for i := range r.Items {
		it := &r.Items[i]
		oldID := it.ID
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
		if oldID != 0 {
			remap[oldID] = it.ID
		}
	}
	for i := range r.Adjustments {
		a := &r.Adjustments[i]
		a.ReceiptID = r.ID
		if a.AppliesToID != nil {
			if nid, ok := remap[*a.AppliesToID]; ok {
				a.AppliesToID = &nid
			} else {
				a.AppliesToID = nil // its item was removed in this edit; keep the adjustment unattached
			}
		}
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
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()
	if err := createReceiptTx(ctx, tx, receipt); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	return nil
}

func createReceiptTx(ctx context.Context, tx txExec, receipt *models.Receipt) error {
	receipt.Normalize()
	if err := resolveMerchantStore(ctx, tx, receipt, false); err != nil {
		return err
	}
	now := time.Now()
	receipt.CreatedAt, receipt.UpdatedAt = now, now

	err := tx.QueryRowContext(ctx, `
		INSERT INTO receipts (
			store_name, store_category, store_address, store_phone, date, purchased_at,
			merchant_id, store_id, subtotal_cents, tax_cents, total_cents, savings_cents, currency,
			payment_method, payment_last4, source, external_id, source_document_id,
			member_id, uploaded_by_id, confidence, status, parse_error, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$24)
		RETURNING id`,
		nullStr(receipt.StoreName), nullStr(receipt.StoreCategory), nullStr(receipt.StoreAddress), nullStr(receipt.StorePhone),
		nullStr(receipt.Date), receipt.PurchasedAt, nullID(receipt.MerchantID), receipt.StoreID,
		receipt.SubtotalCents, receipt.TaxCents, receipt.TotalCents, receipt.SavingsCents, receipt.Currency,
		nullStr(receipt.PaymentMethod), nullStr(receipt.PaymentLast4), receipt.Source, receipt.ExternalID,
		receipt.SourceDocumentID, receipt.MemberID, receipt.UploadedByID, receipt.Confidence,
		receipt.Status, nullStr(receipt.ParseError), now,
	).Scan(&receipt.ID)
	if err != nil {
		return fmt.Errorf("failed to create receipt: %w", err)
	}
	return insertChildren(ctx, tx, receipt, now)
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
		rc                                                 models.Receipt
		name, cat, addr, phone, num, date, pm, last4, perr sql.NullString
		merchant                                           sql.NullInt64
	)
	err := s.Scan(&rc.ID, &name, &cat, &addr, &phone, &num, &date, &rc.PurchasedAt,
		&merchant, &rc.StoreID, &rc.SubtotalCents, &rc.TaxCents, &rc.TotalCents, &rc.SavingsCents,
		&rc.Currency, &pm, &last4, &rc.Source, &rc.ExternalID, &rc.SourceDocumentID,
		&rc.MemberID, &rc.UploadedByID, &rc.Confidence, &rc.Status, &perr,
		&rc.ParseStartedAt, &rc.ParsedAt, &rc.ReviewedAt, &rc.ReviewedByID, &rc.CreatedAt, &rc.UpdatedAt)
	if err != nil {
		return nil, err
	}
	rc.StoreName, rc.Date, rc.MerchantID = name.String, date.String, merchant.Int64
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
	requested := receipt.Status // "" = keep current status
	receipt.Normalize()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Status changes go through the same transition table as SetStatus. The row lock keeps a
	// concurrent claim/SetStatus from slipping in between the check and the write.
	var current string
	var oldName sql.NullString
	var oldMerchant sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT status, store_name, merchant_id FROM receipts WHERE id = $1 FOR UPDATE`, receipt.ID).Scan(&current, &oldName, &oldMerchant); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("failed to load receipt status: %w", err)
	}
	if models.EditLocked(current) {
		return ErrReceiptBusy // a queued/running extraction would overwrite this edit
	}
	if requested == "" {
		receipt.Status = current
	} else if requested != current && !models.UserCanTransition(current, requested) {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, current, requested)
	}

	keep := oldMerchant.Valid && models.NormalizeMerchantName(receipt.StoreName) == models.NormalizeMerchantName(oldName.String)
	if keep {
		receipt.MerchantID = oldMerchant.Int64
	}
	if err := writeReceiptTx(ctx, tx, receipt, keep); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	return nil
}

// writeReceiptTx writes header fields and replaces children. Callers have locked the row.
func writeReceiptTx(ctx context.Context, tx *sql.Tx, receipt *models.Receipt, keepMerchant bool) error {
	if err := resolveMerchantStore(ctx, tx, receipt, keepMerchant); err != nil {
		return err
	}
	receipt.UpdatedAt = time.Now()
	res, err := tx.ExecContext(ctx, `
		UPDATE receipts SET
			store_name = $2, store_category = $3, store_address = $4, store_phone = $5, date = $6,
			purchased_at = $7, merchant_id = $8, store_id = $9, subtotal_cents = $10, tax_cents = $11,
			total_cents = $12, savings_cents = $13, currency = $14, payment_method = $15,
			payment_last4 = $16, member_id = $17, status = $18, updated_at = $19, confidence = $20
		WHERE id = $1`,
		receipt.ID, nullStr(receipt.StoreName), nullStr(receipt.StoreCategory), nullStr(receipt.StoreAddress),
		nullStr(receipt.StorePhone), nullStr(receipt.Date), receipt.PurchasedAt, nullID(receipt.MerchantID), receipt.StoreID,
		receipt.SubtotalCents, receipt.TaxCents, receipt.TotalCents, receipt.SavingsCents, receipt.Currency,
		nullStr(receipt.PaymentMethod), nullStr(receipt.PaymentLast4), receipt.MemberID, receipt.Status,
		receipt.UpdatedAt, receipt.Confidence)
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
	return insertChildren(ctx, tx, receipt, receipt.UpdatedAt)
}

func (r *receiptRepo) UpdateItem(ctx context.Context, it *models.ReceiptItem) error {
	if it.LineTotalCents == 0 && it.UnitPriceCents != 0 {
		it.LineTotalCents = it.ComputedLineTotalCents()
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()
	var status string
	err = tx.QueryRowContext(ctx, `SELECT r.status FROM receipts r JOIN receipt_items i ON i.receipt_id = r.id
		WHERE i.id = $1 FOR UPDATE OF r`, it.ID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("failed to lock receipt: %w", err)
	}
	if models.EditLocked(status) {
		return ErrReceiptBusy
	}
	now := time.Now()
	it.EditedAt = &now
	err = tx.QueryRowContext(ctx, `
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
	return tx.Commit()
}

func (r *receiptRepo) SetStatus(ctx context.Context, id int, status, parseError string, reviewerID *int64) error {
	from := models.StatusesAllowedInto(status)
	if len(from) == 0 {
		return fmt.Errorf("%w: unknown status %q", ErrInvalidTransition, status)
	}
	res, err := r.db.ExecContext(ctx, `
		UPDATE receipts SET
			status = $2,
			parse_error = $3,
			parse_started_at = CASE WHEN $2 = 'parsing' THEN now() ELSE parse_started_at END,
			parsed_at = CASE WHEN $2 IN ('parsed','needs_review') THEN now() ELSE parsed_at END,
			reviewed_at = CASE WHEN $2 = 'reviewed' THEN now() ELSE reviewed_at END,
			reviewed_by_id = CASE WHEN $2 = 'reviewed' THEN $4 ELSE reviewed_by_id END
		WHERE id = $1 AND status = ANY($5)`, id, status, nullStr(parseError), reviewerID, pq.Array(from))
	if err != nil {
		return fmt.Errorf("failed to set status: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var cur string
		if err := r.db.QueryRowContext(ctx, `SELECT status FROM receipts WHERE id = $1`, id).Scan(&cur); errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, cur, status)
	}
	return nil
}

func (r *receiptRepo) ClaimForParsing(ctx context.Context, id int) (models.ClaimToken, bool, error) {
	var tok time.Time
	// clock_timestamp() (not now()) so two claims in one transaction still differ
	err := r.db.QueryRowContext(ctx, `
		UPDATE receipts SET status = 'parsing', parse_started_at = clock_timestamp(), parse_error = NULL
		WHERE id = $1 AND source_document_id IS NOT NULL AND status = ANY($2)
		RETURNING parse_started_at`, id, pq.Array(models.ReextractableStatuses())).Scan(&tok)
	if errors.Is(err, sql.ErrNoRows) {
		return models.ClaimToken{}, false, nil
	}
	if err != nil {
		return models.ClaimToken{}, false, fmt.Errorf("failed to claim receipt: %w", err)
	}
	return models.ClaimToken(tok), true, nil
}

const claimedWhere = `id = $1 AND status = 'parsing' AND parse_started_at = $2`

func (r *receiptRepo) CompleteParse(ctx context.Context, rc *models.Receipt, token models.ClaimToken, status, reason string) error {
	if status != models.StatusParsed && status != models.StatusNeedsReview {
		return fmt.Errorf("%w: parsing -> %s", ErrInvalidTransition, status)
	}
	rc.Normalize()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()
	var one int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM receipts WHERE `+claimedWhere+` FOR UPDATE`, rc.ID, time.Time(token)).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return models.ErrClaimLost
	}
	if err != nil {
		return fmt.Errorf("failed to lock receipt: %w", err)
	}
	rc.Status = status
	if err := writeReceiptTx(ctx, tx, rc, false); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE receipts SET parse_error = $2, parsed_at = now() WHERE id = $1`, rc.ID, nullStr(reason)); err != nil {
		return fmt.Errorf("failed to record parse result: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	return nil
}

func (r *receiptRepo) FailParse(ctx context.Context, id int, token models.ClaimToken, reason string) error {
	res, err := r.db.ExecContext(ctx, `UPDATE receipts SET status = 'failed', parse_error = $3 WHERE `+claimedWhere,
		id, time.Time(token), nullStr(reason))
	if err != nil {
		return fmt.Errorf("failed to record parse failure: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return models.ErrClaimLost
	}
	return nil
}

func (r *receiptRepo) ReleaseClaim(ctx context.Context, id int, token models.ClaimToken) (bool, error) {
	res, err := r.db.ExecContext(ctx, `UPDATE receipts SET status = 'uploaded' WHERE `+claimedWhere, id, time.Time(token))
	if err != nil {
		return false, fmt.Errorf("failed to release claim: %w", err)
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

func (r *receiptRepo) ResetStaleParsing(ctx context.Context, olderThan time.Duration) ([]int, error) {
	rows, err := r.db.QueryContext(ctx, `
		UPDATE receipts SET status = 'uploaded'
		WHERE status = 'parsing' AND (parse_started_at IS NULL OR parse_started_at < now() - $1::interval)
		RETURNING id`, fmt.Sprintf("%d milliseconds", olderThan.Milliseconds()))
	if err != nil {
		return nil, fmt.Errorf("failed to reset stale parsing: %w", err)
	}
	defer rows.Close()
	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (r *receiptRepo) Delete(ctx context.Context, id int) error {
	// Refused while a worker holds the claim; a queued (uploaded) receipt may be deleted, its
	// job then finds no claimable row.
	res, err := r.db.ExecContext(ctx, `DELETE FROM receipts WHERE id = $1 AND status <> 'parsing'`, id)
	if err != nil {
		return fmt.Errorf("failed to delete receipt: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var st string
		if err := r.db.QueryRowContext(ctx, `SELECT status FROM receipts WHERE id = $1`, id).Scan(&st); err == nil {
			return ErrReceiptBusy
		}
		return ErrNotFound
	}
	return nil
}

func (r *receiptRepo) RequestReextract(ctx context.Context, id int) error {
	var got int
	err := r.db.QueryRowContext(ctx, `
		UPDATE receipts SET status = 'uploaded', parse_error = NULL
		WHERE id = $1 AND source_document_id IS NOT NULL
		  AND status IN ('uploaded','parsed','needs_review','failed')
		RETURNING id`, id).Scan(&got)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("failed to request re-extract: %w", err)
	}
	var st string
	var doc sql.NullInt64
	if err := r.db.QueryRowContext(ctx, `SELECT status, source_document_id FROM receipts WHERE id = $1`, id).Scan(&st, &doc); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	switch {
	case !doc.Valid:
		return ErrNoPhoto
	case st == models.StatusParsing:
		return ErrReceiptBusy
	default:
		return fmt.Errorf("%w: %s -> uploaded", ErrInvalidTransition, st)
	}
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

func (r *receiptRepo) ListIDsByStatus(ctx context.Context, statuses ...string) ([]int, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id FROM receipts WHERE status = ANY($1) ORDER BY id`, pq.Array(statuses))
	if err != nil {
		return nil, fmt.Errorf("failed to list receipts by status: %w", err)
	}
	defer rows.Close()
	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
