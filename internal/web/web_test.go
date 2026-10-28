package web

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"

	"receipts-db/internal/models"
	"receipts-db/internal/repository"
)

type fakeReceipts struct {
	mu        sync.Mutex
	r         map[int]*models.Receipt
	reviewer  *int64
	reextract int
}

func (f *fakeReceipts) List(context.Context, int, int) ([]*models.Receipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*models.Receipt
	for _, r := range f.r {
		cp := *r
		out = append(out, &cp)
	}
	return out, nil
}
func (f *fakeReceipts) GetByID(_ context.Context, id int) (*models.Receipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.r[id]
	if !ok {
		return nil, repository.ErrNotFound
	}
	cp := *r
	cp.Items = append([]models.ReceiptItem(nil), r.Items...)
	return &cp, nil
}
func (f *fakeReceipts) Update(_ context.Context, r *models.Receipt) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cur := f.r[r.ID]
	if models.EditLocked(cur.Status) {
		return repository.ErrReceiptBusy
	}
	cp := *r
	cp.Status = cur.Status
	f.r[r.ID] = &cp
	return nil
}
func (f *fakeReceipts) SetStatus(_ context.Context, id int, st, _ string, reviewer *int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !models.CanTransition(f.r[id].Status, st) {
		return repository.ErrInvalidTransition
	}
	f.r[id].Status, f.reviewer = st, reviewer
	return nil
}
func (f *fakeReceipts) RequestReextract(_ context.Context, id int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reextract++
	f.r[id].Status = models.StatusUploaded
	return nil
}

type fakeMembers struct{}

func (fakeMembers) List(context.Context) ([]models.Member, error) {
	return []models.Member{{ID: 1, DisplayName: "Jack"}, {ID: 2, DisplayName: "Sam"}}, nil
}

type fakeMerchants struct{ m []models.Merchant }

func (f *fakeMerchants) List(context.Context) ([]models.Merchant, error) { return f.m, nil }
func (f *fakeMerchants) Rename(_ context.Context, id int64, name string) (*models.Merchant, error) {
	key := models.NormalizeMerchantName(name)
	if key == "" {
		return nil, repository.ErrInvalidMerchantName
	}
	for _, m := range f.m {
		if m.Name == key && m.ID != id {
			return nil, &repository.MerchantConflict{Existing: m}
		}
	}
	for i := range f.m {
		if f.m[i].ID == id {
			f.m[i].Name = key
			return &f.m[i], nil
		}
	}
	return nil, repository.ErrNotFound
}

type fakeDocs struct{}

func (fakeDocs) GetByID(_ context.Context, id int64) (*models.SourceDocument, error) {
	return &models.SourceDocument{ID: id, ObjectKey: "receipts/x.png", MimeType: "image/png"}, nil
}

type fakeBlobs struct{}

func (fakeBlobs) Get(_ context.Context, k string) ([]byte, error) {
	if k == "receipts/x.png" {
		return []byte("\x89PNG fake"), nil
	}
	return nil, fmt.Errorf("missing")
}

type fakeIngest struct {
	got      []byte
	uploader *int64
	code     int
	msg      string
}

func (f *fakeIngest) Ingest(_ context.Context, data []byte, uploader, _ *int64) (*models.UploadResult, int, string) {
	f.got, f.uploader = data, uploader
	if f.code >= 400 {
		return nil, f.code, f.msg
	}
	return &models.UploadResult{ReceiptID: 7, Status: "uploaded"}, 202, "Upload accepted; extraction queued"
}

type fakeQueue struct{ ids []int }

func (q *fakeQueue) Enqueue(id int) bool { q.ids = append(q.ids, id); return true }

type env struct {
	ui  *UI
	rs  *fakeReceipts
	ms  *fakeMerchants
	in  *fakeIngest
	q   *fakeQueue
	srv http.Handler
}

