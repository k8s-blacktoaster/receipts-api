-- 006: one stored merchant name = normalized key (used for matching AND display).
-- merchant_name_key() must match Go models.NormalizeMerchantName exactly (parity test):
--   lower -> '&' to ' and ' -> drop listed punctuation -> runs of separators to '_' -> strip '_'.
-- Explicit character lists (not locale classes) keep Go and Postgres in agreement.
-- Edited in place before release; only test databases ran earlier versions.
CREATE FUNCTION merchant_name_key(t text) RETURNS text
LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE AS $fn$
SELECT btrim(
         regexp_replace(
           regexp_replace(replace(lower(t), '&', ' and '),
                          '[!"#$%''()*+,./:;<=>?@\[\\\]^`{|}~‘’“”–—…·•®™©]', '', 'g'),
           '[ \t\n\v\f\r _-]+', '_', 'g'),
         '_')
$fn$;

DROP INDEX IF EXISTS merchants_name_uniq;
ALTER TABLE merchants DROP CONSTRAINT IF EXISTS merchants_name_trimmed;

CREATE TEMP TABLE _merchant_map ON COMMIT DROP AS
SELECT id, min(id) OVER (PARTITION BY merchant_name_key(name)) AS keep_id FROM merchants;
-- stores of a merged group dedupe by (store_number) or, without a number, (lower(address))
CREATE TEMP TABLE _store_map ON COMMIT DROP AS
SELECT s.id, m.keep_id AS keep_merchant,
       min(s.id) OVER (PARTITION BY m.keep_id,
           CASE WHEN s.store_number IS NOT NULL THEN 'n:' || s.store_number
                WHEN s.address IS NOT NULL THEN 'a:' || lower(s.address)
                ELSE 'id:' || s.id END) AS keep_store
FROM stores s JOIN _merchant_map m ON m.id = s.merchant_id;
CREATE TEMP TABLE _receipt_store ON COMMIT DROP AS
SELECT r.id AS receipt_id, sm.keep_store
FROM receipts r JOIN _store_map sm ON sm.id = r.store_id WHERE sm.keep_store <> sm.id;
-- detach receipts from duplicate stores, drop those stores, move kept stores
-- (ON UPDATE CASCADE carries their receipts), move all receipts, then re-attach
UPDATE receipts r SET store_id = NULL FROM _receipt_store x WHERE x.receipt_id = r.id;
DELETE FROM stores s USING _store_map sm WHERE sm.id = s.id AND sm.keep_store <> sm.id;
UPDATE stores s SET merchant_id = sm.keep_merchant FROM _store_map sm
 WHERE sm.id = s.id AND s.merchant_id <> sm.keep_merchant;
DELETE FROM _merchant_map WHERE id = keep_id;
UPDATE receipts r SET merchant_id = m.keep_id FROM _merchant_map m WHERE r.merchant_id = m.id;
UPDATE receipts r SET store_id = x.keep_store FROM _receipt_store x WHERE x.receipt_id = r.id;
-- aliases of a merged group: keep the oldest per lower(alias_text) and per item_number
DELETE FROM product_aliases a USING (
    SELECT a.id,
           row_number() OVER (PARTITION BY COALESCE(m.keep_id, a.merchant_id), lower(a.alias_text) ORDER BY a.id) AS rn_alias,
           CASE WHEN a.item_number IS NULL THEN 1 ELSE
           row_number() OVER (PARTITION BY COALESCE(m.keep_id, a.merchant_id), a.item_number ORDER BY a.id) END AS rn_item
    FROM product_aliases a LEFT JOIN _merchant_map m ON m.id = a.merchant_id
    WHERE a.merchant_id IN (SELECT id FROM _merchant_map) OR a.merchant_id IN (SELECT keep_id FROM _merchant_map)
) d WHERE d.id = a.id AND (d.rn_alias > 1 OR d.rn_item > 1);
UPDATE product_aliases a SET merchant_id = m.keep_id FROM _merchant_map m WHERE a.merchant_id = m.id;
DELETE FROM merchants WHERE id IN (SELECT id FROM _merchant_map);

UPDATE merchants SET name = merchant_name_key(name) WHERE name <> merchant_name_key(name);
-- names that normalize to '' (pure punctuation) cannot satisfy the CHECK; keep them visible
UPDATE merchants SET name = 'unknown_' || id WHERE name = '';

CREATE UNIQUE INDEX merchants_name_uniq ON merchants (name);
ALTER TABLE merchants ADD CONSTRAINT merchants_name_normalized
    CHECK (name <> '' AND name = merchant_name_key(name));
