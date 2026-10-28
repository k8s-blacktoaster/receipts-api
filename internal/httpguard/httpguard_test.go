package httpguard

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSameOrigin(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	h := SameOrigin(ok)
	cases := []struct {
		name, method, origin, site string
		want                       int
	}{
		{"get cross-site is fine", "GET", "https://evil.example", "cross-site", 204},
		{"cli without headers", "POST", "", "", 204},
		{"same-origin", "POST", "http://app.local", "same-origin", 204},
		{"origin match, no fetch metadata", "POST", "http://app.local", "", 204},
		{"none without origin", "POST", "", "none", 204},
		{"none with origin", "POST", "http://app.local", "none", 403},
		{"cross-site", "POST", "", "cross-site", 403},
		{"same-site sibling", "POST", "http://other.local", "same-site", 403},
		{"same-site even with matching origin", "PUT", "http://app.local", "same-site", 403},
		{"origin null", "POST", "null", "", 403},
		{"origin mismatch", "DELETE", "https://evil.example", "", 403},
		{"unknown fetch site", "PATCH", "", "weird", 403},
	}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, "http://app.local/api/v1/receipts/1", nil)
		if c.origin != "" {
			r.Header.Set("Origin", c.origin)
		}
		if c.site != "" {
			r.Header.Set("Sec-Fetch-Site", c.site)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != c.want {
			t.Errorf("%s: %d, want %d", c.name, rec.Code, c.want)
		}
	}
}

func TestRequireJSON(t *testing.T) {
	h := RequireJSON(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	for ct, want := range map[string]int{
		"application/json": 204, "application/json; charset=utf-8": 204,
		"text/plain": 415, "application/x-www-form-urlencoded": 415, "multipart/form-data; boundary=x": 415, "": 415,
	} {
		r := httptest.NewRequest("POST", "/api/v1/receipts", nil)
		if ct != "" {
			r.Header.Set("Content-Type", ct)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != want {
			t.Errorf("%q: %d want %d", ct, rec.Code, want)
		}
	}
	r := httptest.NewRequest("DELETE", "/api/v1/receipts/1", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != 204 {
		t.Errorf("DELETE without body: %d", rec.Code)
	}
}
