-- Blank = empty after Go's strings.TrimSpace, i.e. only unicode.IsSpace runes: tab, LF, VT, FF,
-- CR, space, U+0085, U+00A0 (NBSP), U+1680, U+2000-U+200A, U+2028, U+2029, U+202F, U+205F,
-- U+3000. 015 only trimmed ASCII spaces, so e.g. a tab or NBSP-only category survived it.
-- This turns such merchants.category / stores.address / stores.phone values into NULL and
-- replaces 015's CHECKs with go_is_blank() ones. Parity with Go is tested
-- (TestGoBlankParity). Only rows that change are updated; replaced values are saved in
-- migration_016_blanks so down restores them exactly.
--
-- updated_at: the BEFORE UPDATE triggers set updated_at = now() on every updated row. Up only
-- touches rows it changes. Note that 015 down (and this down) also bump updated_at on the rows
-- they restore - the old timestamps are not saved.
CREATE FUNCTION go_is_blank(s text) RETURNS boolean
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT btrim(s, E'\u0009\u000A\u000B\u000C\u000D\u0020\u0085\u00A0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200A\u2028\u2029\u202F\u205F\u3000') = ''
$$;

CREATE TABLE migration_016_blanks (
    tbl  TEXT   NOT NULL,
    id   BIGINT NOT NULL,
    col  TEXT   NOT NULL,
    val  TEXT   NOT NULL,
    PRIMARY KEY (tbl, id, col)
);

INSERT INTO migration_016_blanks (tbl, id, col, val)
SELECT 'merchants', id, 'category', category FROM merchants WHERE go_is_blank(category)
UNION ALL
SELECT 'stores', id, 'address', address FROM stores WHERE go_is_blank(address)
UNION ALL
SELECT 'stores', id, 'phone', phone FROM stores WHERE go_is_blank(phone);

UPDATE merchants SET category = NULL WHERE go_is_blank(category);
UPDATE stores SET address = CASE WHEN go_is_blank(address) THEN NULL ELSE address END,
                  phone   = CASE WHEN go_is_blank(phone) THEN NULL ELSE phone END
WHERE go_is_blank(address) OR go_is_blank(phone);

ALTER TABLE merchants DROP CONSTRAINT merchants_category_not_blank,
                      ADD CONSTRAINT merchants_category_not_blank CHECK (NOT go_is_blank(category));
ALTER TABLE stores DROP CONSTRAINT stores_address_not_blank,
                   DROP CONSTRAINT stores_phone_not_blank,
                   ADD CONSTRAINT stores_address_not_blank CHECK (NOT go_is_blank(address)),
                   ADD CONSTRAINT stores_phone_not_blank CHECK (NOT go_is_blank(phone));
