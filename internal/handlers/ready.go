package handlers

import (
	"context"
	"net/http"
	"time"
)

// Pinger is satisfied by *sql.DB / *database.DB.
type Pinger interface {
	PingContext(ctx context.Context) error
}

// Readiness serves GET /readyz: 200 when the database answers a ping within Timeout, else 503.
// Used by the k8s readiness probe; liveness stays on /api/v1/health, which never touches the DB,
// so a Postgres outage takes the pod out of the Service without restarting it.
type Readiness struct {
	DB      Pinger
	Timeout time.Duration // default 2s
}

func (h Readiness) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	t := h.Timeout
	if t <= 0 {
		t = 2 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), t)
	defer cancel()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if h.DB == nil || h.DB.PingContext(ctx) != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("database unavailable\n"))
		return
	}
	_, _ = w.Write([]byte("ok\n"))
}
