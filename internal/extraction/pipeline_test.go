package extraction

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"receipts-db/internal/models"
	"receipts-db/internal/storage"
)

type statusCall struct {
	status, reason string
}

type fakeReceipts struct {
	mu       sync.Mutex
	r        *models.Receipt
	calls    []statusCall
	updated  *models.Receipt
	updateEr error
}

func (f *fakeReceipts) GetByID(_ context.Context, id int) (*models.Receipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *f.r
	return &cp, nil
}
func (f *fakeReceipts) Update(_ context.Context, r *models.Receipt) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.updateEr != nil {
		return f.updateEr
	}
	cp := *r
	f.updated = &cp
	return nil
}
func (f *fakeReceipts) SetStatus(_ context.Context, id int, status, reason string, _ *int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, statusCall{status, reason})
	return nil
}

type fakeDocs struct {
	doc    models.SourceDocument
	states []string
	raw    json.RawMessage
	model  string
}

func (f *fakeDocs) GetByID(context.Context, int64) (*models.SourceDocument, error) {
	d := f.doc
	return &d, nil
}
func (f *fakeDocs) SaveExtraction(_ context.Context, _ int64, state string, raw json.RawMessage, model, _, _ string) error {
	f.states = append(f.states, state)
	if raw != nil {
		f.raw = raw
	}
	if model != "" {
		f.model = model
	}
	return nil
}

type stubProvider struct {
	res *Result
	err error
}

func (s stubProvider) Name() string  { return "stub" }
func (s stubProvider) Model() string { return "stub-1" }
func (s stubProvider) Extract(context.Context, []Image) (*Result, error) {
	return s.res, s.err
}

func setup(t *testing.T, p Provider) (*Pipeline, *fakeReceipts, *fakeDocs) {
	t.Helper()
	doc := int64(3)
	blobs := storage.NewMemory()
	_ = blobs.Put(context.Background(), "receipts/ab/abc.jpg", jpeg)
	fr := &fakeReceipts{r: &models.Receipt{ID: 11, Source: models.SourcePhoto, Status: models.StatusUploaded, SourceDocumentID: &doc}}
	fd := &fakeDocs{doc: models.SourceDocument{ID: 3, ObjectKey: "receipts/ab/abc.jpg", MimeType: "image/jpeg"}}
	return &Pipeline{Receipts: fr, Docs: fd, Blobs: blobs, Provider: p}, fr, fd
}

func result(t *testing.T, name string) *Result {
	raw := json.RawMessage(fixture(t, name))
	return &Result{Receipt: parse(t, name), Raw: raw, Model: "stub-1"}
}

func statuses(c []statusCall) string {
	var s []string
	for _, x := range c {
		s = append(s, x.status)
	}
	return strings.Join(s, ">")
}

func TestPipelineParsed(t *testing.T) {
	p, fr, fd := setup(t, stubProvider{res: result(t, "receipt_ok.json")})
	out, err := p.Process(context.Background(), 11)
	if err != nil || out.Status != models.StatusParsed {
		t.Fatalf("%v %v", out, err)
	}
	if statuses(fr.calls) != "parsing>parsed" || fr.calls[1].reason != "" {
		t.Fatalf("transitions: %+v", fr.calls)
	}
	if fr.updated.StoreName != "Sprouts Farmers Market" || fr.updated.Status != models.StatusParsed || len(fr.updated.Items) != 3 {
		t.Fatalf("update: %+v", fr.updated)
	}
	if strings.Join(fd.states, ">") != "extracting>parsed" || !json.Valid(fd.raw) || fd.model != "stub-1" {
		t.Fatalf("doc: %v %s %s", fd.states, fd.raw, fd.model)
	}
}

func TestPipelineMismatchNeedsReview(t *testing.T) {
	p, fr, fd := setup(t, stubProvider{res: result(t, "receipt_mismatch.json")})
	out, err := p.Process(context.Background(), 11)
	if err != nil || out.Status != models.StatusNeedsReview {
		t.Fatalf("%v %v", out, err)
	}
	last := fr.calls[len(fr.calls)-1]
	if last.status != models.StatusNeedsReview || !strings.Contains(last.reason, "total mismatch") {
		t.Fatalf("reason: %+v", last)
	}
	if fd.states[len(fd.states)-1] != "needs_review" {
		t.Fatalf("doc state: %v", fd.states)
	}
}

