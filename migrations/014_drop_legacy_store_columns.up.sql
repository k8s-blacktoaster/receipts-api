-- Phase 7: drop the legacy receipts.store_* columns. Every reader now uses merchants/stores
-- (merchant_id, store_id). The printed merchant name is still needed (edits keep merchant_id
-- when it is unchanged), so store_name is kept under a clearer name: printed_merchant_name.
--
-- Before dropping, copy anything the normalized tables lack (never overwrite):
--   store_category -> merchants.category, store_address/store_phone -> stores.address/phone.
-- A receipt with an address/phone but no store row (no store number and no address) loses
-- its phone; the app has only ever created such rows from manual edits.
UPDATE merchants m SET category = c.store_category
FROM (SELECT DISTINCT ON (merchant_id) merchant_id, store_category
      FROM receipts WHERE merchant_id IS NOT NULL AND NULLIF(btrim(store_category), '') IS NOT NULL
      ORDER BY merchant_id, id) c
WHERE m.id = c.merchant_id AND m.category IS NULL;

UPDATE stores s SET address = COALESCE(s.address, c.store_address), phone = COALESCE(s.phone, c.store_phone)
FROM (SELECT DISTINCT ON (store_id) store_id, NULLIF(btrim(store_address), '') AS store_address,
             NULLIF(btrim(store_phone), '') AS store_phone
      FROM receipts WHERE store_id IS NOT NULL ORDER BY store_id, id DESC) c
WHERE s.id = c.store_id AND (s.address IS NULL OR s.phone IS NULL);

DROP INDEX IF EXISTS idx_receipts_store_name;
DROP INDEX IF EXISTS idx_receipts_store_date;

ALTER TABLE receipts RENAME COLUMN store_name TO printed_merchant_name;
ALTER TABLE receipts
    DROP COLUMN store_category,
    DROP COLUMN store_address,
    DROP COLUMN store_phone;

COMMENT ON COLUMN receipts.printed_merchant_name IS
    'Merchant name as printed/entered (was store_name). merchant_id is the normalized link.';
