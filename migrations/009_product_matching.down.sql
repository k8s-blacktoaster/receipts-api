DROP INDEX IF EXISTS idx_products_name_trgm;
DROP INDEX IF EXISTS idx_product_aliases_text_trgm;
DROP INDEX IF EXISTS idx_receipt_items_unmapped;
ALTER TABLE receipt_items
    DROP COLUMN IF EXISTS suggested_product_id,
    DROP COLUMN IF EXISTS match_confidence,
    DROP COLUMN IF EXISTS match_source;
