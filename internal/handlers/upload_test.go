package handlers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	jpegenc "image/jpeg"
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

// members 1..4 exist
func (f *fakeUploadStore) MemberExists(_ context.Context, id int64) (bool, error) {
	return id >= 1 && id <= 4, nil
}

type fakeQueue struct{ ids []int }

func (q *fakeQueue) Enqueue(id int) bool { q.ids = append(q.ids, id); return true }

type fakeLookup struct{ r *models.Receipt }

func (f fakeLookup) RequestReextract(_ context.Context, id int) error {
	if f.r == nil || f.r.ID != id {
		return repository.ErrNotFound
	}
	switch f.r.Status {
	case models.StatusParsing:
		return repository.ErrReceiptBusy
	case models.StatusReviewed:
		return repository.ErrInvalidTransition
	}
	f.r.Status = models.StatusUploaded
	return nil
}

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
	if d.MimeType != "image/jpeg" || *d.UploadedByID != 3 || !strings.HasPrefix(d.ObjectKey, "receipts/") || !strings.HasSuffix(d.ObjectKey, ".jpg") {
		t.Fatalf("doc: %+v", d)
	}
	if got, _ := blobs.Get(context.Background(), d.ObjectKey); len(got) < 2 || got[0] != 0xFF || got[1] != 0xD8 || len(blobs.M) != 1 {
		t.Fatal("normalized JPEG not stored (or original kept by default)")
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

func TestUploadUnknownMemberRejectedBeforeWrite(t *testing.T) {
	for _, field := range []string{"member_id", "uploaded_by_id"} {
		h, st, _, blobs := newUploadHandler()
		body, ct := multipartBody(t, "image", pngBytes(t, 9), map[string]string{field: "999"})
		rec := do(h.UploadReceiptPhoto, body, ct)
		if rec.Code != 400 || !strings.Contains(rec.Body.String(), "Unknown "+field) {
			t.Fatalf("%s: %d %s", field, rec.Code, rec.Body)
		}
		if len(blobs.M) != 0 || len(st.docs) != 0 {
			t.Fatalf("%s: wrote %d blobs / %d docs before validating", field, len(blobs.M), len(st.docs))
		}
	}
	h, _, _, _ := newUploadHandler()
	body, ct := multipartBody(t, "image", pngBytes(t, 9), map[string]string{"member_id": "1", "uploaded_by_id": "2"})
	if rec := do(h.UploadReceiptPhoto, body, ct); rec.Code != 202 {
		t.Fatalf("known members: %d %s", rec.Code, rec.Body)
	}
}

func TestUploadTruncatedImageRejected(t *testing.T) {
	full := pngBytes(t, 3)
	var jb bytes.Buffer
	if err := jpegenc.Encode(&jb, image.NewRGBA(image.Rect(0, 0, 32, 32)), nil); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"png":  full[:len(full)-20],
		"jpeg": jb.Bytes()[:jb.Len()/2],
	} {
		h, st, _, blobs := newUploadHandler()
		body, ct := multipartBody(t, "image", data, nil)
		rec := do(h.UploadReceiptPhoto, body, ct)
		if rec.Code != 400 {
			t.Fatalf("truncated %s: %d %s", name, rec.Code, rec.Body)
		}
		if len(blobs.M) != 0 || len(st.docs) != 0 {
			t.Fatalf("truncated %s was stored", name)
		}
	}
}

func TestUploadCleansUpBlobOnDBError(t *testing.T) {
	h, st, _, blobs := newUploadHandler()
	st.err = errors.New("db down")
	body, ct := multipartBody(t, "image", pngBytes(t, 4), nil)
	if rec := do(h.UploadReceiptPhoto, body, ct); rec.Code != 500 {
		t.Fatalf("want 500, got %d", rec.Code)
	}
	if len(blobs.M) != 0 {
		t.Fatalf("orphan blob left behind: %d", len(blobs.M))
	}
	// a pre-existing blob (same content, owned by another upload) is never deleted
	sumKey := ""
	st.err = nil
	body, ct = multipartBody(t, "image", pngBytes(t, 4), nil)
	do(h.UploadReceiptPhoto, body, ct)
	for k := range blobs.M {
		sumKey = k
	}
	st.err, st.bySHA = errors.New("db down"), nil
	body, ct = multipartBody(t, "image", pngBytes(t, 4), nil)
	do(h.UploadReceiptPhoto, body, ct)
	if _, ok := blobs.M[sumKey]; !ok {
		t.Fatal("deleted a blob this request did not create")
	}
}

