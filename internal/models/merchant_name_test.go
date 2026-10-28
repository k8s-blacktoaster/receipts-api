package models

import "testing"

func TestNormalizeMerchantName(t *testing.T) {
	for in, want := range MerchantNameCases {
		if got := NormalizeMerchantName(in); got != want {
			t.Errorf("NormalizeMerchantName(%q) = %q, want %q", in, got, want)
		}
		if got := NormalizeMerchantName(want); got != want {
			t.Errorf("not idempotent for %q: %q", want, got)
		}
	}
}
