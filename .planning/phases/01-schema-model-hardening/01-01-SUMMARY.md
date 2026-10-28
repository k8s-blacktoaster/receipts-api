# 01-01 Summary: Schema & model hardening

**Status:** executed 2026-10-27 (awaiting /gsd-verify-work)
**Requirements:** DATA-01..DATA-08

## Delivered
- `cmd/migrate up|down [-steps N]` + `database.RollbackMigrations` (runner previously had no down).
- `migrations/003_merchant_agnostic_core_model` (up/down):
  - money -> BIGINT cents (`subtotal_cents`, `tax_cents`, `total_cents`, `savings_cents`,
    item `unit_price_cents`, `line_total_cents`, `discount_cents`, generated `net_total_cents`);
    legacy DECIMAL columns converted and dropped; down converts back.
  - `merchants`, `stores` (store_number, address, ...), backfilled from legacy store_* text.
  - receipt_items: line_no, raw_text, sku, item_number, upc, product_id, NUMERIC qty, unit,
    taxable, tax_flag, is_return, confidence, metadata.
  - `receipt_adjustments`, `products`, `product_aliases` (merchant-scoped uniques),
    `source_documents` (object_key, sha256, raw_extraction, parse_state).
  - receipts: merchant_id, store_id, source_document_id, purchased_at, currency, payment,
    `source` (photo|manual), nullable `external_id`, confidence, metadata.
  - 002 views rebuilt on cents/total (same names/columns, plus total/tax/savings);
    new `product_purchase_locations_view`.
- `migrations/004_members_and_parse_status` (up/down): `members`, receipts.member_id /
  uploaded_by_id / reviewed_by_id, `status` (uploaded|parsing|parsed|needs_review|reviewed|failed),
  parse_error, parse_started_at, parsed_at, reviewed_at, receipt_items.edited_at.
- Models/repos: integer cents, merchant/store resolution, items+adjustments, UpdateItem,
  SetStatus, ProductRepository (aliases, ResolveAlias, WhereBought), MemberRepository.

## Verification
- up/down/up on seeded legacy data: data and all 002 views identical after down.
- go build / vet / test pass; integration tests against disposable Postgres (TEST_DATABASE_URL).

## Deviations from canvas DDL
- Integer cents instead of NUMERIC; store/alias keyed by merchant_id (not chain text).
- No ingest_jobs (phase 2) and no pg_trgm (phase 4).
- source_documents.kind values generic (photo/email/pdf/import/manual).

## QA follow-up (2026-10-27)
| Finding | Fix | Test |
|---|---|---|
| F1 whitespace merchant dupes | 005: trim + merge, unique lower(btrim(name)), trimmed CHECK; repo trims | TestF1MerchantDedupeTrimmed, TestF1F6BackfillMerges |
| F2 money sanity | 005: >=0 CHECKs, adjustment sign CHECK; no total-equation constraint (phase 2 needs_review) | TestF2MoneyChecks |
| F3 store/merchant mismatch | 005: UNIQUE(stores.id, merchant_id) + composite FK | TestF3StoreBelongsToMerchant |
| F4 FK indexes | 005: 5 partial indexes | TestF4FKIndexes |
| F5 up ignores -steps | RunMigrationsN; `migrate up -steps N` | TestF5UpSteps |
| F6 store dupes | 005: unique (merchant_id, lower(address)) where no number; repo upsert | TestF6StoreUpsertByAddress, TestF1F6BackfillMerges |
| F7 legacy store_* | store_name nullable; receipt_details_view/recent_purchases_view read merchants/stores | TestF7ViewsUseMerchantStore |
005 down is lossy for merged merchants/stores (not un-merged). pg_dump -s identical after down 1 / up.

