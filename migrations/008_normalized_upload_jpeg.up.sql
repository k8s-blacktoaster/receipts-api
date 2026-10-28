-- 008: uploads are stored as one normalized JPEG (object_key, mime_type image/jpeg).
-- sha256 stays the hash of the ORIGINAL bytes (duplicate detection); jpeg_sha256 is the hash of
-- the stored JPEG. original_* are only set when KEEP_ORIGINAL_UPLOADS=true.
ALTER TABLE source_documents
    ADD COLUMN jpeg_sha256        BYTEA,
    ADD COLUMN original_object_key TEXT,
    ADD COLUMN original_mime_type  TEXT,
    ADD COLUMN image_width         INT,
    ADD COLUMN image_height        INT;
COMMENT ON COLUMN source_documents.sha256 IS 'sha256 of the original uploaded bytes (dedupe key)';
