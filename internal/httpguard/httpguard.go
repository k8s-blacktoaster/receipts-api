// Package httpguard holds small CSRF guards shared by the JSON API and the web UI. There is no
// auth (VPN-only), so the threat is a page on another site making a VPN user's browser write.
package httpguard

import (
	"encoding/json"
	"mime"
	"net/http"
	"net/url"
	"time"
)

func unsafeMethod(m string) bool {
	return m == http.MethodPost || m == http.MethodPut || m == http.MethodPatch || m == http.MethodDelete
}

// SameOrigin refuses unsafe requests (POST/PUT/PATCH/DELETE) that a browser marks as coming
// from anywhere but this origin. Allowed: Sec-Fetch-Site "same-origin"; "none" (typed URL,
// bookmark) only without an Origin header; and non-browser clients that send neither header.
// Refused: Sec-Fetch-Site "cross-site" or "same-site" (a sibling subdomain is not trusted),
// Origin "null" (sandboxed iframes, data: URLs), and an Origin whose host differs.
func SameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if unsafeMethod(r.Method) {
			if reason := crossOrigin(r); reason != "" {
				refuse(w, r, http.StatusForbidden, reason)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func crossOrigin(r *http.Request) string {
	origin, hasOrigin := r.Header.Get("Origin"), r.Header.Get("Origin") != ""
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "same-origin":
	case "none":
		if hasOrigin {
			return "cross-origin request refused"
		}
	default: // cross-site, same-site, anything unknown
		return "cross-site request refused"
	}
	if hasOrigin {
		if origin == "null" {
			return "cross-origin request refused (Origin: null)"
		}
		if u, err := url.Parse(origin); err != nil || u.Host != r.Host {
			return "cross-origin request refused"
		}
	}
	return ""
}

// RequireJSON makes POST/PUT/PATCH bodies application/json (415 otherwise). Browsers can only
// send form or text/plain bodies cross-site without a CORS preflight, so this closes the
// "simple request" CSRF path on the JSON API.
func RequireJSON(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch {
			mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil || mt != "application/json" {
				refuse(w, r, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// refuse answers in the API's JSON error shape for /api paths, plain text otherwise.
func refuse(w http.ResponseWriter, r *http.Request, code int, msg string) {
	if len(r.URL.Path) >= 5 && r.URL.Path[:5] == "/api/" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "error", "code": code, "message": msg,
			"timestamp": time.Now().UTC()})
		return
	}
	http.Error(w, msg, code)
}
