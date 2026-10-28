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

## QA follow-ups (after 7bb14a1)
- Merges stick: migration 010 `merchant_name_redirects`; merged-away names resolve to the survivor.
- Review map with a new product creates it inside the map transaction (no orphans on 404/409).
- Merge never folds a numbered store by address; both stores are kept.
- UPCs: 8/12/13/14 digits (model + API), migration 011 per-merchant unique `product_aliases.upc`
  (invalid products.upc cleared; duplicates keep the oldest alias's UPC), matching per merchant.
- Exact alias matching uses merchant_name_key (migration 012 index).

## Conservative choices (open questions)
- Fuzzy matches never auto-map; LLM suggestions are not implemented (no live calls; `llm` reserved).
- Edits keep an existing product_id even if the item text/number changed.
- Merge: an alias-text collision keeps the target's alias and drops the source one (the unique
  key cannot hold both); the count is reported.
- Merge does not re-run matching over the merged merchant's old unmapped lines; new saves do.
- Mapping is allowed on reviewed receipts (product mapping is not receipt content).
- No product edit/merge/delete UI yet.
