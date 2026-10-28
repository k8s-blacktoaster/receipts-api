-- 006: merchant names collapse internal whitespace ('Trader  Joes' = 'Trader Joes') and merged
-- duplicates keep a properly cased display name.
-- Normalized key: lower(regexp_replace(btrim(name), '\s+', ' ', 'g')).
-- Display-name choice per key: most common variant (weighted by referencing receipts, min 1 per
-- merchant row); tie -> a variant containing uppercase letters; tie -> lowest merchant id.

DROP INDEX IF EXISTS merchants_name_uniq;
ALTER TABLE merchants DROP CONSTRAINT IF EXISTS merchants_name_trimmed;
UPDATE merchants SET name = regexp_replace(btrim(name), '\s+', ' ', 'g')
 WHERE name <> regexp_replace(btrim(name), '\s+', ' ', 'g');

CREATE TEMP TABLE _merchant_map ON COMMIT DROP AS
SELECT id, min(id) OVER (PARTITION BY lower(name)) AS keep_id FROM merchants;

CREATE TEMP TABLE _merchant_display ON COMMIT DROP AS
SELECT DISTINCT ON (keep_id) keep_id, name
FROM (
    SELECT mm.keep_id, m.name, m.id,
           sum(GREATEST((SELECT count(*) FROM receipts r WHERE r.merchant_id = m.id), 1))
               OVER (PARTITION BY mm.keep_id, m.name) AS uses
    FROM merchants m JOIN _merchant_map mm ON mm.id = m.id
) v
ORDER BY keep_id, uses DESC, (name <> lower(name)) DESC, id;

DELETE FROM _merchant_map WHERE id = keep_id;
-- receipts on a duplicate store: move store + merchant together (composite FK)
UPDATE receipts r SET store_id = k.id, merchant_id = m.keep_id
  FROM stores s JOIN _merchant_map m ON s.merchant_id = m.id
  JOIN stores k ON k.merchant_id = m.keep_id
   AND ((s.store_number IS NOT NULL AND k.store_number = s.store_number)
     OR (s.store_number IS NULL AND k.store_number IS NULL AND lower(k.address) = lower(s.address)))
 WHERE r.store_id = s.id;
DELETE FROM stores s USING _merchant_map m, stores k
 WHERE s.merchant_id = m.id AND k.merchant_id = m.keep_id
   AND ((s.store_number IS NOT NULL AND k.store_number = s.store_number)
     OR (s.store_number IS NULL AND k.store_number IS NULL AND lower(k.address) = lower(s.address)));
-- remaining stores move to the kept merchant (ON UPDATE CASCADE carries their receipts)
UPDATE stores s SET merchant_id = m.keep_id FROM _merchant_map m WHERE s.merchant_id = m.id;
UPDATE receipts r SET merchant_id = m.keep_id FROM _merchant_map m WHERE r.merchant_id = m.id;
DELETE FROM product_aliases a USING _merchant_map m, product_aliases k
 WHERE a.merchant_id = m.id AND k.merchant_id = m.keep_id AND lower(k.alias_text) = lower(a.alias_text);
UPDATE product_aliases a SET merchant_id = m.keep_id FROM _merchant_map m WHERE a.merchant_id = m.id;
DELETE FROM merchants WHERE id IN (SELECT id FROM _merchant_map);
UPDATE merchants m SET name = d.name FROM _merchant_display d WHERE d.keep_id = m.id AND m.name <> d.name;

CREATE UNIQUE INDEX merchants_name_uniq ON merchants (lower(regexp_replace(btrim(name), '\s+', ' ', 'g')));
ALTER TABLE merchants ADD CONSTRAINT merchants_name_normalized
    CHECK (name = regexp_replace(btrim(name), '\s+', ' ', 'g') AND name <> '');