func newEnv(t *testing.T) *env {
	t.Helper()
	doc := int64(3)
	rs := &fakeReceipts{r: map[int]*models.Receipt{
		7: {ID: 7, StoreName: "Sprouts", Date: "2026-10-01", Source: "photo", Status: models.StatusNeedsReview, SourceDocumentID: &doc,
			TotalCents: 1963, ParseError: "total mismatch", Items: []models.ReceiptItem{{ID: 1, Name: "Milk", Quantity: 1, UnitPriceCents: 699, LineTotalCents: 699, SKU: "SPR-1"}}},
		8:  {ID: 8, Source: "photo", Status: models.StatusParsing, SourceDocumentID: &doc},
		9:  {ID: 9, StoreName: "Costco", Date: "2026-10-02", Source: "photo", Status: models.StatusReviewed, SourceDocumentID: &doc},
		10: {ID: 10, Source: "photo", Status: models.StatusUploaded, SourceDocumentID: &doc},
	}}
	e := &env{rs: rs, ms: &fakeMerchants{m: []models.Merchant{{ID: 1, Name: "sprouts"}, {ID: 2, Name: "trader_joes", ReceiptCount: 4}}},
		in: &fakeIngest{}, q: &fakeQueue{}}
	ui, err := New(UI{Receipts: rs, Members: fakeMembers{}, Merchants: e.ms, Docs: fakeDocs{}, Blobs: fakeBlobs{}, Uploads: e.in, Queue: e.q})
	if err != nil {
		t.Fatal(err)
	}
	e.ui = ui
	r := chi.NewRouter()
	ui.Register(r)
	e.srv = r
	return e
}

func (e *env) do(method, path string, form url.Values, cookie bool) *httptest.ResponseRecorder {
	var req *http.Request
	if form != nil {
		req = httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	if cookie {
		req.AddCookie(&http.Cookie{Name: MemberCookie, Value: "2"})
	}
	rec := httptest.NewRecorder()
	e.srv.ServeHTTP(rec, req)
	return rec
}

func TestRenderPages(t *testing.T) {
	e := newEnv(t)
	for path, want := range map[string][]string{
		"/receipts":      {"Take photo", `capture="environment"`, `accept="image/*,.heic,.heif"`, "s-needs_review", "Sprouts", "$19.63", "Pick member"},
		"/receipts/7":    {`src="/receipts/7/image"`, `value="Milk"`, "Needs review: total mismatch", "Save &amp; approve", "Re-extract"},
		"/receipts/8":    {`http-equiv="refresh"`, "Editing is locked", "<fieldset disabled>"},
		"/receipts/9":    {"Re-extract is disabled for reviewed receipts"},
		"/merchants":     {`value="trader_joes"`, "4 receipts"},
		"/member":        {"Jack", "Sam", "only reachable on the home VPN"},
		"/static/app.js": {"data-autosubmit"},
	} {
		rec := e.do(http.MethodGet, path, nil, false)
		if rec.Code != 200 {
			t.Fatalf("%s: %d", path, rec.Code)
		}
		for _, w := range want {
			if !strings.Contains(rec.Body.String(), w) {
				t.Errorf("%s missing %q", path, w)
			}
		}
	}
	if b := e.do(http.MethodGet, "/receipts/9", nil, false).Body.String(); strings.Contains(b, `action="/receipts/9/extract"`) {
		t.Error("reviewed receipt offers re-extract")
	}
	if rec := e.do(http.MethodGet, "/receipts", nil, true); !strings.Contains(rec.Body.String(), "Sam") {
		t.Error("member cookie not shown")
	}
	if rec := e.do(http.MethodGet, "/receipts/404", nil, false); rec.Code != 404 {
		t.Errorf("missing receipt: %d", rec.Code)
	}
	img := e.do(http.MethodGet, "/receipts/7/image", nil, false)
	if img.Code != 200 || img.Header().Get("Content-Type") != "image/png" || img.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("image: %d %v", img.Code, img.Header())
	}
}

