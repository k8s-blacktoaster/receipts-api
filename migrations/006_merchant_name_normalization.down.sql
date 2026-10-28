-- Reverse of 006. Merged merchants are not un-merged and collapsed whitespace is not restored (lossy, accepted).
ALTER TABLE merchants DROP CONSTRAINT merchants_name_normalized;
DROP INDEX merchants_name_uniq;
CREATE UNIQUE INDEX merchants_name_uniq ON merchants (lower(btrim(name)));
ALTER TABLE merchants ADD CONSTRAINT merchants_name_trimmed CHECK (name = btrim(name) AND name <> '');
