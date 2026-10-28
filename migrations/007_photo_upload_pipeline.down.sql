-- Reverse of 007. LOSSY: receipts that never got a merchant/date (pending/failed photos) are deleted.
-- Image files under UPLOAD_DIR are NOT removed: a migration cannot touch the filesystem, so the
-- deleted receipts' photos (and their source_documents rows' files) are left orphaned on disk.
-- Clean UPLOAD_DIR by hand if needed (files are content-addressed: receipts/<sha[:2]>/<sha>.<ext>).
ALTER TABLE source_documents DROP COLUMN parsed_at;
DROP INDEX IF EXISTS idx_receipts_pending;
DROP INDEX IF EXISTS receipts_source_document_uniq;
ALTER TABLE receipts DROP CONSTRAINT receipts_complete_when_parsed;
DELETE FROM receipts WHERE merchant_id IS NULL OR date IS NULL;
ALTER TABLE receipts ALTER COLUMN date SET NOT NULL;
ALTER TABLE receipts ALTER COLUMN merchant_id SET NOT NULL;
