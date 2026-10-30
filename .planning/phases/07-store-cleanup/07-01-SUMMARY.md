# Phase 7: legacy store_* column cleanup (07-01 summary)

## Migration 014_drop_legacy_store_columns
- **up:**
  1. Copies values the normalized tables lack, without overwriting anything:
     - `store_category` goes to `merchants.category`.
     - `store_address` and `store_phone` go to `stores.address` and `stores.phone`, using the
       newest receipt per store.
  2. Drops `idx_receipts_store_name` and `idx_receipts_store_date`.
  3. Renames `store_name` to `printed_merchant_name`.
  4. Drops `store_category`, `store_address` and `store_phone`.
- **down:**
  1. Renames the column back.
  2. Re-adds the three columns and refills them from `merchants` and `stores`.
  3. Recreates the 001 indexes.
  
  Per-receipt values that differed from the normalized ones before 014 are not recoverable.
  Every reader has used the normalized values since 005.

## Decisions
- **`store_name` is renamed, not dropped.** The printed merchant name is real data and not a copy
  of merchants/stores: edits keep `merchant_id` when the printed name is unchanged, and the UI
  shows it. The rename keeps it and removes the legacy `store_*` naming.
- **API and UI JSON are unchanged.** `store_name` is the printed name, `store_category` is
  `merchants.category`, and `store_address`, `store_phone` and `store_number` come from `stores`.
- **Known loss:** a receipt with a phone but neither a store number nor an address has no store
  row, so 014 drops that phone. Only hand-entered receipts could look like that.
- **Views:** none depend on these columns (005 moved them onto merchants/stores), so none changed.

## Tests
- `TestMigration014LegacyStoreColumns` seeds at 013, then goes up, checks the copied values and
  the repository read, goes down and checks the columns, values and indexes are back, then goes
  up again.
- `TestReceiptStoreFieldsViaNormalizedTables` round-trips create, get and list.
- `TestF7` and the guards test now use `printed_merchant_name`.
- The CLI round trip works on :55434: `migrate down -steps 1` brings the 4 legacy columns back,
  and `migrate up` removes them again.

## QA round 1 (PR #9 at ea0c33c)
- **Major 1:** store_* values that can't be stored are rejected with a 422 (`ErrStoreField`) on API
  create/PUT and the web form. Nothing is silently dropped. A merchant with no category gets it
  written through. A category different from the merchant's existing one is rejected, not written
  through, because the category is shared by every receipt of that merchant. A phone needs a store
  location, and with one it writes through. Extraction stays lenient so no parse fails on these.
  The web form no longer sends the loaded category back.
- **Major 2:** DEPLOY.md section 6 explains rollback across migrations: migrate down with the new
  image first, then roll the image back. It lists exactly what 014 down loses. `/readyz` checks
  the schema with `database.CheckSchema` (expected versions from the image's migrations dir). Any
  missing version (down-level or a gap) or unknown version (ahead) gives 503. This migration
  system runs each migration in a transaction and has no dirty flag, so "dirty" means a gap or an
  unknown version.
- **Migration 015** (014 stays byte-identical to the pushed version): blank merchant category and
  store address/phone become NULL. Only rows that change are updated, CHECKs keep blanks out, the
  changed values are saved so down is exact, and the 014 down losses are written in its comments.
- **Not possible without editing 014:** the "newest non-blank per column" backfill. 014 has already
  dropped `receipts.store_*`, so 015 has nothing left to backfill from. 015 does clean up any
  blanks that 014 copied.
- **PR #8 follow-ups:** the backup wait fails closed (kubectl error or 30-minute timeout) and also
  waits for manual `receipts-pg-backup-*` jobs. The manual backup step uses `kubectl wait` with a
  15-minute timeout. The pre-flight checks a default StorageClass and that ESO serves
  `external-secrets.io/v1beta1`.

## Phase 7.1 follow-ups (after PR #9 merged)
- **Migration 016:** `go_is_blank(text)` uses the same whitespace set as Go's `unicode.IsSpace`
  and `strings.TrimSpace`. Values in merchants.category and stores.address/phone that are blank by
  that rule become NULL. Only rows that change are updated, and the replaced values are saved in
  `migration_016_blanks`. 015's CHECKs are replaced with `go_is_blank` ones. Down is exact and
  restores 015's CHECKs.
- **updated_at:** 015 down and 016 down move `updated_at` on the rows they restore, because the
  trigger sets now() and the old timestamps aren't saved. This is documented in the README and in
  016's comments.
- **DEPLOY restore:** the wait is now a `receipts_wait_backups` function, and step 2 is a single
  `&&` chain, so a STOP (kubectl error or timeout) stops the scale-down and the restore pod.
- **Tests:** older migration tests now roll back *to* a version, using `rollbackTo`, instead of a
  fixed number of steps.
