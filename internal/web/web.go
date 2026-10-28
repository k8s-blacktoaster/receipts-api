// Package web is the household's mobile web UI: server-rendered html/template pages plus a
// little vanilla JS, embedded in the binary with go:embed. It is VPN-only by design: there
// is no login, only a member picker remembered in a cookie (see README "Web UI").
package web

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"receipts-db/internal/httpguard"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"receipts-db/internal/handlers"
	"receipts-db/internal/models"
	"receipts-db/internal/repository"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// MemberCookie remembers the selected household member (not an auth token).
const MemberCookie = "receipts_member"

// Receipts is the receipt persistence the UI needs.
type Receipts interface {
	List(ctx context.Context, limit, offset int) ([]*models.Receipt, error)
	GetByID(ctx context.Context, id int) (*models.Receipt, error)
	Update(ctx context.Context, r *models.Receipt) error
	SetStatus(ctx context.Context, id int, status, parseError string, reviewerID *int64) error
	RequestReextract(ctx context.Context, id int) error
}

type Members interface {
	List(ctx context.Context) ([]models.Member, error)
}

type Merchants interface {
	List(ctx context.Context) ([]models.Merchant, error)
	Rename(ctx context.Context, id int64, newName string) (*models.Merchant, error)
}

type Docs interface {
	GetByID(ctx context.Context, id int64) (*models.SourceDocument, error)
}

type Blobs interface {
	Get(ctx context.Context, key string) ([]byte, error)
}

// Ingester stores an uploaded photo (handlers.UploadHandler).
type Ingester interface {
	Ingest(ctx context.Context, data []byte, uploader, member *int64) (*models.UploadResult, int, string)
}

// UI serves the web pages.
type UI struct {
	Receipts  Receipts
	Members   Members
	Merchants Merchants
	Docs      Docs
	Blobs     Blobs
	Uploads   Ingester
	Queue     handlers.Enqueuer // nil = extraction disabled
	MaxBytes  int64
	Logger    *slog.Logger

	pages map[string]*template.Template
}

var funcs = template.FuncMap{
	"money": func(c int64) string {
		sign := ""
		if c < 0 {
			sign, c = "-", -c
		}
		return fmt.Sprintf("%s%d.%02d", sign, c/100, c%100)
	},
	"qty":         func(q float64) string { return strconv.FormatFloat(q, 'f', -1, 64) },
	"statusLabel": func(s string) string { return strings.ReplaceAll(s, "_", " ") },
	"pending":     func(s string) bool { return models.EditLocked(s) },
	"editable":    func(s string) bool { return !models.EditLocked(s) },
	"approvable":  func(s string) bool { return models.UserCanTransition(s, models.StatusReviewed) },
	"reextractable": func(r *models.Receipt) bool {
		return r.SourceDocumentID != nil && r.Status != models.StatusParsing && r.Status != models.StatusReviewed
	},
}

// New parses the embedded templates.
func New(u UI) (*UI, error) {
	if u.Logger == nil {
		u.Logger = slog.Default()
	}
	if u.MaxBytes <= 0 {
		u.MaxBytes = handlers.DefaultMaxUploadBytes
	}
	u.pages = map[string]*template.Template{}
	for _, p := range []string{"list", "detail", "merchants", "member"} {
		t, err := template.New("layout.html").Funcs(funcs).ParseFS(templateFS, "templates/layout.html", "templates/"+p+".html")
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", p, err)
		}
		u.pages[p] = t
	}
	return &u, nil
}

// Register mounts the UI routes on r (the JSON API stays under /api/v1).
func (u *UI) Register(r chi.Router) {
	static, _ := fs.Sub(staticFS, "static")
	r.Handle("/static/*", http.StripPrefix("/static/", cacheStatic(http.FileServer(http.FS(static)))))
	r.Get("/", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/receipts", http.StatusFound) })
	r.Group(func(r chi.Router) {
		r.Use(httpguard.SameOrigin)
		r.Get("/receipts", u.list)
		r.Post("/upload", u.upload)
		r.Get("/receipts/{id}", u.detail)
		r.Post("/receipts/{id}", u.save)
		r.Post("/receipts/{id}/approve", u.approve)
		r.Post("/receipts/{id}/extract", u.reextract)
		r.Get("/receipts/{id}/image", u.image)
		r.Get("/member", u.memberPage)
		r.Post("/member", u.setMember)
		r.Get("/merchants", u.merchants)
		r.Post("/merchants/{id}/rename", u.rename)
	})
}

func cacheStatic(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		h.ServeHTTP(w, r)
	})
}