## Migration 006 + QA major (2026-10-27)
- 006: merchant key `lower(regexp_replace(btrim(name), '\s+', ' ', 'g'))`; duplicates merged.
- Display name is chosen only among existing variants: legacy receipts.store_name values
  (whitespace-collapsed, one vote per receipt; merchant rows are a zero-vote fallback).
  Most votes wins; tie -> mixed case over ALL-CAPS/all-lower; casing is never invented.
- Resolve/insert upgrades an all-lower or ALL-CAPS stored name when a mixed-case variant arrives.
- Tests: TestQAReproLegacyStoreNameVariants (001/002 legacy data -> Costco, Trader Joes),
  TestMerchantBackfillDisplayName, TestMerchantAllCapsUpgrade, TestMerchantInternalWhitespaceAndCasingUpgrade.
- 16/16 repository integration tests pass; pg_dump -s identical after down 2 / up.

## 006 display-name rule revised (CoS, 2026-10-27; 006 edited in place - unreleased, only test DBs ran it)
- Any mixed-case variant beats ALL-CAPS/all-lower regardless of count; among mixed-case
  variants the most frequent wins. No mixed-case variant -> initcap() of the collapsed name.
- Repo: a mixed-case name upgrades a stored all-lower, ALL-CAPS or initcap'd name (initcap'd only
  by a non-initcap mixed name); a stored mixed-case name is never replaced (no flip-flop).
- Known quirk: initcap("TRADER JOE'S") = "Trader Joe'S"; fixed as soon as a real "Trader Joe's" arrives.
- Tests: TestLegacyMixedCaseBeatsCountAndInitcap (3x COSTCO + 1x Costco -> Costco; whole foods /
  WHOLE  FOODS -> Whole Foods), TestMerchantBackfillDisplayName (updated), TestMerchantInitcapUpgradeNoFlipFlop.
- 18/18 repository integration tests pass; pg_dump -s identical after down 2 / up.

## 006 final naming iteration (QA major at a046b33; 006 edited in place, unreleased)
- Bug: the repo treated any initcap()-equal name as machine-made, so 'Trader Joes' was overwritten by 'Trader joes'.
- Fix: merchants.name_is_derived BOOLEAN NOT NULL DEFAULT false (down drops it). 006 sets it true
  only for the initcap fallback / all-lower / ALL-CAPS choices. Repo upgrades a name only while
  derived and only with a mixed-case spelling (clears the flag); new merchants are derived iff
  all-lower or ALL-CAPS. Initcap-equality heuristic removed.
- Tests: TestMixedCaseStoredNameNeverOverwritten (QA repro), TestNewMerchantDerivedFlag,
  TestMerchantBackfillDisplayName (asserts flags), TestMerchantInitcapUpgradeNoFlipFlop (flag-based setup).
- 20/20 repository integration tests pass; pg_dump -s identical after down 2 / up.

## 006 simplified (Jack, 2026-10-27; edited in place, unreleased - supersedes the display-name iterations above)
- One stored merchant name = normalized key (models.NormalizeMerchantName / SQL merchant_name_key()).
  name_is_derived, display-name voting, initcap and all upgrade logic removed.
- '&' -> ' and ' padded: "H&M" -> h_and_m, "AT&T" -> at_and_t, "Barnes & Noble" -> barnes_and_noble.
- Backfill merges duplicate merchants, deduping their stores (store number / address) and aliases
  (alias text / item number) and moving receipts. Unique index on merchants.name; CHECK
  name = merchant_name_key(name). 006 down is lossy (no un-merge, names stay normalized).
- Tests: TestNormalizeMerchantName (table), TestMerchantNameSQLGoParity, TestMerchantBackfillMerge
  (Costco/COSTCO/costco + trader  joes/Trader Joe's/TRADER   JOES -> costco, trader_joes, with
  store + alias dedupe), TestMerchantNameUniqueAndCheck, TestMerchantResolveByKey. Obsolete naming tests removed.
- 16/16 repository integration tests pass (incl. TestMerchantTrigramIndexAndSimilarity); pg_dump -s identical after down 2 / up.