func TestPipelineProviderFailure(t *testing.T) {
	p, fr, fd := setup(t, stubProvider{err: errors.New("xai returned 503: overloaded")})
	out, err := p.Process(context.Background(), 11)
	if err != nil || out.Status != models.StatusFailed {
		t.Fatalf("%v %v", out, err)
	}
	last := fr.calls[len(fr.calls)-1]
	if statuses(fr.calls) != "parsing>failed" || !strings.Contains(last.reason, "503") {
		t.Fatalf("calls: %+v", fr.calls)
	}
	if fr.updated != nil {
		t.Fatal("failed extraction must not overwrite receipt fields")
	}
	if fd.states[len(fd.states)-1] != "failed" {
		t.Fatalf("doc: %v", fd.states)
	}
}

func TestPipelineRedactsStoredError(t *testing.T) {
	p, fr, _ := setup(t, stubProvider{err: errors.New("proxy said: Authorization: Bearer abc123secret")})
	p.Process(context.Background(), 11)
	last := fr.calls[len(fr.calls)-1].reason
	if strings.Contains(last, "abc123secret") || !strings.Contains(last, "[REDACTED]") {
		t.Fatalf("parse_error not redacted: %q", last)
	}
}

func TestPipelineAnthropicStubFails(t *testing.T) {
	p, fr, _ := setup(t, NewAnthropicProvider("m"))
	out, _ := p.Process(context.Background(), 11)
	if out.Status != models.StatusFailed || !strings.Contains(fr.calls[len(fr.calls)-1].reason, "not implemented") {
		t.Fatalf("%+v %+v", out, fr.calls)
	}
}

func TestPipelineNoProviderFails(t *testing.T) {
	p, fr, _ := setup(t, nil)
	p.Provider = nil
	out, _ := p.Process(context.Background(), 11)
	if out.Status != models.StatusFailed || statuses(fr.calls) != "failed" {
		t.Fatalf("%+v %+v", out, fr.calls)
	}
}

func TestPipelineSaveFailureMarksFailed(t *testing.T) {
	p, fr, _ := setup(t, stubProvider{res: result(t, "receipt_ok.json")})
	fr.updateEr = errors.New("db constraint")
	out, _ := p.Process(context.Background(), 11)
	if out.Status != models.StatusFailed || !strings.Contains(fr.calls[len(fr.calls)-1].reason, "db constraint") {
		t.Fatalf("%+v %+v", out, fr.calls)
	}
}

// End to end through the real XAIProvider against the httptest fake.
func TestPipelineWithFakeXAIServer(t *testing.T) {
	_, xp := newFake(t, fixture(t, "receipt_ok.json"), 200)
	p, fr, fd := setup(t, xp)
	out, err := p.Process(context.Background(), 11)
	if err != nil || out.Status != models.StatusParsed || fd.model != "grok-test-model" {
		t.Fatalf("%v %v model=%s calls=%+v", out, err, fd.model, fr.calls)
	}
}

type countProc struct {
	mu  sync.Mutex
	ids []int
}

func (c *countProc) Process(_ context.Context, id int) (*Outcome, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ids = append(c.ids, id)
	return &Outcome{Status: "parsed"}, nil
}

func TestQueue(t *testing.T) {
	cp := &countProc{}
	q := NewQueue(cp, 1, time.Second, nil)
	if !q.Enqueue(1) || q.Enqueue(2) {
		t.Fatal("buffer of 1: first accepted, second rejected (stays pending)")
	}
	ctx, cancel := context.WithCancel(context.Background())
	q.Start(ctx, 1)
	waitFor := func(n int) {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			cp.mu.Lock()
			got := len(cp.ids)
			cp.mu.Unlock()
			if got >= n {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	waitFor(1)
	q.RequeuePending(ctx, func(context.Context, ...string) ([]int, error) { return []int{2}, nil })
	waitFor(2)
	cancel()
	q.Wait()
	if len(cp.ids) != 2 {
		t.Fatalf("processed %v", cp.ids)
	}
}
