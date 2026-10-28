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
		"got bearer XYZ987 back":                               "got bearer [REDACTED] back",
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
