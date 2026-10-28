# Phase 4 - Normalization & review: summary

## Requirements
- NORM-01: matcher in `internal/repository/matcher.go`, run inside the receipt write tx for every
  unmapped line: merchant item number -> UPC -> merchant alias text (exact) -> pg_trgm fuzzy
  (>= 0.4) as a suggestion only. Match provenance in `receipt_items.match_source/match_confidence`.
- NORM-02: review queue (`GET /api/v1/review/items`, `/review`), map via API or UI; a correction
  learns a merchant-scoped manual alias (text + item number) and maps the merchant's other
  unmapped lines; later receipts reuse it.
- NORM-03: `MerchantRepository.Merge` (UI on /merchants); alias item-number collisions are kept
  with item_number cleared. Migration 006's backfill was changed to the same rule (only affects
  databases that have not applied 006 yet).
- NORM-04: merchant sanity check flags names containing the store city or street (whole tokens).

## Migration
- 009_product_matching: receipt_items.match_source / match_confidence / suggested_product_id,
  existing mappings backfilled as manual, unmapped partial index, trigram GIN on alias text and
  canonical names. Down drops them.

## Success criteria
- >=90% of lines with an item number/UPC auto-map: `TestMatcherAutoMapsItemNumbersAndUPCs`
  (20/20 once aliases/UPCs exist).
- Corrections create merchant-scoped aliases reused next time: `TestReviewQueueMapLearnsAliasAndReusesIt`.

## Conservative choices (open questions)
- Fuzzy matches never auto-map; LLM suggestions are not implemented (no live calls; `llm` reserved).
- Edits keep an existing product_id even if the item text/number changed.
- Merge: an alias-text collision keeps the target's alias and drops the source one (the unique
  key cannot hold both); the count is reported.
- Merge does not re-run matching over the merged merchant's old unmapped lines; new saves do.
- Mapping is allowed on reviewed receipts (product mapping is not receipt content).
- No product edit/merge/delete UI yet.
