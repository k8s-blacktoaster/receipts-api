// Package redact removes credentials from text before it is stored (parse_error), logged or
// returned. Secrets registered at startup (e.g. XAI_API_KEY) are replaced verbatim; Bearer tokens,
// Authorization header/field values and xAI-style keys are replaced by pattern.
package redact

import (
	"regexp"
	"sort"
	"strings"
	"sync"
)

const Mask = "[REDACTED]"

var (
	mu      sync.RWMutex
	secrets []string

	patterns = []*regexp.Regexp{
		// Authorization: Bearer xyz / "authorization":"Basic xyz" / authorization=xyz.
		// The field name is kept; scheme and value become exactly one Mask.
		regexp.MustCompile(`(?i)(authorization["']?\s*[:=]\s*["']?)(?:(?:bearer|basic|token)\s+)?(?:\[REDACTED\]|[^\s"',;}]+)`),
		// bare "Bearer xyz" (scheme included, so output never reads "Bearer [REDACTED]")
		regexp.MustCompile(`(?i)()\bbearer\s+(?:\[REDACTED\]|[A-Za-z0-9._~+/=-]+)`),
		// xAI / OpenAI / Anthropic style keys
		regexp.MustCompile(`()\b(?:xai|sk|sk-ant)-[A-Za-z0-9_-]{8,}`),
	}
)

// Register adds a secret value (ignored if shorter than 6 chars to avoid masking noise).
func Register(secret string) {
	secret = strings.TrimSpace(secret)
	if len(secret) < 6 {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	for _, s := range secrets {
		if s == secret {
			return
		}
	}
	secrets = append(secrets, secret)
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
}

// String returns s with every credential replaced by exactly one Mask ("[REDACTED]"): registered secrets, extra secrets and credential patterns masked.
func String(s string, extra ...string) string {
	mu.RLock()
	all := append(append([]string(nil), secrets...), extra...)
	mu.RUnlock()
	for _, sec := range all {
		if len(strings.TrimSpace(sec)) >= 6 {
			s = strings.ReplaceAll(s, sec, Mask)
		}
	}
	for _, p := range patterns {
		s = p.ReplaceAllString(s, "${1}"+Mask)
	}
	return s
}
