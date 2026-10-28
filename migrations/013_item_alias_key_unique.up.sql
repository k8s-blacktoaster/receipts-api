-- One item alias per (merchant, item_alias_key(alias_text)). Nothing references product_aliases
-- by id, so deduping only has to keep the data: the OLDEST row per key survives; it takes over
-- an item_number / upc it lacks from a duplicate (unique keys allow that since the duplicate
-- is deleted first); other duplicates' codes are dropped with their rows.
CREATE TEMP TABLE _alias_dups ON COMMIT DROP AS
SELECT id, keep_id FROM (
    SELECT id, first_value(id) OVER w AS keep_id, row_number() OVER w AS rn
    FROM product_aliases
    WINDOW w AS (PARTITION BY merchant_id, item_alias_key(alias_text) ORDER BY id)
) d WHERE rn > 1;
CREATE TEMP TABLE _alias_codes ON COMMIT DROP AS
SELECT d.keep_id,
       (array_agg(a.item_number ORDER BY a.id) FILTER (WHERE a.item_number IS NOT NULL))[1] AS item_number,
       (array_agg(a.upc ORDER BY a.id) FILTER (WHERE a.upc IS NOT NULL))[1] AS upc
FROM _alias_dups d JOIN product_aliases a ON a.id = d.id GROUP BY d.keep_id;
DELETE FROM product_aliases WHERE id IN (SELECT id FROM _alias_dups);
UPDATE product_aliases k SET item_number = COALESCE(k.item_number, c.item_number), upc = COALESCE(k.upc, c.upc)
FROM _alias_codes c WHERE c.keep_id = k.id;

DROP INDEX IF EXISTS idx_product_aliases_merchant_text_key;
CREATE UNIQUE INDEX product_aliases_merchant_text_key_uniq ON product_aliases (merchant_id, item_alias_key(alias_text));
