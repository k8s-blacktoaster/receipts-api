-- Phase 4 (NORM-01/02): how each line was matched to a canonical product, plus a fuzzy
-- suggestion for unmapped lines (the review queue). pg_trgm itself landed in 006.
ALTER TABLE receipt_items
    ADD COLUMN match_source TEXT
        CHECK (match_source IN ('item_number','upc','exact','fuzzy','llm','manual')),
    ADD COLUMN match_confidence NUMERIC(4,3)
        CHECK (match_confidence IS NULL OR (match_confidence >= 0 AND match_confidence <= 1)),
    ADD COLUMN suggested_product_id BIGINT REFERENCES products(id) ON DELETE SET NULL;

-- Lines mapped before this migration were mapped by hand (no matcher existed).
UPDATE receipt_items SET match_source = 'manual', match_confidence = 1 WHERE product_id IS NOT NULL;

-- Review queue: unmapped lines.
CREATE INDEX idx_receipt_items_unmapped ON receipt_items (receipt_id) WHERE product_id IS NULL;
-- Fuzzy suggestions / product search.
CREATE INDEX idx_product_aliases_text_trgm ON product_aliases USING gin (lower(alias_text) gin_trgm_ops);
CREATE INDEX idx_products_name_trgm ON products USING gin (lower(canonical_name) gin_trgm_ops);
