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

## QA fixes (round 1, after 49f626e)
| Finding | Fix | Test |
|---|---|---|
| BLOCKER double parsing | `ClaimForParsing`: single `UPDATE receipts SET status='parsing', parse_started_at=now() ... WHERE id=$1 AND status = ANY(uploaded,parsed,needs_review,failed) RETURNING id`; pipeline skips (no provider call) if not claimed. Transition table (`models/status.go`) enforced in `SetStatus` (`ErrInvalidTransition`). Queue coalesces duplicate enqueues. `/extract` 409 while parsing. | `TestConcurrentExtractSingleProviderCall` (4 workers, slow fake provider, upload + 5 concurrent extracts => exactly 1 provider call, 409 while parsing); `TestClaimForParsingIsAtomic` (8 concurrent Process => 1 call, 7 skipped; invalid transition; reviewed unclaimable; stale reset); `TestStatusTransitions`; `TestPipelineSkipsUnclaimable` |
| MAJOR re-extract on reviewed | Always 409 in v1, no force option. REQUIREMENTS ING-07, swagger 409, README. | `TestReextract` (409 for parsing and reviewed) |
| Blob store race | `CreateTemp` + `os.Link` into place; "exists" = success with `created=false`; `Delete` added. | `TestLocalPutConcurrentSameKey` (16 writers: 1 created, full bytes always, no temp leaks); `TestLocalBlobStore` |
| Unknown member / orphan files | `member_id` / `uploaded_by_id` checked (`MemberExists`) before any write -> 400; blob deleted on later DB error only if this request created it. | `TestUploadUnknownMemberRejectedBeforeWrite`, `TestUploadCleansUpBlobOnDBError` |
| Truncated images | `image.DecodeConfig` (50 MP cap) + full `image.Decode` -> 400. | `TestUploadTruncatedImageRejected` (PNG and JPEG) |
| 007 down | Comment: image files left orphaned in `UPLOAD_DIR`; README too. | (doc) |
| Key leakage | `internal/redact` (registered API key, `Authorization`/`Bearer`, `xai-`/`sk-` keys) applied to xAI errors, stored `parse_error`, pipeline/queue/startup logs. | `internal/redact` tests, `TestXAIErrorRedactsEchoedKey`, `TestPipelineRedactsStoredError` |
| Graceful shutdown | `Queue.Shutdown(EXTRACTION_SHUTDOWN_TIMEOUT, default 25s)`: no new jobs, waits for in-flight, cancels on timeout. Startup `ResetStaleParsing` then requeue `uploaded`. | `TestQueueShutdownWaitsForInFlight`, `TestQueueShutdownTimeoutCancelsInFlight`, `TestQueueCoalescesDuplicates`, `TestExtractionShutdownTimeout` |

Tests now: 59 pass, 0 fail (`go test -race -p 1` with TEST_DATABASE_URL on throwaway PG 55432; `-p 1` because integration packages share one test DB). Migrations up / down all / up OK.
~~Not changed: `PUT /receipts/{id}` could still set `status` directly.~~ Fixed in round 2 (below).

### QA round 2: PUT status
`PUT /api/v1/receipts/{id}` now routes status changes through the transition table (`receiptRepo.Update` locks the row with `SELECT ... FOR UPDATE`, rejects invalid moves with `ErrInvalidTransition`; handler returns 409 "Invalid status transition from X to Y; receipt unchanged"). Omitted status keeps the current one (it used to reset to the default); same status is allowed; `parsing` can only be entered via `ClaimForParsing`; unknown values stay 400. Tests: `TestUpdateReceiptStatusTransitions` (handler: valid, omitted, same, reviewed->uploaded 409, ->parsing 409, bogus 400) and `TestUpdateRoutesStatusThroughTransitionTable` (Postgres: rejected updates leave the row unchanged). Closes the PUT gap noted above. Tests: 61 pass, 0 fail.

### QA round 3 (re-QA at de4fc5d)
| Finding | Fix | Test |
|---|---|---|
| MAJOR 1 stuck in parsing | Claim token = exact `parse_started_at` (`clock_timestamp()`). Cancelled job (shutdown timeout / ctx cancel) calls `ReleaseClaim` (parsing -> uploaded only if still its claim) on a 5s background context. `Queue.Recover` (reset parsing older than job timeout + 30s grace, requeue all uploaded) runs at startup and every `EXTRACTION_STALE_SWEEP_INTERVAL` (default 60s). | `TestShutdownTimeoutReleasesAndRestartReprocesses` (12s jobs, 2s shutdown timeout, restart reprocesses, re-extract works), `TestReleaseClaimTokenAndPeriodicSweep`, `TestPipelineCancelledJobReleasesClaim`, `TestExtractionSweepInterval` |
| MAJOR 2 lost update | PUT while `parsing` -> 409 for any body (`ErrReceiptBusy`). Worker final write is one `CompleteParse` tx conditional on `status='parsing'` + own token (`FailParse` likewise); otherwise `ErrClaimLost` and the result is discarded. | `TestPutWhileParsingIsRefused`, `TestReleaseClaimTokenAndPeriodicSweep` (old token can't complete/fail/release), `TestPipelineClaimLostDiscardsResult`, `TestUpdateRoutesStatusThroughTransitionTable` |
| Minor: user vs worker transitions | `models.UserCanTransition`: parsed -> needs_review/reviewed, needs_review -> reviewed, failed -> reviewed, reviewed -> needs_review. PUT can't enter/leave parsing, set failed or uploaded. | `TestUserTransitions`, `TestUpdateReceiptStatusTransitions` |
| Minor: redaction format | Every credential becomes exactly `[REDACTED]` (scheme dropped, no double masks). | `TestRedact`, `TestRedactConsistentMarker` |

Tests now: 69 pass, 0 fail (`go test -race -p 1`, PG 55432).
