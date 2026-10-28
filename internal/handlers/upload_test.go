package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"receipts-db/internal/models"
	"receipts-db/internal/repository"
	"receipts-db/internal/storage"
)

type fakeUploadStore struct {
	mu     sync.Mutex
	bySHA  map[string]models.UploadResult
	docs   []*models.SourceDocument
	nextID int
	err    error
}

func (f *fakeUploadStore) FindReceiptBySHA(_ context.Context, sha []byte) (int, int64, string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.bySHA[string(sha)]
	return r.ReceiptID, r.SourceDocumentID, r.Status, ok, nil
}

func (f *fakeUploadStore) CreateUpload(_ context.Context, d *models.SourceDocument, r *models.Receipt) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.nextID++
	d.ID, r.ID = int64(100+f.nextID), f.nextID
	f.docs = append(f.docs, d)
	if f.bySHA == nil {
		f.bySHA = map[string]models.UploadResult{}
	}
	f.bySHA[string(d.SHA256)] = models.UploadResult{ReceiptID: r.ID, SourceDocumentID: d.ID, Status: r.Status}
	if r.Status != models.StatusUploaded || r.Source != models.SourcePhoto {
		return errors.New("upload must create a photo receipt with status uploaded")
	}
	return nil
}

type fakeQueue struct{ ids []int }

func (q *fakeQueue) Enqueue(id int) bool { q.ids = append(q.ids, id); return true }

type fakeLookup struct{ r *models.Receipt }

func (f fakeLookup) GetByID(_ context.Context, id int) (*models.Receipt, error) {
	if f.r == nil || f.r.ID != id {
		return nil, repository.ErrNotFound
	}
	return f.r, nil
}

func pngBytes(t *testing.T, c uint8) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, 4, 4))
	img.Set(1, 1, color.Gray{Y: c})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func multipartBody(t *testing.T, field string, data []byte, extra map[string]string) (*bytes.Buffer, string) {
	t.Helper()
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	for k, v := range extra {
		w.WriteField(k, v)
	}
	if field != "" {
		fw, _ := w.CreateFormFile(field, "receipt.png")
		fw.Write(data)
	}
	w.Close()
	return &b, w.FormDataContentType()
}

func newUploadHandler() (*UploadHandler, *fakeUploadStore, *fakeQueue, *storage.Memory) {
	st, q, blobs := &fakeUploadStore{}, &fakeQueue{}, storage.NewMemory()
	return &UploadHandler{Store: st, Blobs: blobs, Queue: q, MaxBytes: 1 << 20,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}, st, q, blobs
}

