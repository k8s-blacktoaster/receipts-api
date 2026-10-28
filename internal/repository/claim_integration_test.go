package repository_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"receipts-db/internal/extraction"
	"receipts-db/internal/handlers"
	"receipts-db/internal/models"
	"receipts-db/internal/repository"
	"receipts-db/internal/storage"
)

// slowProvider blocks every call until release is closed and counts calls.
type slowProvider struct {
	calls   atomic.Int32
	entered chan struct{}
	release chan struct{}
}

func (s *slowProvider) Name() string  { return "slow" }
func (s *slowProvider) Model() string { return "slow-1" }
func (s *slowProvider) Extract(ctx context.Context, _ []extraction.Image) (*extraction.Result, error) {
	if s.calls.Add(1) == 1 {
		close(s.entered)
	}
	select {
	case <-s.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	raw, err := os.ReadFile("../extraction/testdata/receipt_ok.json")
	if err != nil {
		return nil, err
	}
	var e extraction.Extracted
	if err := json.Unmarshal(raw, &e); err != nil {
		return nil, err
	}
	return &extraction.Result{Receipt: &e, Raw: raw, Model: "slow-1"}, nil
}

func pngFile(t *testing.T) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Pix[0] = 77
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// Repro for the QA blocker: 4 workers, slow provider, an upload followed by 5 concurrent
// re-extract requests must result in exactly one provider call.
func TestConcurrentExtractSingleProviderCall(t *testing.T) {
	db := repository.TestDB(t)
	receipts, docs := repository.NewReceiptRepository(db), repository.NewSourceDocumentRepository(db)
	prov := &slowProvider{entered: make(chan struct{}), release: make(chan struct{})}
	blobs := storage.NewMemory()
	pl := &extraction.Pipeline{Receipts: receipts, Docs: docs, Blobs: blobs, Provider: prov}
	q := extraction.NewQueue(pl, 100, time.Minute, nil)
	q.Start(context.Background(), 4)
	defer q.Shutdown(5 * time.Second)

	h := &handlers.UploadHandler{Store: docs, Receipts: receipts, Blobs: blobs, Queue: q, MaxBytes: 1 << 20, Logger: slog.Default()}
	r := chi.NewRouter()
	r.Post("/api/v1/uploads", h.UploadReceiptPhoto)
	r.Post("/api/v1/receipts/{id}/extract", h.ReextractReceipt)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("image", "r.png")
	_, _ = fw.Write(pngFile(t))
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/uploads", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 202 {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body)
	}
	var resp struct {
		Data models.UploadResult `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	id := resp.Data.ReceiptID

	var wg sync.WaitGroup
	codes := make([]int, 5)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/receipts/"+itoa(id)+"/extract", nil))
			codes[i] = rec.Code
		}(i)
	}
	wg.Wait()
	for _, c := range codes {
		if c != 202 && c != 409 {
			t.Fatalf("extract codes %v", codes)
		}
	}
	<-prov.entered
	// while parsing: re-extract is 409
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/receipts/"+itoa(id)+"/extract", nil))
	if rec.Code != 409 {
		t.Fatalf("extract while parsing: %d", rec.Code)
	}
	close(prov.release)
	waitStatus(t, receipts, id, models.StatusParsed)
	time.Sleep(200 * time.Millisecond) // give any (incorrect) duplicate job time to run
	if n := prov.calls.Load(); n != 1 {
		t.Fatalf("provider calls = %d, want exactly 1 (codes %v)", n, codes)
	}
}

// The DB claim alone (no queue coalescing) admits exactly one of N concurrent Process calls.
func TestClaimForParsingIsAtomic(t *testing.T) {
	db := repository.TestDB(t)
	ctx := context.Background()
	receipts, docs := repository.NewReceiptRepository(db), repository.NewSourceDocumentRepository(db)
	blobs := storage.NewMemory()
	data := pngFile(t)
	if _, err := blobs.Put(ctx, "receipts/claim.png", data); err != nil {
		t.Fatal(err)
	}
	rc := &models.Receipt{Source: models.SourcePhoto, Status: models.StatusUploaded}
	if err := docs.CreateUpload(ctx, &models.SourceDocument{ObjectKey: "receipts/claim.png", SHA256: []byte("claim-sha-0000000000000000000000"), MimeType: "image/png"}, rc); err != nil {
		t.Fatal(err)
	}
	prov := &slowProvider{entered: make(chan struct{}), release: make(chan struct{})}
	pl := &extraction.Pipeline{Receipts: receipts, Docs: docs, Blobs: blobs, Provider: prov}
	var wg sync.WaitGroup
	var skipped atomic.Int32
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := pl.Process(ctx, rc.ID)
			if err != nil {
				t.Error(err)
				return
			}
			if out.Status == extraction.StatusSkipped {
				skipped.Add(1)
			}
		}()
	}
	<-prov.entered
	time.Sleep(100 * time.Millisecond)
	close(prov.release)
	wg.Wait()
	if prov.calls.Load() != 1 || skipped.Load() != 7 {
		t.Fatalf("calls=%d skipped=%d", prov.calls.Load(), skipped.Load())
	}

	// transition table enforced in SetStatus
	if err := receipts.SetStatus(ctx, rc.ID, models.StatusFailed, "", nil); !errors.Is(err, repository.ErrInvalidTransition) {
		t.Fatalf("parsed -> failed should be invalid: %v", err)
	}
	if err := receipts.SetStatus(ctx, 999999, models.StatusParsed, "", nil); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("missing receipt: %v", err)
	}
	// reviewed receipts are never claimable
	if err := receipts.SetStatus(ctx, rc.ID, models.StatusReviewed, "", nil); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := receipts.ClaimForParsing(ctx, rc.ID); ok || err != nil {
		t.Fatalf("claimed reviewed receipt: %v %v", ok, err)
	}
	// stale parsing reset
	if _, err := db.Exec(`UPDATE receipts SET status='parsing', parse_started_at=now()-interval '1 hour' WHERE id=$1`, rc.ID); err != nil {
		t.Fatal(err)
	}
	if ids, err := receipts.ResetStaleParsing(ctx, time.Minute); err != nil || len(ids) != 1 || ids[0] != rc.ID {
		t.Fatalf("reset: %v %v", ids, err)
	}
	if _, ok, _ := receipts.ClaimForParsing(ctx, rc.ID); !ok {
		t.Fatal("reset receipt should be claimable")
	}
	if ids, _ := receipts.ResetStaleParsing(ctx, time.Minute); len(ids) != 0 {
		t.Fatal("fresh claim must not be reset")
	}
}

func waitStatus(t *testing.T, r repository.ReceiptRepository, id int, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got, err := r.GetByID(context.Background(), id); err == nil && got.Status == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("receipt %d never reached %s", id, want)
}

func itoa(i int) string { return strconv.Itoa(i) }