func TestUploadForm(t *testing.T) {
	e := newEnv(t)
	post := func(cookie bool) *httptest.ResponseRecorder {
		var b bytes.Buffer
		mw := multipart.NewWriter(&b)
		fw, _ := mw.CreateFormFile("image", "r.jpg")
		_, _ = fw.Write([]byte("jpegbytes"))
		_ = mw.Close()
		req := httptest.NewRequest(http.MethodPost, "/upload", &b)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		if cookie {
			req.AddCookie(&http.Cookie{Name: MemberCookie, Value: "2"})
		}
		rec := httptest.NewRecorder()
		e.srv.ServeHTTP(rec, req)
		return rec
	}
	rec := post(true)
	if rec.Code != 303 || !strings.HasPrefix(rec.Header().Get("Location"), "/receipts/7?msg=") || string(e.in.got) != "jpegbytes" || e.in.uploader == nil || *e.in.uploader != 2 {
		t.Fatalf("upload: %d %v uploader=%v", rec.Code, rec.Header(), e.in.uploader)
	}
	e.in.code, e.in.msg = 415, "HEIC/HEIF photos are not supported"
	rec = post(false)
	if rec.Code != 415 || !strings.Contains(rec.Body.String(), "HEIC/HEIF") {
		t.Fatalf("415: %d", rec.Code)
	}
	req := httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader("x"))
	rec = httptest.NewRecorder()
	e.srv.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatalf("no file: %d", rec.Code)
	}
}

func TestEditAndApprove(t *testing.T) {
	e := newEnv(t)
	form := url.Values{"store_name": {"Sprouts"}, "date": {"2026-10-01"}, "subtotal": {"21.65"}, "tax": {"0.98"},
		"total": {"19.63"}, "savings": {"3"},
		"item_name": {"Organic milk", "Bananas"}, "item_qty": {"2", ""}, "item_price": {"6.99", "0.25"},
		"item_total": {"", ""}, "item_discount": {"1", ""}}
	rec := e.do(http.MethodPost, "/receipts/7", form, true)
	if rec.Code != 303 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	got := e.rs.r[7]
	if got.SubtotalCents != 2165 || got.SavingsCents != 300 || len(got.Items) != 2 || got.Items[0].Name != "Organic milk" ||
		got.Items[0].LineTotalCents != 1398 || got.Items[0].DiscountCents != 100 || got.Items[0].SKU != "SPR-1" ||
		got.Items[1].Quantity != 1 || got.Items[1].LineTotalCents != 25 || got.Status != models.StatusNeedsReview {
		t.Fatalf("saved: %+v", got)
	}
	// removing a row by clearing its name
	form.Set("item_name", "")
	form["item_name"] = []string{"", "Bananas"}
	e.do(http.MethodPost, "/receipts/7", form, true)
	if len(e.rs.r[7].Items) != 1 || e.rs.r[7].Items[0].Name != "Bananas" {
		t.Fatalf("remove row: %+v", e.rs.r[7].Items)
	}
	// bad money
	form.Set("tax", "abc")
	if rec := e.do(http.MethodPost, "/receipts/7", form, true); rec.Code != 400 || !strings.Contains(rec.Body.String(), "Tax") {
		t.Fatalf("bad money: %d", rec.Code)
	}
	form.Set("tax", "0.98")
	// save & approve
	form.Set("approve", "1")
	rec = e.do(http.MethodPost, "/receipts/7", form, true)
	if rec.Code != 303 || e.rs.r[7].Status != models.StatusReviewed || e.rs.reviewer == nil || *e.rs.reviewer != 2 {
		t.Fatalf("approve: %d %s reviewer=%v", rec.Code, e.rs.r[7].Status, e.rs.reviewer)
	}
	if loc := rec.Header().Get("Location"); !strings.Contains(loc, "Approved") {
		t.Fatalf("approve redirect %s", loc)
	}
}