func do(h http.HandlerFunc, body io.Reader, ct string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/uploads", body)
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func decodeResult(t *testing.T, rec *httptest.ResponseRecorder) models.UploadResult {
	t.Helper()
	var env struct {
		Data models.UploadResult `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return env.Data
}

func TestUploadAcceptedAndDuplicate(t *testing.T) {
	h, st, q, blobs := newUploadHandler()
	img := pngBytes(t, 7)
	body, ct := multipartBody(t, "image", img, map[string]string{"uploaded_by_id": "3", "member_id": "4"})
	rec := do(h.UploadReceiptPhoto, body, ct)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	res := decodeResult(t, rec)
	if res.ReceiptID != 1 || res.SourceDocumentID != 101 || res.Status != "uploaded" || res.Duplicate {
		t.Fatalf("result: %+v", res)
	}
	if len(q.ids) != 1 || q.ids[0] != 1 {
		t.Fatalf("queue: %v", q.ids)
	}
	d := st.docs[0]
	if d.MimeType != "image/png" || *d.UploadedByID != 3 || !strings.HasPrefix(d.ObjectKey, "receipts/") || !strings.HasSuffix(d.ObjectKey, ".png") {
		t.Fatalf("doc: %+v", d)
	}
	if got, _ := blobs.Get(context.Background(), d.ObjectKey); !bytes.Equal(got, img) {
		t.Fatal("blob not stored")
	}
	body, ct = multipartBody(t, "image", img, nil)
	rec = do(h.UploadReceiptPhoto, body, ct)
	if rec.Code != http.StatusOK || !decodeResult(t, rec).Duplicate || len(q.ids) != 1 {
		t.Fatalf("duplicate: %d %s queue=%v", rec.Code, rec.Body.String(), q.ids)
	}
}

func TestUploadRejects(t *testing.T) {
	h, _, q, _ := newUploadHandler()
	cases := []struct {
		name string
		body func() (io.Reader, string)
		want int
	}{
		{"not multipart", func() (io.Reader, string) { return strings.NewReader("{}"), "application/json" }, 400},
		{"missing file", func() (io.Reader, string) { b, ct := multipartBody(t, "", nil, nil); return b, ct }, 400},
		{"wrong field", func() (io.Reader, string) { b, ct := multipartBody(t, "photo", pngBytes(t, 1), nil); return b, ct }, 400},
		{"gif", func() (io.Reader, string) {
			b, ct := multipartBody(t, "image", []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00;"), nil)
			return b, ct
		}, 415},
		{"text", func() (io.Reader, string) { b, ct := multipartBody(t, "image", []byte("hello"), nil); return b, ct }, 415},
		{"too large", func() (io.Reader, string) {
			big := append(pngBytes(t, 2), make([]byte, 2<<20)...)
			b, ct := multipartBody(t, "image", big, nil)
			return b, ct
		}, 413},
		{"bad member", func() (io.Reader, string) {
			b, ct := multipartBody(t, "image", pngBytes(t, 3), map[string]string{"member_id": "abc"})
			return b, ct
		}, 400},
	}
	for _, c := range cases {
		b, ct := c.body()
		if rec := do(h.UploadReceiptPhoto, b, ct); rec.Code != c.want {
			t.Errorf("%s: got %d want %d (%s)", c.name, rec.Code, c.want, rec.Body.String())
		}
	}
	if len(q.ids) != 0 {
		t.Fatalf("nothing should be queued: %v", q.ids)
	}
}

func TestUploadStoreFailure(t *testing.T) {
	h, st, q, _ := newUploadHandler()
	st.err = errors.New("db down")
	body, ct := multipartBody(t, "image", pngBytes(t, 9), nil)
	if rec := do(h.UploadReceiptPhoto, body, ct); rec.Code != 500 || len(q.ids) != 0 {
		t.Fatalf("%d %v", rec.Code, q.ids)
	}
}

func TestReextract(t *testing.T) {
	doc := int64(5)
	h, _, q, _ := newUploadHandler()
	h.Receipts = fakeLookup{r: &models.Receipt{ID: 9, SourceDocumentID: &doc, Status: "failed"}}
	r := chi.NewRouter()
	r.Post("/api/v1/receipts/{id}/extract", h.ReextractReceipt)
	call := func(path string) int {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
		return rec.Code
	}
	if c := call("/api/v1/receipts/9/extract"); c != 202 || len(q.ids) != 1 || q.ids[0] != 9 {
		t.Fatalf("202 expected: %d %v", c, q.ids)
	}
	if c := call("/api/v1/receipts/8/extract"); c != 404 {
		t.Fatalf("404 expected: %d", c)
	}
	if c := call("/api/v1/receipts/x/extract"); c != 400 {
		t.Fatalf("400 expected: %d", c)
	}
	h.Receipts = fakeLookup{r: &models.Receipt{ID: 9}}
	if c := call("/api/v1/receipts/9/extract"); c != 400 {
		t.Fatalf("manual receipt: %d", c)
	}
	for _, st := range []string{models.StatusParsing, models.StatusReviewed} {
		h.Receipts = fakeLookup{r: &models.Receipt{ID: 9, SourceDocumentID: &doc, Status: st}}
		if c := call("/api/v1/receipts/9/extract"); c != 409 {
			t.Fatalf("%s: want 409, got %d", st, c)
		}
	}
	h.Receipts = fakeLookup{r: &models.Receipt{ID: 9, SourceDocumentID: &doc}}
	h.Queue = nil
	if c := call("/api/v1/receipts/9/extract"); c != 503 {
		t.Fatalf("disabled: %d", c)
	}
}
