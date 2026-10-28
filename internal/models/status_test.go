package models

import (
	"reflect"
	"testing"
)

func TestStatusTransitions(t *testing.T) {
	ok := [][2]string{{"uploaded", "parsing"}, {"parsing", "parsed"}, {"parsing", "needs_review"}, {"parsing", "failed"},
		{"failed", "parsing"}, {"parsed", "reviewed"}, {"reviewed", "needs_review"}, {"parsing", "uploaded"}}
	bad := [][2]string{{"uploaded", "parsed"}, {"parsing", "parsing"}, {"reviewed", "parsing"}, {"reviewed", "uploaded"},
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

func TestUserTransitions(t *testing.T) {
	ok := [][2]string{{"parsed", "reviewed"}, {"parsed", "needs_review"}, {"needs_review", "reviewed"},
		{"failed", "reviewed"}, {"reviewed", "needs_review"}}
	bad := [][2]string{{"parsing", "parsed"}, {"parsing", "needs_review"}, {"parsing", "failed"}, {"parsing", "uploaded"},
		{"uploaded", "parsing"}, {"failed", "parsing"}, {"uploaded", "failed"}, {"parsed", "failed"},
		{"failed", "uploaded"}, {"reviewed", "uploaded"}, {"uploaded", "reviewed"}}
	for _, c := range ok {
		if !UserCanTransition(c[0], c[1]) {
			t.Errorf("user %s -> %s should be allowed", c[0], c[1])
		}
	}
	for _, c := range bad {
		if UserCanTransition(c[0], c[1]) {
			t.Errorf("user %s -> %s should be rejected", c[0], c[1])
		}
	}
	for from, tos := range userTransitions {
		for _, to := range tos {
			if !CanTransition(from, to) {
				t.Errorf("user edge %s -> %s missing from the full table", from, to)
			}
		}
	}
}

func TestEditLocked(t *testing.T) {
	for st, want := range map[string]bool{"uploaded": true, "parsing": true, "parsed": false, "needs_review": false, "failed": false, "reviewed": false} {
		if EditLocked(st) != want {
			t.Errorf("EditLocked(%s) != %v", st, want)
		}
	}
	if !CanTransition("parsed", "uploaded") || !CanTransition("needs_review", "uploaded") || CanTransition("reviewed", "uploaded") {
		t.Fatal("re-extract request edges")
	}
	if UserCanTransition("parsed", "uploaded") {
		t.Fatal("users must not request uploaded directly via PUT")
	}
}
