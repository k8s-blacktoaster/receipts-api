package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"receipts-db/internal/database"
	"receipts-db/internal/models"
)

// SourceDocumentRepository stores uploaded photos' metadata and raw extraction output.
type SourceDocumentRepository interface {
	// FindReceiptBySHA returns the receipt created for an identical image, if any.
	FindReceiptBySHA(ctx context.Context, sha []byte) (receiptID int, docID int64, status string, found bool, err error)
	// CreateUpload inserts the document and its pending receipt in one transaction.
	CreateUpload(ctx context.Context, doc *models.SourceDocument, receipt *models.Receipt) error
	GetByID(ctx context.Context, id int64) (*models.SourceDocument, error)
	// SaveExtraction records parse state, raw provider output, model and error.
	SaveExtraction(ctx context.Context, id int64, state string, raw json.RawMessage, model, promptVersion, errMsg string) error
}

type sourceDocRepo struct{ db *database.DB }

func NewSourceDocumentRepository(db *database.DB) SourceDocumentRepository {
	return &sourceDocRepo{db: db}
}

func (r *sourceDocRepo) FindReceiptBySHA(ctx context.Context, sha []byte) (int, int64, string, bool, error) {
	var (
		rid    int
		did    int64
		status string
	)
	err := r.db.QueryRowContext(ctx, `
		SELECT r.id, d.id, r.status FROM source_documents d JOIN receipts r ON r.source_document_id = d.id
		WHERE d.sha256 = $1`, sha).Scan(&rid, &did, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, "", false, nil
	}
	if err != nil {
		return 0, 0, "", false, fmt.Errorf("failed to look up upload: %w", err)
	}
	return rid, did, status, true, nil
}

func (r *sourceDocRepo) CreateUpload(ctx context.Context, doc *models.SourceDocument, receipt *models.Receipt) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()
	if doc.Kind == "" {
		doc.Kind = "photo"
	}
	if doc.ParseState == "" {
		doc.ParseState = "pending"
	}
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO source_documents (kind, object_key, sha256, mime_type, uploaded_by_id, parse_state)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id, created_at`,
		doc.Kind, nullStr(doc.ObjectKey), doc.SHA256, nullStr(doc.MimeType), doc.UploadedByID, doc.ParseState,
	).Scan(&doc.ID, &doc.CreatedAt); err != nil {
		return fmt.Errorf("failed to create source document: %w", err)
	}
	receipt.SourceDocumentID = &doc.ID
	if err := createReceiptTx(ctx, tx, receipt); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	return nil
}

func (r *sourceDocRepo) GetByID(ctx context.Context, id int64) (*models.SourceDocument, error) {
	var (
		d                         models.SourceDocument
		key, mime, model, pv, msg sql.NullString
		raw                       []byte
	)
	err := r.db.QueryRowContext(ctx, `
		SELECT id, kind, object_key, sha256, mime_type, uploaded_by_id, raw_extraction, model, prompt_version,
		       parse_state, error, parsed_at, created_at
		FROM source_documents WHERE id = $1`, id).Scan(&d.ID, &d.Kind, &key, &d.SHA256, &mime, &d.UploadedByID,
		&raw, &model, &pv, &d.ParseState, &msg, &d.ParsedAt, &d.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get source document: %w", err)
	}
	d.ObjectKey, d.MimeType, d.Model, d.PromptVersion, d.Error = key.String, mime.String, model.String, pv.String, msg.String
	if len(raw) > 0 {
		d.RawExtraction = raw
	}
	return &d, nil
}

func (r *sourceDocRepo) SaveExtraction(ctx context.Context, id int64, state string, raw json.RawMessage, model, promptVersion, errMsg string) error {
	var rawArg any
	if len(raw) > 0 {
		rawArg = []byte(raw)
	}
	res, err := r.db.ExecContext(ctx, `
		UPDATE source_documents SET parse_state = $2, raw_extraction = COALESCE($3::jsonb, raw_extraction),
		       model = COALESCE($4, model), prompt_version = COALESCE($5, prompt_version), error = $6,
		       parsed_at = CASE WHEN $2 IN ('parsed','needs_review') THEN now() ELSE parsed_at END
		WHERE id = $1`, id, state, rawArg, nullStr(model), nullStr(promptVersion), nullStr(errMsg))
	if err != nil {
		return fmt.Errorf("failed to save extraction: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
