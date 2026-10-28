package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"

	"receipts-db/internal/models"
)

// FuzzySuggestThreshold is the minimum pg_trgm similarity for a review-queue suggestion.
// Fuzzy matches are never applied automatically; they only pre-fill the review queue.
const FuzzySuggestThreshold = 0.4

// matchItem implements NORM-01 for one line, in order:
//  1. merchant-scoped item number (product_aliases.item_number)
//  2. merchant-scoped UPC (product_aliases.upc)
//  3. merchant-scoped alias text (exact, case-insensitive; raw text, then name)
//  4. fuzzy (pg_trgm) over the merchant's aliases and all canonical names -> suggestion only
//
// A line that already carries a product_id (mapped by hand or kept through an edit) is left alone.
func matchItem(ctx context.Context, q txExec, merchantID int64, it *models.ReceiptItem) error {
	if it.ProductID != nil {
		if it.MatchSource == "" {
			it.MatchSource, it.MatchConfidence = "manual", ptrF(1)
		}
		it.SuggestedProductID = nil
		return nil
	}
	it.MatchSource, it.MatchConfidence, it.SuggestedProductID = "", nil, nil
	set := func(id int64, src string) {
		it.ProductID, it.MatchSource, it.MatchConfidence = &id, src, ptrF(1)
	}
	one := func(query string, args ...any) (int64, bool, error) {
		var id int64
		err := q.QueryRowContext(ctx, query, args...).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return 0, false, nil
		}
		return id, err == nil, err
	}
	if n := strings.TrimSpace(it.ItemNumber); n != "" && merchantID != 0 {
		id, ok, err := one(`SELECT product_id FROM product_aliases WHERE merchant_id = $1 AND item_number = $2`, merchantID, n)
		if err != nil {
			return fmt.Errorf("match item number: %w", err)
		}
		if ok {
			set(id, "item_number")
			return nil
		}
	}
	if u := strings.TrimSpace(it.UPC); u != "" && merchantID != 0 {
		id, ok, err := one(`SELECT product_id FROM product_aliases WHERE merchant_id = $1 AND upc = $2`, merchantID, u)
		if err != nil {
			return fmt.Errorf("match upc: %w", err)
		}
		if ok {
			set(id, "upc")
			return nil
		}
	}
	texts := itemTexts(it)
	if merchantID != 0 {
		for _, t := range texts {
			id, ok, err := one(`SELECT product_id FROM product_aliases WHERE merchant_id = $1 AND lower(alias_text) = lower($2)`, merchantID, t)
			if err != nil {
				return fmt.Errorf("match alias: %w", err)
			}
			if ok {
				set(id, "exact")
				return nil
			}
		}
	}
	var best int64
	var bestSim float64
	for _, t := range texts {
		var id int64
		var sim float64
		err := q.QueryRowContext(ctx, `
			SELECT product_id, sim FROM (
				SELECT product_id, similarity(lower(alias_text), lower($2)) AS sim
				  FROM product_aliases WHERE merchant_id = $1 AND lower(alias_text) % lower($2)
				UNION ALL
				SELECT id, similarity(lower(canonical_name), lower($2))
				  FROM products WHERE lower(canonical_name) % lower($2)
			) c WHERE sim >= $3 ORDER BY sim DESC, product_id LIMIT 1`, merchantID, t, FuzzySuggestThreshold).Scan(&id, &sim)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return fmt.Errorf("fuzzy match: %w", err)
		}
		if sim > bestSim {
			best, bestSim = id, sim
		}
	}
	if best != 0 {
		it.SuggestedProductID, it.MatchSource = &best, "fuzzy"
		it.MatchConfidence = ptrF(math.Round(bestSim*1000) / 1000)
	}
	return nil
}

// itemTexts are the printed forms used for alias lookups: raw text first, then the name.
func itemTexts(it *models.ReceiptItem) []string {
	var out []string
	for _, s := range []string{it.RawText, it.Name} {
		s = strings.TrimSpace(s)
		if s != "" && (len(out) == 0 || !strings.EqualFold(out[0], s)) {
			out = append(out, s)
		}
	}
	return out
}

// aliasText is the text recorded when a correction teaches an alias.
func aliasText(it *models.ReceiptItem) string {
	if t := itemTexts(it); len(t) > 0 {
		return t[0]
	}
	return ""
}

func ptrF(f float64) *float64 { return &f }
