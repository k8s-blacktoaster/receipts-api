-- Reverse of 004. uploaded/parsing/parsed/failed collapse to 'auto' (003 had no finer states).
-- ACCEPTED (Jack, 2026-10-27): this rollback is lossy; uploaded/parsing/parsed/failed and
-- parse timestamps/errors are not recoverable after down + up.
ALTER TABLE receipt_items DROP COLUMN edited_at;

DROP INDEX IF EXISTS idx_receipts_status;
ALTER TABLE receipts ADD COLUMN review_state TEXT NOT NULL DEFAULT 'auto'
    CHECK (review_state IN ('auto','needs_review','reviewed'));
UPDATE receipts SET review_state = CASE status
    WHEN 'needs_review' THEN 'needs_review' WHEN 'reviewed' THEN 'reviewed' ELSE 'auto' END;
CREATE INDEX idx_receipts_review_state ON receipts (review_state) WHERE review_state <> 'auto';
ALTER TABLE receipts
    DROP COLUMN status, DROP COLUMN parse_error, DROP COLUMN parse_started_at,
    DROP COLUMN parsed_at, DROP COLUMN reviewed_at, DROP COLUMN reviewed_by_id,
    DROP COLUMN uploaded_by_id;
ALTER INDEX idx_receipts_member_id RENAME TO idx_receipts_member;
ALTER TABLE receipts RENAME CONSTRAINT receipts_member_id_fkey TO receipts_household_member_id_fkey;
ALTER TABLE receipts RENAME COLUMN member_id TO household_member_id;

DROP TRIGGER IF EXISTS update_members_updated_at ON members;
DROP INDEX IF EXISTS members_email_uniq;
ALTER SEQUENCE members_id_seq RENAME TO household_members_id_seq;
ALTER INDEX members_display_name_key RENAME TO household_members_name_key;
ALTER INDEX members_pkey RENAME TO household_members_pkey;
ALTER TABLE members DROP COLUMN updated_at;
ALTER TABLE members RENAME COLUMN display_name TO name;
ALTER TABLE members RENAME TO household_members;
