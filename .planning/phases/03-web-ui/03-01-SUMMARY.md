# 03-01 SUMMARY - Mobile web UI

**Branch:** phase-03-web-ui (base main 9f48834) · **Status:** executed, PR open

## Delivered
- **Web UI** (`internal/web`, go:embed templates + static): receipt list with status badges,
  camera/library upload (`accept="image/*,.heic,.heif"`), detail page (photo beside header,
  items, adjustments), edit, Approve, Re-extract (409 states explained), Delete, merchant
  rename, member picker cookie (`receipts_member`, HttpOnly, SameSite=Lax). Same-origin check
  on POSTs (403 cross-site). Pending receipts auto-refresh and their edit forms are disabled.
- **Guards (UI-05):** edits 409 while `uploaded`/`parsing`; DELETE 409 while `parsing`;
  re-extract moves the receipt to `uploaded` first (`RequestReextract`); queue reruns a receipt
  enqueued while running only if that run was skipped (lost claim), so re-extract after a lost
  claim requeues immediately. Stale-parsing docs corrected to `EXTRACTION_TIMEOUT_SECONDS` + 60s.
- **Merchants (UI-04):** rename re-normalizes; key collision -> 409 naming the other merchant
  (no merge in v1). Receipt edits that keep the printed name keep the merchant_id. Adjustment
  `applies_to_item_id` is remapped when items are rewritten.
- **Upload normalization (UI-07, migration 008):** `internal/imaging` detects JPEG/PNG/HEIC by
  magic bytes and re-encodes every upload to one JPEG (EXIF orientation, long edge <= 2000,
  q80, metadata stripped, 50 MP decode bound). Only the JPEG is stored; `sha256` = original
  bytes (dedupe), `jpeg_sha256` = stored JPEG; `KEEP_ORIGINAL_UPLOADS=true` also keeps the
  original (`original_object_key`). HEIC via libheif CLI (`heif-dec`/`heif-convert`, `sips` on
  macOS dev; `HEIC_DECODER` override); without a decoder HEIC gets 415 with guidance.
  Re-uploading a photo whose receipt was deleted reuses the orphaned document (was a 500).
- **Store location (ING-09):** schema/prompt (`receipt-v2`) add store_city/state/zip; worker
  upserts `stores.city/region/postal_code` by store number else address; repeats share the row.
- **Ops:** Dockerfile base/test/build/runtime (golang 1.23 alpine, `libheif-tools`,
  `CGO_ENABLED=0`); `docker build --target test` passes (HEIC test runs with heif-dec).
  Devcontainer adds `libheif-examples`. `.dockerignore` excludes `.env` and golden photos.
  VS Code launch loads `.env` via `envFile`; README documents `set -a; . ./.env; set +a`.

## Why exec libheif instead of cgo/wasm
Keeps the binary pure Go (CGO_ENABLED=0, simple cross-builds), isolates decoder crashes and
CVE exposure in a short-lived child process, and uses the distro-maintained libheif. The cost is
a process spawn per HEIC upload (tens of ms), fine for a household app.

## Verification
- `go build ./...`, `go vet ./...`, `go test -race -p 1 ./...` with the throwaway PG on 55432.
- Migration 008 up/down/up (and `TestMigrationsUpDownUp`).
- `docker build --target test .` passes; runtime image has `/usr/bin/heif-dec`.
- Local smoke (EXTRACTION_PROVIDER=none, throwaway DB, temp UPLOAD_DIR): pages 200, HEIC upload
  202 -> stored `.jpg`, duplicate 200, UI edit while uploaded 409 with message, cross-site 403.

## Extraction fixes + model choice (after the live golden run)
- Prompt `receipt-v3` (generic): discount/instant-savings lines referencing an item go into
  that item's discount_cents (never their own item); line_total is the pre-discount price;
  deposits/fees exactly once; savings only from a printed savings line; each priced line once.
- Checker: item-level reconciliation (lines - discounts + adjustments vs subtotal), negative
  prices, discount-looking names, deposit counted twice, savings copied from an item.
- `EXTRACTION_TIMEOUT_SECONDS` default 300; `EXTRACTION_REASONING_EFFORT` (default low) sent
  only to models that support it. Default model now `grok-4.7` (+ low effort).
- Live comparison (receipt-v3, 3 receipts, 12 calls, ground truth = manual reading of the
  photos, all three reconcile to the printed subtotal; local-only, not committed):

  | model / effort | items correct (of 62) | headers | latency | cost (3 receipts) | checker |
  |---|---|---|---|---|---|
  | grok-4.7 / low | 62 | all except merchant name variant | 21-55 s | ~$0.10 | right (R2 had name false positives, fixed) |
  | grok-4.3 / low | 52 (+1 name truncated) | OK | 28-34 s | ~$0.04 | flagged R2, R3 correctly |
  | grok-4.20 non-reasoning | 43 | OK | 13-15 s | ~$0.03 | flagged all 3 correctly |
  | grok-4.20 (reasoning) | 22 of 39 (R3 timed out at 300 s) | R2 date/savings wrong | 2m16-5m | ~$0.09+ | missed R2 (prices shifted one row; sums still reconcile) |

## QA fixes (PR #3 review)
- M1 CSRF: shared `httpguard`; JSON API requires application/json (415, 1 MiB cap); SameOrigin
  on all `/api/v1` writes including upload and extract; Origin null / same-site refused.
- M2 memory: decode budget by bit depth (200 MiB), HEIC size from `ispe` before decoding (and
  decoder output re-checked), max 2 concurrent normalizations.
- Minors: money/quantity bounds (NaN/Inf/1e308/int64 max/negative total -> 400), text length
  limits, approve of an incomplete receipt -> 400, strict local `next`, blank item name -> 400
  (explicit Remove checkbox).

## Open / deferred
- A misaligned price column (each price shifted one row) still reconciles; only a model-side fix
  or per-line checks (qty x unit = line) can catch it.
- HEIC from a real iPhone/Safari not tested on a device (iOS usually transcodes to JPEG anyway).
- Merchant merge (phase 4), multi-photo receipts (later phase).
