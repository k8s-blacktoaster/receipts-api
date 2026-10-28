-- UPCs: 8, 12, 13 or 14 digits (UPC-E/EAN-8, UPC-A, EAN-13, GTIN-14), unique per merchant, and
-- matched per merchant through product_aliases.upc.
--
-- Existing data: products.upc values that are not a valid UPC are cleared (they could never
-- match a validated line). Valid products.upc values are copied to the product's OLDEST alias
-- at each merchant that has one. If two products of the same merchant share a UPC, only the
-- oldest alias keeps it; the others get NULL (kept, not dropped - same rule as item numbers).
-- products.upc itself stays as a descriptive attribute.
UPDATE products SET upc = NULL WHERE upc IS NOT NULL AND upc !~ '^([0-9]{8}|[0-9]{12,14})$';
ALTER TABLE products ADD CONSTRAINT products_upc_format CHECK (upc ~ '^([0-9]{8}|[0-9]{12,14})$');

ALTER TABLE product_aliases ADD COLUMN upc TEXT
    CONSTRAINT product_aliases_upc_format CHECK (upc ~ '^([0-9]{8}|[0-9]{12,14})$');
UPDATE product_aliases a SET upc = d.upc FROM (
    SELECT a.id, p.upc,
           row_number() OVER (PARTITION BY a.merchant_id, a.product_id ORDER BY a.id) AS rn_prod
    FROM product_aliases a JOIN products p ON p.id = a.product_id
    WHERE p.upc IS NOT NULL
) d WHERE d.id = a.id AND d.rn_prod = 1;
UPDATE product_aliases a SET upc = NULL FROM (
    SELECT id, row_number() OVER (PARTITION BY merchant_id, upc ORDER BY id) AS rn
    FROM product_aliases WHERE upc IS NOT NULL
) d WHERE d.id = a.id AND d.rn > 1;
CREATE UNIQUE INDEX product_aliases_merchant_upc_uniq ON product_aliases (merchant_id, upc) WHERE upc IS NOT NULL;
CREATE INDEX idx_receipt_items_upc_unmapped ON receipt_items (upc) WHERE upc IS NOT NULL AND product_id IS NULL;
