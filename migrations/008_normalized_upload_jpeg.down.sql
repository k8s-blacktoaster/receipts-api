-- Reverse of 008. Kept original files (KEEP_ORIGINAL_UPLOADS=true) stay on disk, orphaned.
COMMENT ON COLUMN source_documents.sha256 IS NULL;
ALTER TABLE source_documents
    DROP COLUMN IF EXISTS image_height,
    DROP COLUMN IF EXISTS image_width,
    DROP COLUMN IF EXISTS original_mime_type,
    DROP COLUMN IF EXISTS original_object_key,
    DROP COLUMN IF EXISTS jpeg_sha256;
