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