type fakeHEIC struct{ calls int }

func (f *fakeHEIC) Name() string { return "fake" }
func (f *fakeHEIC) Decode(context.Context, []byte) (image.Image, error) {
	f.calls++
	return image.NewRGBA(image.Rect(0, 0, 2400, 3200)), nil
}

// minimal ftyp + an ispe property (2400x3200) so the pre-decode size check passes
var heicBytes = append(append(append([]byte{0, 0, 0, 0x18}, []byte("ftypheic\x00\x00\x00\x00mif1heic")...),
	0, 0, 0, 20, 'i', 's', 'p', 'e', 0, 0, 0, 0, 0, 0, 0x09, 0x60, 0, 0, 0x0c, 0x80), make([]byte, 64)...)

func TestUploadHEIC(t *testing.T) {
	// no decoder on this server: 415 with guidance, nothing stored
	h, st, _, blobs := newUploadHandler()
	body, ct := multipartBody(t, "image", heicBytes, nil)
	rec := do(h.UploadReceiptPhoto, body, ct)
	if rec.Code != 415 || !strings.Contains(rec.Body.String(), "libheif") || len(blobs.M) != 0 || len(st.docs) != 0 {
		t.Fatalf("no decoder: %d %s", rec.Code, rec.Body)
	}
	// with a decoder: converted to one bounded JPEG
	dec := &fakeHEIC{}
	h.HEIC = dec
	body, ct = multipartBody(t, "image", heicBytes, nil)
	rec = do(h.UploadReceiptPhoto, body, ct)
	if rec.Code != 202 || dec.calls != 1 || len(blobs.M) != 1 {
		t.Fatalf("decoder: %d %s", rec.Code, rec.Body)
	}
	d := st.docs[0]
	img, err := jpegenc.Decode(bytes.NewReader(blobs.M[d.ObjectKey]))
	if err != nil || d.MimeType != "image/jpeg" || img.Bounds().Dy() != 2000 || img.Bounds().Dx() != 1500 || d.Width != 1500 {
		t.Fatalf("stored %s %v %v", d.MimeType, err, img.Bounds())
	}
}

// Only the normalized JPEG is stored by default; KEEP_ORIGINAL_UPLOADS also keeps the original.
// Dedupe uses the hash of the ORIGINAL bytes either way.
func TestUploadStoresNormalizedJPEG(t *testing.T) {
	for _, keep := range []bool{false, true} {
		h, st, _, blobs := newUploadHandler()
		h.KeepOriginal = keep
		orig := pngBytes(t, 7)
		body, ct := multipartBody(t, "image", orig, nil)
		if rec := do(h.UploadReceiptPhoto, body, ct); rec.Code != 202 {
			t.Fatalf("keep=%v: %d %s", keep, rec.Code, rec.Body)
		}
		d := st.docs[0]
		sum := sha256.Sum256(orig)
		if !bytes.Equal(d.SHA256, sum[:]) || len(d.JPEGSHA256) != 32 || !strings.HasSuffix(d.ObjectKey, ".jpg") || d.MimeType != "image/jpeg" {
			t.Fatalf("keep=%v doc: %+v", keep, d)
		}
		if b := blobs.M[d.ObjectKey]; len(b) < 3 || b[0] != 0xFF || b[1] != 0xD8 {
			t.Fatalf("keep=%v: stored blob is not a JPEG", keep)
		}
		_, origStored := blobs.M[d.OriginalObjectKey]
		if keep != (d.OriginalObjectKey != "" && origStored && bytes.Equal(blobs.M[d.OriginalObjectKey], orig) && d.OriginalMimeType == "image/png") {
			t.Fatalf("keep=%v: original key=%q stored=%v", keep, d.OriginalObjectKey, origStored)
		}
		if want := map[bool]int{false: 1, true: 2}[keep]; len(blobs.M) != want {
			t.Fatalf("keep=%v: %d blobs, want %d", keep, len(blobs.M), want)
		}
		// same original again: duplicate, nothing new written
		body, ct = multipartBody(t, "image", orig, nil)
		rec := do(h.UploadReceiptPhoto, body, ct)
		if rec.Code != 200 || !decodeResult(t, rec).Duplicate || len(st.docs) != 1 {
			t.Fatalf("keep=%v duplicate: %d", keep, rec.Code)
		}
	}
}
