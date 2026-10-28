# Summary 02-01: Photo upload + extraction pipeline

**Status:** executed 2026-10-27 on branch phase-02-upload-extraction (PR into main, not merged)
**Requirements:** ING-01 (single photo per upload), ING-02 (local disk; object store in phase 6),
ING-03, ING-04, ING-05

## Delivered
- Migration 007 (+down): merchant_id/date nullable until parsed (CHECK receipts_complete_when_parsed),
  one receipt per source document, pending-receipt index, source_documents.parsed_at.
- `internal/storage`: BlobStore (Local on UPLOAD_DIR, Memory for tests).
- `POST /api/v1/uploads` (multipart `image`, JPEG/PNG, <= 20 MiB, sha256 dedupe -> 200 existing)
  and `POST /api/v1/receipts/{id}/extract` (re-queue).
- `internal/extraction`: Provider interface; xAI Grok provider (chat completions, base64 image,
  strict json_schema, EXTRACTION_MODEL default grok-4.20, temperature 0); Anthropic stub
  (ErrNotImplemented); EXTRACTION_PROVIDER = xai|anthropic|none; mapping to the merchant-agnostic
  model (merchant resolved via models.NormalizeMerchantName; sku/item_number/upc; cents; sign
  convention); totals/required-field check -> needs_review with reasons; pipeline with
  timestamps and parse_error; in-process worker queue with restart re-queue.
- XAI_API_KEY only from env / k8s Secret (README, .env.example). Never logged.
- Swagger regenerated; receipts.http upload examples; README endpoints + env docs.

## Tests (44 passing, 0 failing; integration on throwaway PG 55432)
- extraction: request shape against httptest fake xAI, response parsing, error/invalid JSON (no
  key in errors), provider selection, Anthropic stub, mapping, totals check incl. tolerance,
  pipeline parsed / needs_review / failed / disabled / save-failure, end-to-end via fake server, queue.
- handlers: 202 + enqueue, duplicate 200, 400/413/415 rejects, store failure, re-extract 202/404/400/503.
- config: env defaults/overrides, invalid tolerance. storage: local round-trip, traversal guard.
- repository (DB): upload -> pipeline -> parsed (normalized merchant, items, adjustments, doc raw
  JSON/model/prompt version), needs_review, failed; sha256 dedupe; 007 CHECK; pending ids.
- Migrations up/down/up: pg_dump -s identical. Manual smoke: API up, upload 202, dup 200.

## Open questions
- Golden set of 20 real receipts (success criterion 2) not run: needs real photos + a live key.
- No ingest_jobs table yet: durable state is receipts.status; in-flight "parsing" rows are re-run on restart.
- Multiple photos per receipt (long receipts) and HEIC from iPhones: not yet (HEIC -> convert in UI phase 3).
- Default model name grok-4.20 should be confirmed against the account's /v1/language-models.
- Image retention/backup and object-store implementation belong to phase 6.
