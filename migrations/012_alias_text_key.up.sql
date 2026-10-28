-- Conservative item-alias key (Go twin: models.ItemAliasKey): lowercase, trim, collapse runs of
-- ASCII whitespace/NBSP to one space; every punctuation character is kept, so "MILK 1/2 GAL"
-- never equals "MILK 12 GAL". Merchant names keep using merchant_name_key.
CREATE FUNCTION item_alias_key(t text) RETURNS text
LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE AS $fn$
SELECT btrim(regexp_replace(lower(t), '[' || chr(32) || chr(9) || chr(10) || chr(11) || chr(12) || chr(13) || chr(160) || ']+', ' ', 'g'), ' ')
$fn$;
CREATE INDEX idx_product_aliases_merchant_text_key ON product_aliases (merchant_id, item_alias_key(alias_text));
