-- Exact inverse of 016 up: back to 015's ASCII-space CHECKs, restore the values 016 replaced
-- with NULL (rows changed since keep their current value), drop the helper and the backup.
-- The restored rows get updated_at = now() from the update trigger (like 015 down).
ALTER TABLE stores DROP CONSTRAINT stores_phone_not_blank,
                   DROP CONSTRAINT stores_address_not_blank;
ALTER TABLE merchants DROP CONSTRAINT merchants_category_not_blank;

UPDATE merchants m SET category = b.val
FROM migration_016_blanks b
WHERE b.tbl = 'merchants' AND b.col = 'category' AND b.id = m.id AND m.category IS NULL;
UPDATE stores s SET address = b.val
FROM migration_016_blanks b
WHERE b.tbl = 'stores' AND b.col = 'address' AND b.id = s.id AND s.address IS NULL;
UPDATE stores s SET phone = b.val
FROM migration_016_blanks b
WHERE b.tbl = 'stores' AND b.col = 'phone' AND b.id = s.id AND s.phone IS NULL;

ALTER TABLE merchants ADD CONSTRAINT merchants_category_not_blank CHECK (btrim(category) <> '');
ALTER TABLE stores ADD CONSTRAINT stores_address_not_blank CHECK (btrim(address) <> ''),
                   ADD CONSTRAINT stores_phone_not_blank CHECK (btrim(phone) <> '');

DROP TABLE migration_016_blanks;
DROP FUNCTION go_is_blank(text);
