-- 004: household members + photo-pipeline parse/review status
-- household_members (003) becomes a proper members table.
ALTER TABLE household_members RENAME TO members;
ALTER TABLE members RENAME COLUMN name TO display_name;
ALTER TABLE members ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT now();
ALTER INDEX household_members_pkey RENAME TO members_pkey;
ALTER INDEX household_members_name_key RENAME TO members_display_name_key;
ALTER SEQUENCE household_members_id_seq RENAME TO members_id_seq;
CREATE UNIQUE INDEX members_email_uniq ON members (lower(email)) WHERE email IS NOT NULL;
CREATE TRIGGER update_members_updated_at BEFORE UPDATE ON members
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- who the purchase belongs to, and who uploaded the photo (both nullable: legacy rows
-- and unattributed uploads have no member; NOT NULL can be added once data is backfilled)
ALTER TABLE receipts RENAME COLUMN household_member_id TO member_id;
ALTER INDEX idx_receipts_member RENAME TO idx_receipts_member_id;
ALTER TABLE receipts RENAME CONSTRAINT receipts_household_member_id_fkey TO receipts_member_id_fkey;
ALTER TABLE receipts ADD COLUMN uploaded_by_id BIGINT REFERENCES members(id) ON DELETE SET NULL;

-- review_state (auto|needs_review|reviewed) -> status covering the photo pipeline
ALTER TABLE receipts ADD COLUMN status TEXT NOT NULL DEFAULT 'uploaded'
    CHECK (status IN ('uploaded','parsing','parsed','needs_review','reviewed','failed'));
UPDATE receipts SET status = CASE review_state
    WHEN 'auto' THEN 'parsed' WHEN 'needs_review' THEN 'needs_review' ELSE 'reviewed' END;
DROP INDEX IF EXISTS idx_receipts_review_state;
ALTER TABLE receipts DROP COLUMN review_state;
ALTER TABLE receipts
    ADD COLUMN parse_error TEXT,
    ADD COLUMN parse_started_at TIMESTAMPTZ,
    ADD COLUMN parsed_at TIMESTAMPTZ,
    ADD COLUMN reviewed_at TIMESTAMPTZ,
    ADD COLUMN reviewed_by_id BIGINT REFERENCES members(id) ON DELETE SET NULL;
CREATE INDEX idx_receipts_status ON receipts (status, created_at DESC);

-- line items edited during review
ALTER TABLE receipt_items ADD COLUMN edited_at TIMESTAMPTZ;
