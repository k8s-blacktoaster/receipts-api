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

## Open / deferred
- Live golden check not run: xAI rejected the local `.env` key. Rerun once the key is fixed.
- HEIC from a real iPhone/Safari not tested on a device (iOS usually transcodes to JPEG anyway).
- Merchant merge (phase 4), multi-photo receipts (later phase).
