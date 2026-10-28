package models

import (
	"errors"
	"time"
)

// ClaimToken identifies one worker's claim on a receipt: the exact parse_started_at the claim
// wrote. Worker writes (complete, fail, release) only apply while the row still carries it.
type ClaimToken time.Time

// ErrClaimLost means the receipt is no longer "parsing" under this worker's claim (reset by
// the stale sweep and re-claimed, or changed by someone else); the worker must not write.
var ErrClaimLost = errors.New("extraction claim lost")

// statusTransitions is the allowed receipt status graph (from -> to). Enforced by
// repository SetStatus; ClaimForParsing implements the "-> parsing" edges atomically.
//
//	uploaded     -> parsing | failed
//	parsing      -> parsed | needs_review | failed | uploaded (stale-claim reset)
//	parsed       -> parsing (re-extract) | needs_review | reviewed
//	needs_review -> parsing (re-extract) | reviewed
//	failed       -> parsing (re-extract) | uploaded | reviewed (fixed by hand)
//	reviewed     -> needs_review (reopen). Never re-extracted in v1.
var statusTransitions = map[string][]string{
	StatusUploaded:    {StatusParsing, StatusFailed},
	StatusParsing:     {StatusParsed, StatusNeedsReview, StatusFailed, StatusUploaded},
	StatusParsed:      {StatusParsing, StatusNeedsReview, StatusReviewed},
	StatusNeedsReview: {StatusParsing, StatusReviewed},
	StatusFailed:      {StatusParsing, StatusUploaded, StatusReviewed},
	StatusReviewed:    {StatusNeedsReview},
}

// userTransitions is the subset a user may perform via PUT /receipts/{id} (review flow).
// Everything into or out of "parsing", and every move to "failed" or "uploaded", belongs to
// the extraction worker. A receipt that is "parsing" cannot be edited at all.
//
//	parsed       -> needs_review | reviewed
//	needs_review -> reviewed
//	failed       -> reviewed (fixed by hand)
//	reviewed     -> needs_review (reopen)
var userTransitions = map[string][]string{
	StatusParsed:      {StatusNeedsReview, StatusReviewed},
	StatusNeedsReview: {StatusReviewed},
	StatusFailed:      {StatusReviewed},
	StatusReviewed:    {StatusNeedsReview},
}

// UserCanTransition reports whether a user edit may move from -> to. It is always a subset
// of CanTransition.
func UserCanTransition(from, to string) bool {
	for _, t := range userTransitions[from] {
		if t == to {
			return CanTransition(from, to)
		}
	}
	return false
}

// CanTransition reports whether from -> to is allowed.
func CanTransition(from, to string) bool {
	for _, t := range statusTransitions[from] {
		if t == to {
			return true
		}
	}
	return false
}

// StatusesAllowedInto lists the statuses from which `to` may be entered.
func StatusesAllowedInto(to string) []string {
	var out []string
	for _, from := range []string{StatusUploaded, StatusParsing, StatusParsed, StatusNeedsReview, StatusFailed, StatusReviewed} {
		if CanTransition(from, to) {
			out = append(out, from)
		}
	}
	return out
}

// ReextractableStatuses are the statuses a photo receipt can be (re-)claimed for parsing from.
func ReextractableStatuses() []string { return StatusesAllowedInto(StatusParsing) }
