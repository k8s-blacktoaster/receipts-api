package mcp

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"net/url"
	"strings"
)

// RequireBearer (MCP-03) accepts only "Authorization: Bearer <token>". The comparison is
// constant-time over sha256 digests so token length is not leaked. An empty token rejects
// everything (callers should not mount the endpoint at all without one). It also applies the
// spec's DNS-rebinding guard: a request carrying an Origin header must be same-host.
func RequireBearer(token string, next http.Handler) http.Handler {
	want := sha256.Sum256([]byte(token))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if o := r.Header.Get("Origin"); o != "" {
			u, err := url.Parse(o)
			if err != nil || !strings.EqualFold(u.Host, r.Host) {
				http.Error(w, "forbidden origin", http.StatusForbidden)
				return
			}
		}
		h := r.Header.Get("Authorization")
		got, ok := strings.CutPrefix(h, "Bearer ")
		if !ok {
			got, ok = strings.CutPrefix(h, "bearer ")
		}
		gotSum := sha256.Sum256([]byte(strings.TrimSpace(got)))
		if token == "" || !ok || subtle.ConstantTimeCompare(gotSum[:], want[:]) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="receipts-mcp"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
