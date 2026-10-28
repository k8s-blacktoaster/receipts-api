package redact

import (
	"strings"
	"testing"
)

func TestRedact(t *testing.T) {
	Register("super-secret-key-123")
	cases := map[string]string{
		"xai returned 401: bad key super-secret-key-123":       "xai returned 401: bad key [REDACTED]",
		"Authorization: Bearer abc.def-ghi":                    "Authorization: [REDACTED]",
		`{"authorization":"Bearer tok123"}`:                    `{"authorization":"[REDACTED]"}`,
		"header authorization=Basic Zm9vOmJhcg== sent":         "header authorization=[REDACTED] sent",
		"got bearer XYZ987 back":                               "got [REDACTED] back",
		"Bearer super-secret-key-123 echoed":                   "[REDACTED] echoed",
		"Authorization: Bearer super-secret-key-123":           "Authorization: [REDACTED]",
		"key xai-AbCdEf123456789 rejected":                     "key [REDACTED] rejected",
		"sk-ant-api03-abcdefghij leaked":                       "[REDACTED] leaked",
		"plain error, nothing secret (receipt 12, status 503)": "plain error, nothing secret (receipt 12, status 503)",
	}
	for in, want := range cases {
		if got := String(in); got != want {
			t.Errorf("String(%q) = %q, want %q", in, got, want)
		}
	}
	if got := String("extra one-off-secret here", "one-off-secret"); strings.Contains(got, "one-off-secret") {
		t.Fatalf("extra secret not masked: %q", got)
	}
	Register("abc") // too short: ignored
	if got := String("abc def"); got != "abc def" {
		t.Fatalf("short secret masked: %q", got)
	}
}

// Output is uniform: only the literal "[REDACTED]" marker, never a scheme or doubled mask.
func TestRedactConsistentMarker(t *testing.T) {
	Register("super-secret-key-123")
	for _, in := range []string{
		"Bearer super-secret-key-123", "bearer abc123token", "Authorization: Bearer super-secret-key-123",
		"authorization=Basic Zm9vOmJhcg==", "xai-AbCdEf123456789", `{"Authorization":"Bearer x.y.z"}`,
	} {
		got := String(in)
		if strings.Contains(strings.ToLower(got), "bearer") || strings.Contains(strings.ToLower(got), "basic") ||
			strings.Count(got, Mask) != 1 || strings.Contains(got, "REDACTED]]") {
			t.Errorf("String(%q) = %q", in, got)
		}
	}
}
