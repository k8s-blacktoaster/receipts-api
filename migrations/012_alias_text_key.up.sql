-- Exact alias matching compares merchant_name_key(alias_text) (the same normalization as merchant
-- names, Go parity in models.NormalizeMerchantName), so "ORG. BANANA" = "org  banana".
CREATE INDEX idx_product_aliases_merchant_text_key ON product_aliases (merchant_id, merchant_name_key(alias_text));
