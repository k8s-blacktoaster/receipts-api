package repository_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
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

// timedProvider sleeps d per call (or until cancelled), then returns the ok fixture.
type timedProvider struct {
	d       time.Duration
	calls   atomic.Int32
	entered chan struct{}
}

func (p *timedProvider) Name() string  { return "timed" }
func (p *timedProvider) Model() string { return "timed-1" }
func (p *timedProvider) Extract(ctx context.Context, _ []extraction.Image) (*extraction.Result, error) {
	if p.calls.Add(1) == 1 && p.entered != nil {
		close(p.entered)
	}
	select {
	case <-time.After(p.d):
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
	return &extraction.Result{Receipt: &e, Raw: raw, Model: "timed-1"}, nil
}

type app struct {
	q      *extraction.Queue
	router chi.Router
}

func newApp(t *testing.T, receipts repository.ReceiptRepository, docs repository.SourceDocumentRepository, blobs storage.BlobStore, prov extraction.Provider, jobTimeout time.Duration) *app {
	pl := &extraction.Pipeline{Receipts: receipts, Docs: docs, Blobs: blobs, Provider: prov}
	q := extraction.NewQueue(pl, 100, jobTimeout, nil)
	q.Start(context.Background(), 2)
	q.Recover(context.Background(), receipts.ResetStaleParsing, receipts.ListIDsByStatus) // startup
	uh := &handlers.UploadHandler{Store: docs, Receipts: receipts, Blobs: blobs, Queue: q, MaxBytes: 1 << 20, Logger: slog.Default()}
	rh := handlers.NewReceiptHandler(receipts, slog.Default())
	r := chi.NewRouter()
	r.Post("/api/v1/uploads", uh.UploadReceiptPhoto)
	r.Post("/api/v1/receipts/{id}/extract", uh.ReextractReceipt)
	r.Put("/api/v1/receipts/{id}", rh.UpdateReceipt)
	return &app{q: q, router: r}
}

func (a *app) upload(t *testing.T) int {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("image", "r.png")
	_, _ = fw.Write(pngFile(t))
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/uploads", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	a.router.ServeHTTP(rec, req)
	if rec.Code != 202 {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body)
	}
	var resp struct {
		Data models.UploadResult `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	return resp.Data.ReceiptID
}

func (a *app) call(method, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	a.router.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec
}

// MAJOR 1 repro: 12s jobs, 2s shutdown timeout, shutdown, immediate restart must reprocess
// the receipt, and re-extract must work afterwards.
func TestShutdownTimeoutReleasesAndRestartReprocesses(t *testing.T) {
	db := repository.TestDB(t)
	receipts, docs, blobs := repository.NewReceiptRepository(db), repository.NewSourceDocumentRepository(db), storage.NewMemory()

	slow := &timedProvider{d: 12 * time.Second, entered: make(chan struct{})}
	a1 := newApp(t, receipts, docs, blobs, slow, time.Minute)
	id := a1.upload(t)
	<-slow.entered
	start := time.Now()
	if a1.q.Shutdown(2 * time.Second) {
		t.Fatal("expected shutdown timeout")
	}
	if el := time.Since(start); el > 5*time.Second {
		t.Fatalf("shutdown took %s", el)
	}
	got, _ := receipts.GetByID(context.Background(), id)
	if got.Status != models.StatusUploaded {
		t.Fatalf("after aborted shutdown status = %s, want uploaded", got.Status)
	}

	fast := &timedProvider{d: 10 * time.Millisecond}
	a2 := newApp(t, receipts, docs, blobs, fast, time.Minute) // "restart"
	defer a2.q.Shutdown(5 * time.Second)
	waitStatus(t, receipts, id, models.StatusParsed)
	if rec := a2.call(http.MethodPost, "/api/v1/receipts/"+strconv.Itoa(id)+"/extract", ""); rec.Code != 202 {
		t.Fatalf("re-extract: %d %s", rec.Code, rec.Body)
	}
	deadline := time.Now().Add(5 * time.Second)
	for fast.calls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	waitStatus(t, receipts, id, models.StatusParsed)
	if fast.calls.Load() != 2 {
		t.Fatalf("restart provider calls = %d, want 2 (requeue + re-extract)", fast.calls.Load())
	}
}

// Release only touches its own claim; the periodic sweep resets old claims and requeues them.
func TestReleaseClaimTokenAndPeriodicSweep(t *testing.T) {
	db := repository.TestDB(t)
	ctx := context.Background()
	receipts, docs, blobs := repository.NewReceiptRepository(db), repository.NewSourceDocumentRepository(db), storage.NewMemory()
	if _, err := blobs.Put(ctx, "receipts/sweep.png", pngFile(t)); err != nil {
		t.Fatal(err)
	}
	rc := &models.Receipt{Source: models.SourcePhoto, Status: models.StatusUploaded}
	if err := docs.CreateUpload(ctx, &models.SourceDocument{ObjectKey: "receipts/sweep.png", SHA256: []byte("sweep-sha-000000000000000000000000"), MimeType: "image/png"}, rc); err != nil {
		t.Fatal(err)
	}
	old, ok, err := receipts.ClaimForParsing(ctx, rc.ID)
	if !ok || err != nil {
		t.Fatal(ok, err)
	}
	// claim is abandoned and swept, then re-claimed by another worker
	if _, err := db.Exec(`UPDATE receipts SET parse_started_at = now() - interval '1 hour' WHERE id = $1`, rc.ID); err != nil {
		t.Fatal(err)
	}
	if ids, _ := receipts.ResetStaleParsing(ctx, time.Minute); len(ids) != 1 {
		t.Fatalf("sweep ids %v", ids)
	}
	newTok, ok, _ := receipts.ClaimForParsing(ctx, rc.ID)
	if !ok {
		t.Fatal("re-claim failed")
	}
	// the old worker's writes must all be refused
	if rel, _ := receipts.ReleaseClaim(ctx, rc.ID, old); rel {
		t.Fatal("old token released a newer claim")
	}
	if err := receipts.FailParse(ctx, rc.ID, old, "late"); !errors.Is(err, models.ErrClaimLost) {
		t.Fatalf("old FailParse: %v", err)
	}
	stale := &models.Receipt{ID: rc.ID, StoreName: "OLD WORKER", Date: "2026-10-01", Source: models.SourcePhoto}
	if err := receipts.CompleteParse(ctx, stale, old, models.StatusParsed, ""); !errors.Is(err, models.ErrClaimLost) {
		t.Fatalf("old CompleteParse: %v", err)
	}
	if got, _ := receipts.GetByID(ctx, rc.ID); got.Status != models.StatusParsing || got.StoreName == "OLD WORKER" {
		t.Fatalf("old worker wrote: %+v", got)
	}
	// the current claim can release
	if rel, err := receipts.ReleaseClaim(ctx, rc.ID, newTok); !rel || err != nil {
		t.Fatalf("release own claim: %v %v", rel, err)
	}

	// periodic sweep: abandoned parsing row is reset and reprocessed without a restart
	if _, ok, _ := receipts.ClaimForParsing(ctx, rc.ID); !ok {
		t.Fatal("claim")
	}
	if _, err := db.Exec(`UPDATE receipts SET parse_started_at = now() - interval '1 hour' WHERE id = $1`, rc.ID); err != nil {
		t.Fatal(err)
	}
	prov := &timedProvider{d: time.Millisecond}
	pl := &extraction.Pipeline{Receipts: receipts, Docs: docs, Blobs: blobs, Provider: prov}
	q := extraction.NewQueue(pl, 10, time.Minute, nil)
	q.Start(ctx, 1)
	defer q.Shutdown(5 * time.Second)
	q.StartSweeper(ctx, 50*time.Millisecond, receipts.ResetStaleParsing, receipts.ListIDsByStatus)
	waitStatus(t, receipts, rc.ID, models.StatusParsed)
	if prov.calls.Load() != 1 {
		t.Fatalf("sweep provider calls = %d", prov.calls.Load())
	}
}

// MAJOR 2: PUT while parsing is 409 whatever the body, and the worker's final write still lands
// intact (the racing edit is refused, not silently overwritten).
func TestPutWhileParsingIsRefused(t *testing.T) {
	db := repository.TestDB(t)
	receipts, docs, blobs := repository.NewReceiptRepository(db), repository.NewSourceDocumentRepository(db), storage.NewMemory()
	slow := &timedProvider{d: 300 * time.Millisecond, entered: make(chan struct{})}
	a := newApp(t, receipts, docs, blobs, slow, time.Minute)
	defer a.q.Shutdown(5 * time.Second)
	id := a.upload(t)
	<-slow.entered
	path := "/api/v1/receipts/" + strconv.Itoa(id)
	for _, body := range []string{
		`{"store_name":"Hand Edit","date":"2026-10-01"}`,
		`{"store_name":"Hand Edit","date":"2026-10-01","status":"reviewed"}`,
		`{"store_name":"Hand Edit","date":"2026-10-01","status":"parsing"}`,
	} {
		if rec := a.call(http.MethodPut, path, body); rec.Code != 409 || !strings.Contains(rec.Body.String(), "being parsed") {
			t.Fatalf("PUT while parsing %s: %d %s", body, rec.Code, rec.Body)
		}
	}
	waitStatus(t, receipts, id, models.StatusParsed)
	got, _ := receipts.GetByID(context.Background(), id)
	if got.StoreName != "Sprouts Farmers Market" || len(got.Items) != 3 {
		t.Fatalf("worker result: %s items=%d", got.StoreName, len(got.Items))
	}
	// after parsing: user edits work, worker-only moves don't
	if rec := a.call(http.MethodPut, path, `{"store_name":"Sprouts","date":"2025-08-10","status":"reviewed"}`); rec.Code != 200 {
		t.Fatalf("review: %d %s", rec.Code, rec.Body)
	}
	if rec := a.call(http.MethodPut, path, `{"store_name":"Sprouts","date":"2025-08-10","status":"failed"}`); rec.Code != 409 {
		t.Fatalf("PUT failed: %d", rec.Code)
	}
}