// Every guard is surfaced as a clear page with the real status code.
func TestGuardsSurfaced(t *testing.T) {
	e := newEnv(t)
	edit := url.Values{"store_name": {"X"}, "date": {"2026-10-01"}}
	for _, id := range []string{"8", "10"} { // parsing, uploaded
		rec := e.do(http.MethodPost, "/receipts/"+id, edit, false)
		if rec.Code != 409 || !strings.Contains(rec.Body.String(), "Edits are locked") {
			t.Fatalf("edit %s: %d", id, rec.Code)
		}
		if rec := e.do(http.MethodPost, "/receipts/"+id+"/approve", nil, false); rec.Code != 409 {
			t.Fatalf("approve %s: %d", id, rec.Code)
		}
	}
	if rec := e.do(http.MethodPost, "/receipts/8/extract", nil, false); rec.Code != 409 || !strings.Contains(rec.Body.String(), "already running") {
		t.Fatalf("extract parsing: %d", rec.Code)
	}
	if rec := e.do(http.MethodPost, "/receipts/9/extract", nil, false); rec.Code != 409 || !strings.Contains(rec.Body.String(), "would overwrite your review") {
		t.Fatalf("extract reviewed: %d", rec.Code)
	}
	if rec := e.do(http.MethodPost, "/receipts/7/extract", nil, false); rec.Code != 303 || len(e.q.ids) != 1 || e.rs.r[7].Status != models.StatusUploaded {
		t.Fatalf("extract ok: %d %v", rec.Code, e.q.ids)
	}
	e.ui.Queue = nil
	if rec := e.do(http.MethodPost, "/receipts/7/extract", nil, false); rec.Code != 503 || !strings.Contains(rec.Body.String(), "turned off") {
		t.Fatalf("extract disabled: %d", rec.Code)
	}
	// cross-site form post refused
	req := httptest.NewRequest(http.MethodPost, "/receipts/7/approve", nil)
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	e.srv.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("cross-origin: %d", rec.Code)
	}
}

func TestMemberPicker(t *testing.T) {
	e := newEnv(t)
	rec := e.do(http.MethodPost, "/member", url.Values{"member_id": {"1"}, "next": {"/receipts/7"}}, false)
	c := rec.Result().Cookies()
	if rec.Code != 303 || len(c) != 1 || c[0].Value != "1" || !c[0].HttpOnly || c[0].SameSite != http.SameSiteLaxMode ||
		!strings.HasPrefix(rec.Header().Get("Location"), "/receipts/7") {
		t.Fatalf("pick: %d %v %v", rec.Code, c, rec.Header())
	}
	if rec := e.do(http.MethodPost, "/member", url.Values{"member_id": {"99"}}, false); rec.Code != 400 {
		t.Fatalf("unknown member: %d", rec.Code)
	}
	rec = e.do(http.MethodPost, "/member", url.Values{"member_id": {"1"}, "next": {"//evil.example"}}, false)
	if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, "/receipts?") {
		t.Fatalf("open redirect: %s", loc)
	}
}

func TestMerchantRename(t *testing.T) {
	e := newEnv(t)
	rec := e.do(http.MethodPost, "/merchants/1/rename", url.Values{"name": {"Sprouts Farmers Market"}}, false)
	if rec.Code != 303 || e.ms.m[0].Name != "sprouts_farmers_market" {
		t.Fatalf("rename: %d %+v", rec.Code, e.ms.m)
	}
	rec = e.do(http.MethodPost, "/merchants/1/rename", url.Values{"name": {"Trader Joe's"}}, false)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "already exists (#2, 4 receipts)") || e.ms.m[0].Name != "sprouts_farmers_market" {
		t.Fatalf("collision: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(http.MethodPost, "/merchants/1/rename", url.Values{"name": {"!!"}}, false); rec.Code != 400 {
		t.Fatalf("empty: %d", rec.Code)
	}
}

func TestParseCents(t *testing.T) {
	for in, want := range map[string]int64{"": 0, "12.34": 1234, "$3": 300, "-1.5": -150, "0.07": 7, ".5": 50} {
		if got, err := parseCents(in); err != nil || got != want {
			t.Errorf("%q: %d %v", in, got, err)
		}
	}
	for _, bad := range []string{"1.234", "abc", "1e5", "--1"} {
		if _, err := parseCents(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
