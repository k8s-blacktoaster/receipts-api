package models

import (
	"reflect"
	"testing"
)

func TestStatusTransitions(t *testing.T) {
	ok := [][2]string{{"uploaded", "parsing"}, {"parsing", "parsed"}, {"parsing", "needs_review"}, {"parsing", "failed"},
		{"failed", "parsing"}, {"parsed", "reviewed"}, {"reviewed", "needs_review"}, {"parsing", "uploaded"}}
	bad := [][2]string{{"uploaded", "parsed"}, {"parsing", "parsing"}, {"reviewed", "parsing"}, {"parsed", "uploaded"},
		{"uploaded", "reviewed"}, {"x", "parsed"}, {"parsed", "bogus"}}
	for _, c := range ok {
		if !CanTransition(c[0], c[1]) {
			t.Errorf("%s -> %s should be allowed", c[0], c[1])
		}
	}
	for _, c := range bad {
		if CanTransition(c[0], c[1]) {
			t.Errorf("%s -> %s should be rejected", c[0], c[1])
		}
	}
	if got := ReextractableStatuses(); !reflect.DeepEqual(got, []string{"uploaded", "parsed", "needs_review", "failed"}) {
		t.Fatalf("reextractable = %v", got)
	}
}