type page struct {
	Title     string
	Member    *models.Member
	Members   []models.Member
	Msg, Err  string
	Extract   bool
	Receipts  []*models.Receipt
	Receipt   *models.Receipt
	Merchants []models.Merchant
	Next      string
}

func (u *UI) render(w http.ResponseWriter, r *http.Request, code int, name string, p *page) {
	if p.Member == nil {
		p.Member = u.currentMember(r)
	}
	p.Extract = u.Queue != nil
	if p.Msg == "" {
		p.Msg = r.URL.Query().Get("msg")
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.WriteHeader(code)
	if err := u.pages[name].ExecuteTemplate(w, "layout.html", p); err != nil {
		u.Logger.Error("render failed", "page", name, "error", err)
	}
}

func (u *UI) currentMember(r *http.Request) *models.Member {
	c, err := r.Cookie(MemberCookie)
	if err != nil {
		return nil
	}
	id, err := strconv.ParseInt(c.Value, 10, 64)
	if err != nil {
		return nil
	}
	ms, err := u.Members.List(r.Context())
	if err != nil {
		return nil
	}
	for i := range ms {
		if ms[i].ID == id {
			return &ms[i]
		}
	}
	return nil
}

func memberID(m *models.Member) *int64 {
	if m == nil {
		return nil
	}
	id := m.ID
	return &id
}

func redirectMsg(w http.ResponseWriter, r *http.Request, path, msg string) {
	http.Redirect(w, r, path+"?msg="+url.QueryEscape(msg), http.StatusSeeOther)
}

func (u *UI) list(w http.ResponseWriter, r *http.Request) {
	rs, err := u.Receipts.List(r.Context(), 100, 0)
	if err != nil {
		u.Logger.Error("list receipts", "error", err)
		u.render(w, r, http.StatusInternalServerError, "list", &page{Title: "Receipts", Err: "Could not load receipts"})
		return
	}
	u.render(w, r, http.StatusOK, "list", &page{Title: "Receipts", Receipts: rs})
}

func (u *UI) listWithError(w http.ResponseWriter, r *http.Request, code int, msg string) {
	rs, _ := u.Receipts.List(r.Context(), 100, 0)
	u.render(w, r, code, "list", &page{Title: "Receipts", Receipts: rs, Err: msg})
}

func (u *UI) upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, u.MaxBytes+1<<20)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			u.listWithError(w, r, http.StatusRequestEntityTooLarge, "Photo too large")
			return
		}
		u.listWithError(w, r, http.StatusBadRequest, "Choose a photo first")
		return
	}
	defer r.MultipartForm.RemoveAll()
	f, _, err := r.FormFile("image")
	if err != nil {
		u.listWithError(w, r, http.StatusBadRequest, "Choose a photo first")
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, u.MaxBytes+1))
	if err != nil || len(data) == 0 {
		u.listWithError(w, r, http.StatusBadRequest, "Could not read the photo")
		return
	}
	if int64(len(data)) > u.MaxBytes {
		u.listWithError(w, r, http.StatusRequestEntityTooLarge, "Photo too large")
		return
	}
	m := memberID(u.currentMember(r))
	res, code, msg := u.Uploads.Ingest(r.Context(), data, m, m)
	if code >= 400 {
		u.listWithError(w, r, code, msg)
		return
	}
	redirectMsg(w, r, "/receipts/"+strconv.Itoa(res.ReceiptID), msg)
}

func (u *UI) load(w http.ResponseWriter, r *http.Request) (*models.Receipt, bool) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || id <= 0 {
		u.listWithError(w, r, http.StatusBadRequest, "Invalid receipt id")
		return nil, false
	}
	rc, err := u.Receipts.GetByID(r.Context(), id)
	if errors.Is(err, repository.ErrNotFound) {
		u.listWithError(w, r, http.StatusNotFound, "Receipt not found")
		return nil, false
	}
	if err != nil {
		u.Logger.Error("load receipt", "error", err)
		u.listWithError(w, r, http.StatusInternalServerError, "Could not load receipt")
		return nil, false
	}
	return rc, true
}

func (u *UI) detail(w http.ResponseWriter, r *http.Request) {
	rc, ok := u.load(w, r)
	if !ok {
		return
	}
	u.render(w, r, http.StatusOK, "detail", &page{Title: "Receipt " + strconv.Itoa(rc.ID), Receipt: rc})
}

