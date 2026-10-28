package extraction

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"receipts-db/internal/models"
	"receipts-db/internal/redact"
)

// ReceiptStore is the subset of the receipt repository the pipeline needs.
type ReceiptStore interface {
	GetByID(ctx context.Context, id int) (*models.Receipt, error)
	SetStatus(ctx context.Context, id int, status, parseError string, reviewerID *int64) error
	ClaimForParsing(ctx context.Context, id int) (models.ClaimToken, bool, error)
	CompleteParse(ctx context.Context, rc *models.Receipt, token models.ClaimToken, status, reason string) error
	FailParse(ctx context.Context, id int, token models.ClaimToken, reason string) error
	ReleaseClaim(ctx context.Context, id int, token models.ClaimToken) (bool, error)
}

// ReleaseTimeout bounds the best-effort claim release after a job is cancelled.
const ReleaseTimeout = 5 * time.Second

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

// StatusAborted is returned when the job's context was cancelled (shutdown timeout, job
// timeout); the claim was released back to "uploaded" so it is retried.
const StatusAborted = "aborted"

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

// failClaimed records a failure under this worker's claim. If the job context is already
// cancelled the claim is released instead, so the receipt is retried rather than failed.
func (p *Pipeline) failClaimed(ctx context.Context, rc *models.Receipt, tok models.ClaimToken, docID int64, raw json.RawMessage, model string, err error) (*Outcome, error) {
	if ctx.Err() != nil {
		return p.abort(rc.ID, tok, err)
	}
	msg := redact.String(err.Error())
	if len(msg) > 1000 {
		msg = msg[:1000]
	}
	if e := p.Receipts.FailParse(ctx, rc.ID, tok, msg); e != nil {
		if errors.Is(e, models.ErrClaimLost) {
			p.log().Warn("extraction claim lost; failure not recorded", "receipt_id", rc.ID)
			return &Outcome{Status: StatusSkipped}, nil
		}
		return nil, e
	}
	if docID != 0 {
		_ = p.Docs.SaveExtraction(ctx, docID, "failed", raw, model, PromptVersion, msg)
	}
	p.log().Warn("extraction failed", "receipt_id", rc.ID, "error", msg)
	return &Outcome{Status: models.StatusFailed, Reasons: []string{msg}}, nil
}

// abort releases this worker's claim (parsing -> uploaded, only if still ours) using a short
// background context, since the job context is already cancelled.
func (p *Pipeline) abort(id int, tok models.ClaimToken, cause error) (*Outcome, error) {
	ctx, cancel := context.WithTimeout(context.Background(), ReleaseTimeout)
	defer cancel()
	released, err := p.Receipts.ReleaseClaim(ctx, id, tok)
	if err != nil {
		p.log().Error("release claim failed; stale sweep will recover it", "receipt_id", id, "error", redact.String(err.Error()))
	} else {
		p.log().Warn("extraction aborted; receipt returned to uploaded", "receipt_id", id, "released", released, "cause", redact.String(cause.Error()))
	}
	return &Outcome{Status: StatusAborted}, nil
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
	tok, claimed, err := p.Receipts.ClaimForParsing(ctx, rc.ID)
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
		return p.failClaimed(ctx, rc, tok, docID, nil, "", fmt.Errorf("load source document: %w", err))
	}
	img, err := p.Blobs.Get(ctx, doc.ObjectKey)
	if err != nil {
		return p.failClaimed(ctx, rc, tok, docID, nil, "", fmt.Errorf("load image: %w", err))
	}
	res, err := p.Provider.Extract(ctx, []Image{{MIME: doc.MimeType, Data: img}})
	if err != nil {
		return p.failClaimed(ctx, rc, tok, docID, nil, p.Provider.Model(), fmt.Errorf("%s: %w", p.Provider.Name(), err))
	}
	if res == nil || res.Receipt == nil {
		return p.failClaimed(ctx, rc, tok, docID, nil, p.Provider.Model(), errors.New("provider returned no receipt"))
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
	reason := strings.Join(reasons, "; ")
	// One conditional write: fields, items and status land only if the row is still
	// "parsing" under our claim, so a reset/re-claim or racing edit is never overwritten.
	if err := p.Receipts.CompleteParse(ctx, rc, tok, status, reason); err != nil {
		if errors.Is(err, models.ErrClaimLost) {
			p.log().Warn("extraction claim lost; result discarded", "receipt_id", rc.ID)
			return &Outcome{Status: StatusSkipped}, nil
		}
		return p.failClaimed(ctx, rc, tok, docID, res.Raw, res.Model, fmt.Errorf("save extraction: %w", err))
	}
	if err := p.Docs.SaveExtraction(ctx, docID, status, res.Raw, res.Model, PromptVersion, reason); err != nil {
		return nil, err
	}
	p.log().Info("extraction done", "receipt_id", rc.ID, "status", status, "items", len(rc.Items))
	return &Outcome{Status: status, Reasons: reasons}, nil
}
