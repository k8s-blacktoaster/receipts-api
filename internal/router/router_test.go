package router

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"receipts-db/internal/handlers"
)

// The guards are wired on the API routes (they reject before the handler runs).
func TestAPIRoutesGuarded(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := New(handlers.NewReceiptHandler(nil, logger), &handlers.UploadHandler{Logger: logger}, &handlers.ProductHandler{Logger: logger}, nil, logger)
	cases := []struct {
		method, path, ct, origin string
		want                     int
	}{
		{"POST", "/api/v1/receipts", "text/plain", "", 415},
		{"PUT", "/api/v1/receipts/1", "application/x-www-form-urlencoded", "", 415},
		{"POST", "/api/v1/receipts", "application/json", "https://evil.example", 403},
		{"DELETE", "/api/v1/receipts/1", "", "null", 403},
		{"POST", "/api/v1/receipts/1/extract", "", "https://evil.example", 403},
		{"POST", "/api/v1/uploads", "multipart/form-data; boundary=x", "https://evil.example", 403},
		{"POST", "/api/v1/products", "text/plain", "", 415},
		{"POST", "/api/v1/products", "application/json", "https://evil.example", 403},
		{"POST", "/api/v1/review/items/1/map", "application/x-www-form-urlencoded", "", 415},
		{"POST", "/api/v1/review/items/1/map", "application/json", "null", 403},
	}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, "http://app.local"+c.path, strings.NewReader("x"))
		if c.ct != "" {
			r.Header.Set("Content-Type", c.ct)
		}
		if c.origin != "" {
			r.Header.Set("Origin", c.origin)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != c.want {
			t.Errorf("%s %s: %d want %d (%s)", c.method, c.path, rec.Code, c.want, rec.Body)
		}
	}
}
