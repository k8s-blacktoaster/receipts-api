-- Exact inverse of 015 up: drop the CHECKs and put back the blank values it replaced with NULL
-- (rows whose value was changed since stay as they are now). Only restored rows are updated.
ALTER TABLE stores DROP CONSTRAINT IF EXISTS stores_phone_not_blank,
                   DROP CONSTRAINT IF EXISTS stores_address_not_blank;
ALTER TABLE merchants DROP CONSTRAINT IF EXISTS merchants_category_not_blank;

UPDATE merchants m SET category = b.val
FROM migration_015_blanks b
WHERE b.tbl = 'merchants' AND b.col = 'category' AND b.id = m.id AND m.category IS NULL;
UPDATE stores s SET address = b.val
FROM migration_015_blanks b
WHERE b.tbl = 'stores' AND b.col = 'address' AND b.id = s.id AND s.address IS NULL;
UPDATE stores s SET phone = b.val
FROM migration_015_blanks b
WHERE b.tbl = 'stores' AND b.col = 'phone' AND b.id = s.id AND s.phone IS NULL;

DROP TABLE migration_015_blanks;
