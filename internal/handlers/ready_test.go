package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type pingFunc func(context.Context) error

func (f pingFunc) PingContext(ctx context.Context) error { return f(ctx) }

func TestReadiness(t *testing.T) {
	ok := pingFunc(func(context.Context) error { return nil })
	down := pingFunc(func(context.Context) error { return errors.New("connection refused") })
	hang := pingFunc(func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() })
	cases := []struct {
		name   string
		h      Readiness
		method string
		want   int
	}{
		{"ok", Readiness{DB: ok}, http.MethodGet, 200},
		{"head", Readiness{DB: ok}, http.MethodHead, 200},
		{"down", Readiness{DB: down}, http.MethodGet, 503},
		{"timeout", Readiness{DB: hang, Timeout: 20 * time.Millisecond}, http.MethodGet, 503},
		{"nil db", Readiness{}, http.MethodGet, 503},
		{"post", Readiness{DB: ok}, http.MethodPost, 405},
	}
	for _, c := range cases {
		start := time.Now()
		rec := httptest.NewRecorder()
		c.h.ServeHTTP(rec, httptest.NewRequest(c.method, "/readyz", nil))
		if rec.Code != c.want {
			t.Fatalf("%s: code %d, want %d", c.name, rec.Code, c.want)
		}
		if time.Since(start) > time.Second {
			t.Fatalf("%s: took %v (timeout not applied)", c.name, time.Since(start))
		}
	}
}
