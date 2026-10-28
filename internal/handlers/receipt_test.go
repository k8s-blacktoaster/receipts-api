package handlers

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"receipts-db/internal/models"
	"receipts-db/internal/repository"
)

// statusRepo mimics the repository's Update status rule on an in-memory status.
type statusRepo struct {
	repository.ReceiptRepository
	status  string
	updates int
}

func (s *statusRepo) Update(_ context.Context, r *models.Receipt) error {
	if models.EditLocked(s.status) {
		return repository.ErrReceiptBusy
	}
	if r.Status == "" {
		r.Status = s.status
	} else if r.Status != s.status && !models.UserCanTransition(s.status, r.Status) {
		return fmt.Errorf("%w: %s -> %s", repository.ErrInvalidTransition, s.status, r.Status)
	}
	s.status = r.Status
	s.updates++
	return nil
}

func TestUpdateReceiptStatusTransitions(t *testing.T) {
	cases := []struct {
		from, body string
		code       int
		want       string
	}{
		{models.StatusParsed, `,"status":"reviewed"`, 200, models.StatusReviewed},
		{models.StatusReviewed, ``, 200, models.StatusReviewed},                     // no status field
		{models.StatusReviewed, `,"status":"reviewed"`, 200, models.StatusReviewed}, // same status
		{models.StatusReviewed, `,"status":"uploaded"`, 409, models.StatusReviewed}, // invalid move
		{models.StatusUploaded, `,"status":"parsing"`, 409, models.StatusUploaded},  // worker-only
		{models.StatusReviewed, `,"status":"bogus"`, 400, models.StatusReviewed},    // unknown value
	}
	for _, c := range cases {
		repo := &statusRepo{status: c.from}
		h := NewReceiptHandler(repo, slog.Default())
		r := chi.NewRouter()
		r.Put("/api/v1/receipts/{id}", h.UpdateReceipt)
		body := `{"store_name":"Sprouts","date":"2026-10-01"` + c.body + `}`
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/v1/receipts/5", strings.NewReader(body)))
		if rec.Code != c.code || repo.status != c.want {
			t.Errorf("%s%s: code=%d status=%s body=%s", c.from, c.body, rec.Code, repo.status, rec.Body)
		}
		if models.EditLocked(c.from) {
			if !strings.Contains(rec.Body.String(), "being extracted") || repo.updates != 0 {
				t.Errorf("busy 409: %s", rec.Body)
			}
			continue
		}
		if c.code == 409 && !strings.Contains(rec.Body.String(), "Invalid status transition from "+c.from+" to ") {
			t.Errorf("409 message unclear: %s", rec.Body)
		}
	}
}

func (s *statusRepo) Delete(_ context.Context, id int) error {
	if s.status == models.StatusParsing {
		return repository.ErrReceiptBusy
	}
	s.status = "deleted"
	return nil
}

func TestDeleteReceiptWhileParsing(t *testing.T) {
	for st, want := range map[string]int{models.StatusParsing: 409, models.StatusUploaded: 204, models.StatusParsed: 204} {
		repo := &statusRepo{status: st}
		h := NewReceiptHandler(repo, slog.Default())
		r := chi.NewRouter()
		r.Delete("/api/v1/receipts/{id}", h.DeleteReceipt)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/v1/receipts/5", nil))
		if rec.Code != want {
			t.Errorf("delete while %s: %d %s", st, rec.Code, rec.Body)
		}
	}
}
