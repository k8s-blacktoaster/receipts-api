-- Deduplicated aliases are not restored.
DROP INDEX IF EXISTS product_aliases_merchant_text_key_uniq;
CREATE INDEX idx_product_aliases_merchant_text_key ON product_aliases (merchant_id, item_alias_key(alias_text));
