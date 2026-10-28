-- Invalid products.upc values cleared by the up migration are not restored.
DROP INDEX IF EXISTS idx_receipt_items_upc_unmapped;
DROP INDEX IF EXISTS product_aliases_merchant_upc_uniq;
ALTER TABLE product_aliases DROP COLUMN IF EXISTS upc;
ALTER TABLE products DROP CONSTRAINT IF EXISTS products_upc_format;
