package extraction

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"receipts-db/internal/models"
	"receipts-db/internal/redact"
)

// ReceiptStore is the subset of the receipt repository the pipeline needs.
type ReceiptStore interface {
	GetByID(ctx context.Context, id int) (*models.Receipt, error)
	Update(ctx context.Context, r *models.Receipt) error
	SetStatus(ctx context.Context, id int, status, parseError string, reviewerID *int64) error
	ClaimForParsing(ctx context.Context, id int) (bool, error)
}

// DocStore is the subset of the source-document repository the pipeline needs.
type DocStore interface {
	GetByID(ctx context.Context, id int64) (*models.SourceDocument, error)
	SaveExtraction(ctx context.Context, id int64, state string, raw json.RawMessage, model, promptVersion, errMsg string) error
}

// BlobGetter fetches image bytes.
type BlobGetter interface {
	Get(ctx context.Context, key string) ([]byte, error)
}

// Pipeline moves a receipt: uploaded -> parsing -> parsed | needs_review | failed.
type Pipeline struct {
	Receipts       ReceiptStore
	Docs           DocStore
	Blobs          BlobGetter
	Provider       Provider
	ToleranceCents int64
	Logger         *slog.Logger
}

// StatusSkipped is returned when another worker already holds (or finished) the receipt.
const StatusSkipped = "skipped"

// Outcome is the final state of one Process call.
type Outcome struct {
	Status  string
	Reasons []string
}

func (p *Pipeline) fail(ctx context.Context, rc *models.Receipt, docID int64, raw json.RawMessage, model string, err error) (*Outcome, error) {
	msg := redact.String(err.Error())
	if len(msg) > 1000 {
		msg = msg[:1000]
	}
	if e := p.Receipts.SetStatus(ctx, rc.ID, models.StatusFailed, msg, nil); e != nil {
		return nil, errors.Join(err, e)
	}
	if docID != 0 {
		_ = p.Docs.SaveExtraction(ctx, docID, "failed", raw, model, PromptVersion, msg)
	}
	p.log().Warn("extraction failed", "receipt_id", rc.ID, "error", msg)
	return &Outcome{Status: models.StatusFailed, Reasons: []string{msg}}, nil
}

func (p *Pipeline) log() *slog.Logger {
	if p.Logger != nil {
		return p.Logger
	}
	return slog.Default()
}

// Process runs extraction for one receipt. A returned error means the pipeline itself could
// not record an outcome (e.g. DB down); provider/mapping failures are recorded as status failed.
func (p *Pipeline) Process(ctx context.Context, receiptID int) (*Outcome, error) {
	rc, err := p.Receipts.GetByID(ctx, receiptID)
	if err != nil {
		return nil, fmt.Errorf("load receipt %d: %w", receiptID, err)
	}
	if rc.SourceDocumentID == nil {
		return p.fail(ctx, rc, 0, nil, "", errors.New("receipt has no source document"))
	}
	docID := *rc.SourceDocumentID
	if p.Provider == nil {
		return p.fail(ctx, rc, docID, nil, "", errors.New("extraction disabled (no provider configured)"))
	}
	// Atomic claim: only one worker/replica moves this receipt into "parsing".
	claimed, err := p.Receipts.ClaimForParsing(ctx, rc.ID)
	if err != nil {
		return nil, err
	}
	if !claimed {
		p.log().Info("extraction skipped; receipt not claimable", "receipt_id", rc.ID, "status", rc.Status)
		return &Outcome{Status: StatusSkipped}, nil
	}
	rc.Status = models.StatusParsing
	_ = p.Docs.SaveExtraction(ctx, docID, "extracting", nil, p.Provider.Model(), PromptVersion, "")

	doc, err := p.Docs.GetByID(ctx, docID)
	if err != nil {
		return p.fail(ctx, rc, docID, nil, "", fmt.Errorf("load source document: %w", err))
	}
	img, err := p.Blobs.Get(ctx, doc.ObjectKey)
	if err != nil {
		return p.fail(ctx, rc, docID, nil, "", fmt.Errorf("load image: %w", err))
	}
	res, err := p.Provider.Extract(ctx, []Image{{MIME: doc.MimeType, Data: img}})
	if err != nil {
		return p.fail(ctx, rc, docID, nil, p.Provider.Model(), fmt.Errorf("%s: %w", p.Provider.Name(), err))
	}
	if res == nil || res.Receipt == nil {
		return p.fail(ctx, rc, docID, nil, p.Provider.Model(), errors.New("provider returned no receipt"))
	}

	Apply(rc, res.Receipt)
	tol := p.ToleranceCents
	if tol <= 0 {
		tol = DefaultToleranceCents
	}
	reasons := Check(rc, res.Receipt, tol)
	status := models.StatusParsed
	if len(reasons) > 0 {
		status = models.StatusNeedsReview
	}
	// Update writes fields/items while still "parsing"; SetStatus then performs the
	// checked parsing -> parsed|needs_review transition.
	rc.Status = models.StatusParsing
	if err := p.Receipts.Update(ctx, rc); err != nil {
		return p.fail(ctx, rc, docID, res.Raw, res.Model, fmt.Errorf("save extraction: %w", err))
	}
	reason := strings.Join(reasons, "; ")
	if err := p.Receipts.SetStatus(ctx, rc.ID, status, reason, nil); err != nil {
		return nil, err
	}
	if err := p.Docs.SaveExtraction(ctx, docID, status, res.Raw, res.Model, PromptVersion, reason); err != nil {
		return nil, err
	}
	p.log().Info("extraction done", "receipt_id", rc.ID, "status", status, "items", len(rc.Items))
	return &Outcome{Status: status, Reasons: reasons}, nil
}
