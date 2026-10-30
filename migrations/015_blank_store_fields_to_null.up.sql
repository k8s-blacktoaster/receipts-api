-- Follow-up to 014 (014 is pushed and must not change). Blank strings are "missing":
-- merchants.category and stores.address/phone that are '' or whitespace become NULL, and CHECKs
-- keep them out from now on. Only rows that actually change are updated, so updated_at (trigger)
-- moves only for those. Each changed value is saved in migration_015_blanks so down restores it.
--
-- Note on 014: its backfill copied the oldest receipt's category / the newest receipt's
-- address+phone even when blank, and ran before this normalization. 014 has already dropped the
-- receipts.store_* columns, so a better per-column backfill (newest non-blank) cannot be redone
-- here; blanks it copied are cleaned up by this migration.
--
-- 014 down is lossy. It restores receipts.store_* from merchants/stores, so it loses:
--   * each receipt's own category/address/phone where it differed from its merchant/store
--     (every receipt gets the shared merchant/store value);
--   * the values of receipts without a merchant (category) or store (address, phone): NULL;
--   * phones of receipts that had no store row (014 up dropped them);
--   * the original blank-vs-NULL distinction.
-- It does not undo 014 up's writes to merchants.category and stores.address/phone.
CREATE TABLE migration_015_blanks (
    tbl  TEXT   NOT NULL,
    id   BIGINT NOT NULL,
    col  TEXT   NOT NULL,
    val  TEXT   NOT NULL,
    PRIMARY KEY (tbl, id, col)
);

INSERT INTO migration_015_blanks (tbl, id, col, val)
SELECT 'merchants', id, 'category', category FROM merchants WHERE category IS NOT NULL AND btrim(category) = ''
UNION ALL
SELECT 'stores', id, 'address', address FROM stores WHERE address IS NOT NULL AND btrim(address) = ''
UNION ALL
SELECT 'stores', id, 'phone', phone FROM stores WHERE phone IS NOT NULL AND btrim(phone) = '';

UPDATE merchants SET category = NULL WHERE category IS NOT NULL AND btrim(category) = '';
UPDATE stores SET address = CASE WHEN btrim(address) = '' THEN NULL ELSE address END,
                  phone   = CASE WHEN btrim(phone) = '' THEN NULL ELSE phone END
WHERE (address IS NOT NULL AND btrim(address) = '') OR (phone IS NOT NULL AND btrim(phone) = '');

ALTER TABLE merchants ADD CONSTRAINT merchants_category_not_blank CHECK (btrim(category) <> '');
ALTER TABLE stores ADD CONSTRAINT stores_address_not_blank CHECK (btrim(address) <> ''),
                   ADD CONSTRAINT stores_phone_not_blank CHECK (btrim(phone) <> '');
