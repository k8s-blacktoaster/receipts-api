-- Reverse of 006. LOSSY: merged merchants are not un-merged and original spellings are not
-- restored (names stay in normalized key form, which also satisfies the 005 constraints).
-- pg_trgm extension is intentionally left installed (shared, harmless; later phases use it).
DROP INDEX IF EXISTS idx_merchants_name_trgm;
ALTER TABLE merchants DROP CONSTRAINT IF EXISTS merchants_name_normalized;
DROP INDEX IF EXISTS merchants_name_uniq;
ALTER TABLE merchants DROP COLUMN IF EXISTS name_is_derived; -- from pre-release 006 drafts
DROP FUNCTION IF EXISTS merchant_name_key(text);
CREATE UNIQUE INDEX merchants_name_uniq ON merchants (lower(btrim(name)));
ALTER TABLE merchants ADD CONSTRAINT merchants_name_trimmed CHECK (name = btrim(name) AND name <> '');
