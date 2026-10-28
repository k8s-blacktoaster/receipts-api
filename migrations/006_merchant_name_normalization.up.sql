-- 006: merchant names collapse internal whitespace ('Trader  Joes' = 'Trader Joes') and merged
-- duplicates keep a properly cased display name.
-- Normalized key: lower(regexp_replace(btrim(name), '\s+', ' ', 'g')).
-- Display name: see _merchant_display below.

DROP INDEX IF EXISTS merchants_name_uniq;
ALTER TABLE merchants DROP CONSTRAINT IF EXISTS merchants_name_trimmed;
UPDATE merchants SET name = regexp_replace(btrim(name), '\s+', ' ', 'g')
 WHERE name <> regexp_replace(btrim(name), '\s+', ' ', 'g');

CREATE TEMP TABLE _merchant_map ON COMMIT DROP AS
SELECT id, min(id) OVER (PARTITION BY lower(name)) AS keep_id FROM merchants;

-- Display name per merged group: chosen only among existing variants, i.e. the legacy
-- receipts.store_name values (whitespace-collapsed, one vote per receipt). Merchant rows'
-- own names are a zero-vote fallback for merchants with no receipts. Ranking: most votes;
-- tie -> mixed case (not all-caps, not all-lower); tie -> earliest merchant id; tie -> name.
CREATE TEMP TABLE _merchant_display ON COMMIT DROP AS
SELECT DISTINCT ON (keep_id) keep_id, name
FROM (
    SELECT keep_id, name, sum(votes) AS votes, min(src_id) AS src_id
    FROM (
        SELECT mm.keep_id, regexp_replace(btrim(r.store_name), '\s+', ' ', 'g') AS name, 1 AS votes, mm.id AS src_id
        FROM receipts r JOIN _merchant_map mm ON mm.id = r.merchant_id
        WHERE r.store_name IS NOT NULL AND btrim(r.store_name) <> ''
        UNION ALL
        SELECT mm.keep_id, m.name, 0, m.id
        FROM merchants m JOIN _merchant_map mm ON mm.id = m.id
    ) c
    GROUP BY keep_id, name
) v
ORDER BY keep_id, votes DESC, (name <> lower(name) AND name <> upper(name)) DESC, src_id, name;

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
