-- 007: photo upload + extraction pipeline.
-- A freshly uploaded photo has no merchant/date yet, so merchant_id and date become nullable while
-- the receipt is uploaded/parsing/needs_review/failed. parsed/reviewed receipts must have both.
ALTER TABLE receipts ALTER COLUMN merchant_id DROP NOT NULL;
ALTER TABLE receipts ALTER COLUMN date DROP NOT NULL;
ALTER TABLE receipts ADD CONSTRAINT receipts_complete_when_parsed CHECK (
    (merchant_id IS NOT NULL AND date IS NOT NULL) OR status IN ('uploaded','parsing','needs_review','failed'));
-- one receipt per source document (duplicate photo -> existing receipt)
CREATE UNIQUE INDEX receipts_source_document_uniq ON receipts (source_document_id) WHERE source_document_id IS NOT NULL;
-- the worker picks up pending photos on restart
CREATE INDEX idx_receipts_pending ON receipts (id) WHERE status IN ('uploaded','parsing');
ALTER TABLE source_documents ADD COLUMN parsed_at TIMESTAMPTZ;
