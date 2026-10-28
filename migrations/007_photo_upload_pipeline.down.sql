-- Reverse of 007. LOSSY: receipts that never got a merchant/date (pending/failed photos) are deleted.
ALTER TABLE source_documents DROP COLUMN parsed_at;
DROP INDEX IF EXISTS idx_receipts_pending;
DROP INDEX IF EXISTS receipts_source_document_uniq;
ALTER TABLE receipts DROP CONSTRAINT receipts_complete_when_parsed;
DELETE FROM receipts WHERE merchant_id IS NULL OR date IS NULL;
ALTER TABLE receipts ALTER COLUMN date SET NOT NULL;
ALTER TABLE receipts ALTER COLUMN merchant_id SET NOT NULL;
