package models

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
