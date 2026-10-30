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