// detailError re-renders the detail page (fresh from the DB) with an error and status code.
func (u *UI) detailError(w http.ResponseWriter, r *http.Request, code int, msg string) {
	rc, ok := u.load(w, r)
	if !ok {
		return
	}
	u.render(w, r, code, "detail", &page{Title: "Receipt " + strconv.Itoa(rc.ID), Receipt: rc, Err: msg})
}

// busyMsg explains why edits are refused while extraction is pending.
const busyMsg = "This receipt is queued for or being extracted. Edits are locked until extraction finishes, so they can't be overwritten; refresh in a moment."

func (u *UI) save(w http.ResponseWriter, r *http.Request) {
	rc, ok := u.load(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		u.detailError(w, r, http.StatusBadRequest, "Invalid form")
		return
	}
	if err := applyForm(rc, r.PostForm); err != nil {
		u.detailError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	rc.Status = "" // keep the current status; approving is a separate action
	if err := rc.Validate(); err != nil {
		u.detailError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	if err := u.Receipts.Update(r.Context(), rc); err != nil {
		switch {
		case errors.Is(err, repository.ErrReceiptBusy):
			u.detailError(w, r, http.StatusConflict, busyMsg)
		case errors.Is(err, repository.ErrNotFound):
			u.listWithError(w, r, http.StatusNotFound, "Receipt not found")
		default:
			u.Logger.Error("save receipt", "error", err)
			u.detailError(w, r, http.StatusInternalServerError, "Could not save the receipt")
		}
		return
	}
	if r.PostForm.Get("approve") == "1" {
		u.approve(w, r)
		return
	}
	redirectMsg(w, r, "/receipts/"+strconv.Itoa(rc.ID), "Saved")
}

func (u *UI) approve(w http.ResponseWriter, r *http.Request) {
	rc, ok := u.load(w, r)
	if !ok {
		return
	}
	if models.EditLocked(rc.Status) {
		u.detailError(w, r, http.StatusConflict, busyMsg)
		return
	}
	if rc.Status == models.StatusReviewed {
		redirectMsg(w, r, "/receipts/"+strconv.Itoa(rc.ID), "Already reviewed")
		return
	}
	if err := rc.ReadyForReview(); err != nil {
		u.detailError(w, r, http.StatusBadRequest, "Can't approve yet: "+err.Error()+" (edit and save first)")
		return
	}
	err := u.Receipts.SetStatus(r.Context(), rc.ID, models.StatusReviewed, "", memberID(u.currentMember(r)))
	switch {
	case err == nil:
		redirectMsg(w, r, "/receipts/"+strconv.Itoa(rc.ID), "Approved: marked reviewed")
	case errors.Is(err, repository.ErrInvalidTransition):
		u.detailError(w, r, http.StatusConflict, "Can't approve from status "+rc.Status+" (it changed meanwhile?); refresh and try again")
	case errors.Is(err, repository.ErrIncomplete):
		u.detailError(w, r, http.StatusBadRequest, "Can't approve yet: the receipt needs a store name and a date (edit and save first)")
	default:
		u.Logger.Error("approve", "error", err)
		u.detailError(w, r, http.StatusInternalServerError, "Could not approve")
	}
}

func (u *UI) reextract(w http.ResponseWriter, r *http.Request) {
	rc, ok := u.load(w, r)
	if !ok {
		return
	}
	if u.Queue == nil {
		u.detailError(w, r, http.StatusServiceUnavailable, "Extraction is turned off on this server (EXTRACTION_PROVIDER=none)")
		return
	}
	switch rc.Status {
	case models.StatusParsing:
		u.detailError(w, r, http.StatusConflict, "Extraction is already running for this receipt")
		return
	case models.StatusReviewed:
		u.detailError(w, r, http.StatusConflict, "This receipt is reviewed. Re-extraction would overwrite your review, so it is not allowed (v1 has no force option)")
		return
	}
	if err := u.Receipts.RequestReextract(r.Context(), rc.ID); err != nil {
		code, msg := handlers.ReextractError(err)
		u.detailError(w, r, code, msg)
		return
	}
	u.Queue.Enqueue(rc.ID)
	redirectMsg(w, r, "/receipts/"+strconv.Itoa(rc.ID), "Re-extraction queued; edits are locked until it finishes")
}

func (u *UI) image(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	rc, err := u.Receipts.GetByID(r.Context(), id)
	if err != nil || rc.SourceDocumentID == nil {
		http.NotFound(w, r)
		return
	}
	doc, err := u.Docs.GetByID(r.Context(), *rc.SourceDocumentID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	data, err := u.Blobs.Get(r.Context(), doc.ObjectKey)
	if err != nil {
		u.Logger.Warn("image missing from blob store", "receipt_id", id, "key", doc.ObjectKey)
		http.NotFound(w, r)
		return
	}
	switch doc.MimeType {
	case "image/jpeg", "image/png":
		w.Header().Set("Content-Type", doc.MimeType)
	default:
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=86400") // content-addressed, never changes
	_, _ = w.Write(data)
}

func (u *UI) memberPage(w http.ResponseWriter, r *http.Request) {
	ms, err := u.Members.List(r.Context())
	if err != nil {
		u.render(w, r, http.StatusInternalServerError, "member", &page{Title: "Who's this?", Err: "Could not load members"})
		return
	}
	u.render(w, r, http.StatusOK, "member", &page{Title: "Who's this?", Members: ms, Next: safeNext(r.URL.Query().Get("next"))})
}

func (u *UI) setMember(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	id, err := strconv.ParseInt(r.PostForm.Get("member_id"), 10, 64)
	ms, lerr := u.Members.List(r.Context())
	found := false
	for _, m := range ms {
		if err == nil && m.ID == id {
			found = true
		}
	}
	if err != nil || lerr != nil || !found {
		u.render(w, r, http.StatusBadRequest, "member", &page{Title: "Who's this?", Members: ms, Err: "Pick one of the listed members"})
		return
	}
	http.SetCookie(w, &http.Cookie{Name: MemberCookie, Value: strconv.FormatInt(id, 10), Path: "/",
		MaxAge: int((365 * 24 * time.Hour).Seconds()), HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil})
	redirectMsg(w, r, safeNext(r.PostForm.Get("next")), "Hi!")
}

// safeNext only allows local paths for post-picker redirects.
// safeNext allows only a local path: one leading '/', no "//" anywhere, no backslashes, no
// control characters or spaces, and no scheme/host once parsed. Anything else -> /receipts.
func safeNext(n string) string {
	const fallback = "/receipts"
	if len(n) == 0 || len(n) > 2048 || n[0] != '/' || strings.Contains(n, "//") || strings.Contains(n, "\\") {
		return fallback
	}
	for _, c := range n {
		if c < 0x21 || c == 0x7f || (c >= 0x80 && c <= 0x9f) {
			return fallback
		}
	}
	if u, err := url.Parse(n); err != nil || u.Scheme != "" || u.Host != "" || u.User != nil {
		return fallback
	}
	return n
}

func (u *UI) merchants(w http.ResponseWriter, r *http.Request) {
	ms, err := u.Merchants.List(r.Context())
	if err != nil {
		u.render(w, r, http.StatusInternalServerError, "merchants", &page{Title: "Merchants", Err: "Could not load merchants"})
		return
	}
	u.render(w, r, http.StatusOK, "merchants", &page{Title: "Merchants", Merchants: ms})
}

func (u *UI) rename(w http.ResponseWriter, r *http.Request) {
	fail := func(code int, msg string) {
		ms, _ := u.Merchants.List(r.Context())
		u.render(w, r, code, "merchants", &page{Title: "Merchants", Merchants: ms, Err: msg})
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		fail(http.StatusBadRequest, "Invalid merchant id")
		return
	}
	_ = r.ParseForm()
	m, err := u.Merchants.Rename(r.Context(), id, r.PostForm.Get("name"))
	var conflict *repository.MerchantConflict
	switch {
	case err == nil:
		redirectMsg(w, r, "/merchants", "Renamed to "+m.Name)
	case errors.As(err, &conflict):
		fail(http.StatusConflict, fmt.Sprintf("A merchant named %q already exists (#%d, %d receipts). Rename refused: merging merchants is not supported yet.",
			conflict.Existing.Name, conflict.Existing.ID, conflict.Existing.ReceiptCount))
	case errors.Is(err, repository.ErrInvalidMerchantName):
		fail(http.StatusBadRequest, "The name must contain a letter or digit")
	case errors.Is(err, repository.ErrNotFound):
		fail(http.StatusNotFound, "Merchant not found")
	default:
		u.Logger.Error("rename merchant", "error", err)
		fail(http.StatusInternalServerError, "Could not rename")
	}
}
