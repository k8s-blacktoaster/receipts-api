-- Restore the legacy receipts.store_* columns, refilled from the normalized tables (the
-- receipt's merchant category and store address/phone). Values that differed per receipt
-- before 014 are not recoverable; the normalized values are what every reader used since 005.
COMMENT ON COLUMN receipts.printed_merchant_name IS NULL;
ALTER TABLE receipts RENAME COLUMN printed_merchant_name TO store_name;
ALTER TABLE receipts
    ADD COLUMN store_category VARCHAR(255),
    ADD COLUMN store_address TEXT,
    ADD COLUMN store_phone VARCHAR(50);

UPDATE receipts r SET store_category = m.category
FROM merchants m WHERE m.id = r.merchant_id AND m.category IS NOT NULL;
UPDATE receipts r SET store_address = s.address, store_phone = s.phone
FROM stores s WHERE s.id = r.store_id;

CREATE INDEX IF NOT EXISTS idx_receipts_store_name ON receipts(store_name);
CREATE INDEX IF NOT EXISTS idx_receipts_store_date ON receipts(store_name, date DESC);
