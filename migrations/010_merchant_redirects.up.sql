-- A merchant merged away leaves its name key here, pointing at the survivor, so future
-- receipts printed with the old name resolve to the survivor instead of re-creating it.
CREATE TABLE merchant_name_redirects (
    name_key    TEXT PRIMARY KEY CHECK (name_key <> '' AND name_key = merchant_name_key(name_key)),
    merchant_id BIGINT NOT NULL REFERENCES merchants(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_merchant_name_redirects_merchant ON merchant_name_redirects (merchant_id);
