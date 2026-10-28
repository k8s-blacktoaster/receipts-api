-- 003: merchant-agnostic core model
-- * money as BIGINT cents (no floats / no DECIMAL in app code)
-- * total vs subtotal, tax, savings
-- * merchants (chain) + stores (locations)
-- * line items: line_no, raw_text, optional sku / item_number / upc, fractional qty, discounts
-- * canonical products + merchant-scoped aliases
-- * source documents (image reference, raw extraction, parse state) + review state
-- * nullable household member
-- Analytics views from 002 are rebuilt on the new columns (same names/columns + new ones).

DROP VIEW IF EXISTS spending_trends_view;
DROP VIEW IF EXISTS recent_purchases_view;
DROP VIEW IF EXISTS item_purchase_history_view;
DROP VIEW IF EXISTS store_spending_view;
DROP VIEW IF EXISTS receipt_details_view;

CREATE TABLE household_members (
    id         BIGSERIAL PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE,
    email      TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE merchants (
    id         BIGSERIAL PRIMARY KEY,
    name       TEXT NOT NULL,
    category   TEXT,
    metadata   JSONB NOT NULL DEFAULT '{}'::jsonb,   -- generic, merchant-neutral
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX merchants_name_uniq ON merchants (lower(name));

CREATE TABLE stores (
    id           BIGSERIAL PRIMARY KEY,
    merchant_id  BIGINT NOT NULL REFERENCES merchants(id) ON DELETE RESTRICT,
    store_number TEXT,
    name         TEXT,
    address      TEXT,
    city         TEXT,
    region       TEXT,
    postal_code  TEXT,
    phone        TEXT,
    metadata     JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX stores_merchant_number_uniq ON stores (merchant_id, store_number) WHERE store_number IS NOT NULL;
CREATE INDEX idx_stores_merchant ON stores (merchant_id);

CREATE TABLE source_documents (
    id              BIGSERIAL PRIMARY KEY,
    kind            TEXT NOT NULL CHECK (kind IN ('photo','email','pdf','import','manual')),
    object_key      TEXT,
    sha256          BYTEA UNIQUE,
    mime_type       TEXT,
    uploaded_by_id  BIGINT REFERENCES household_members(id) ON DELETE SET NULL,
    raw_extraction  JSONB,
    model           TEXT,
    prompt_version  TEXT,
    parse_state     TEXT NOT NULL DEFAULT 'pending'
        CHECK (parse_state IN ('pending','extracting','parsed','needs_review','failed','duplicate')),
    error           TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE products (
    id             BIGSERIAL PRIMARY KEY,
    canonical_name TEXT NOT NULL,
    brand          TEXT,
    category       TEXT,
    upc            TEXT,
    size_value     NUMERIC(12,3),
    size_unit      TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_products_upc ON products (upc) WHERE upc IS NOT NULL;
CREATE INDEX idx_products_name ON products (lower(canonical_name));

CREATE TABLE product_aliases (
    id          BIGSERIAL PRIMARY KEY,
    product_id  BIGINT NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    merchant_id BIGINT NOT NULL REFERENCES merchants(id) ON DELETE CASCADE,
    alias_text  TEXT NOT NULL,          -- as printed on that merchant's receipts
    item_number TEXT,                   -- merchant's own item / SKU number
    source      TEXT NOT NULL DEFAULT 'manual'
        CHECK (source IN ('item_number','upc','exact','fuzzy','llm','manual')),
    confidence  NUMERIC(4,3),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX product_aliases_merchant_alias_uniq ON product_aliases (merchant_id, lower(alias_text));
CREATE UNIQUE INDEX product_aliases_merchant_item_uniq ON product_aliases (merchant_id, item_number) WHERE item_number IS NOT NULL;
CREATE INDEX idx_product_aliases_product ON product_aliases (product_id);

-- ---- receipts ---------------------------------------------------------------
ALTER TABLE receipts
    ADD COLUMN merchant_id         BIGINT REFERENCES merchants(id) ON DELETE RESTRICT,
    ADD COLUMN store_id            BIGINT REFERENCES stores(id) ON DELETE SET NULL,
    ADD COLUMN source_document_id  BIGINT REFERENCES source_documents(id) ON DELETE SET NULL,
    ADD COLUMN household_member_id BIGINT REFERENCES household_members(id) ON DELETE SET NULL,
    ADD COLUMN purchased_at        TIMESTAMPTZ,
    ADD COLUMN subtotal_cents      BIGINT,
    ADD COLUMN tax_cents           BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN total_cents         BIGINT,
    ADD COLUMN savings_cents       BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN currency            CHAR(3) NOT NULL DEFAULT 'USD',
    ADD COLUMN payment_method      TEXT,
    ADD COLUMN payment_last4       TEXT,
    ADD COLUMN confidence          NUMERIC(4,3),
    ADD COLUMN review_state        TEXT NOT NULL DEFAULT 'auto'
        CHECK (review_state IN ('auto','needs_review','reviewed')),
    ADD COLUMN source              TEXT NOT NULL DEFAULT 'photo' CHECK (source IN ('photo','manual')),
    ADD COLUMN external_id         TEXT,
    ADD COLUMN metadata            JSONB NOT NULL DEFAULT '{}'::jsonb;

-- data conversion: legacy subtotal (dollars) -> cents; legacy rows had no tax, so total = subtotal
-- legacy rows were entered by hand
UPDATE receipts SET source = 'manual';
UPDATE receipts SET subtotal_cents = round(subtotal * 100)::bigint,
                    total_cents    = round(subtotal * 100)::bigint;

-- backfill merchants / stores from legacy store_* columns
INSERT INTO merchants (name, category)
SELECT DISTINCT ON (lower(store_name)) store_name, store_category
FROM receipts ORDER BY lower(store_name), id
ON CONFLICT DO NOTHING;

INSERT INTO stores (merchant_id, address, phone)
SELECT DISTINCT m.id, r.store_address, r.store_phone
FROM receipts r JOIN merchants m ON lower(m.name) = lower(r.store_name);

UPDATE receipts r SET merchant_id = m.id
FROM merchants m WHERE lower(m.name) = lower(r.store_name);

UPDATE receipts r SET store_id = s.id
FROM stores s
WHERE s.merchant_id = r.merchant_id
  AND s.address IS NOT DISTINCT FROM r.store_address
  AND s.phone   IS NOT DISTINCT FROM r.store_phone;

ALTER TABLE receipts
    DROP COLUMN subtotal,
    ALTER COLUMN subtotal_cents SET NOT NULL,
    ALTER COLUMN subtotal_cents SET DEFAULT 0,
    ALTER COLUMN total_cents SET NOT NULL,
    ALTER COLUMN total_cents SET DEFAULT 0,
    ALTER COLUMN merchant_id SET NOT NULL;


CREATE INDEX idx_receipts_merchant_date ON receipts (merchant_id, date DESC);
CREATE INDEX idx_receipts_store_id_date    ON receipts (store_id, date DESC);
CREATE INDEX idx_receipts_member        ON receipts (household_member_id) WHERE household_member_id IS NOT NULL;
CREATE INDEX idx_receipts_review_state  ON receipts (review_state) WHERE review_state <> 'auto';

-- ---- receipt_items ----------------------------------------------------------
ALTER TABLE receipt_items
    ADD COLUMN line_no          INT,
    ADD COLUMN raw_text         TEXT,
    ADD COLUMN item_number      TEXT,
    ADD COLUMN upc              TEXT,
    ADD COLUMN product_id       BIGINT REFERENCES products(id) ON DELETE SET NULL,
    ADD COLUMN unit             TEXT,
    ADD COLUMN unit_price_cents BIGINT,
    ADD COLUMN line_total_cents BIGINT,
    ADD COLUMN discount_cents   BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN taxable          BOOLEAN,
    ADD COLUMN tax_flag         TEXT,
    ADD COLUMN is_return        BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN confidence       NUMERIC(4,3),
    ADD COLUMN metadata         JSONB NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE receipt_items ALTER COLUMN quantity TYPE NUMERIC(12,3) USING quantity::numeric;

UPDATE receipt_items SET unit_price_cents = round(unit_price * 100)::bigint,
                         line_total_cents = round(quantity * unit_price * 100)::bigint;

ALTER TABLE receipt_items
    DROP COLUMN unit_price,
    ALTER COLUMN unit_price_cents SET NOT NULL,
    ALTER COLUMN unit_price_cents SET DEFAULT 0,
    ALTER COLUMN line_total_cents SET NOT NULL,
    ALTER COLUMN line_total_cents SET DEFAULT 0,
    ADD COLUMN net_total_cents BIGINT GENERATED ALWAYS AS (line_total_cents - discount_cents) STORED;

CREATE INDEX idx_receipt_items_product     ON receipt_items (product_id, receipt_id) WHERE product_id IS NOT NULL;
CREATE INDEX idx_receipt_items_item_number ON receipt_items (item_number) WHERE item_number IS NOT NULL;
CREATE INDEX idx_receipt_items_upc         ON receipt_items (upc) WHERE upc IS NOT NULL;

-- ---- adjustments (coupons/instant savings not on a line, deposits, fees) -----
CREATE TABLE receipt_adjustments (
    id                 BIGSERIAL PRIMARY KEY,
    receipt_id         INTEGER NOT NULL REFERENCES receipts(id) ON DELETE CASCADE,
    applies_to_item_id INTEGER REFERENCES receipt_items(id) ON DELETE SET NULL,
    kind               TEXT NOT NULL CHECK (kind IN ('instant_savings','coupon','discount','deposit','fee','tip','other')),
    raw_text           TEXT,
    amount_cents       BIGINT NOT NULL,   -- negative = reduces total
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_receipt_adjustments_receipt ON receipt_adjustments (receipt_id);

CREATE TRIGGER update_merchants_updated_at BEFORE UPDATE ON merchants FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE TRIGGER update_stores_updated_at BEFORE UPDATE ON stores FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE TRIGGER update_products_updated_at BEFORE UPDATE ON products FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
CREATE TRIGGER update_source_documents_updated_at BEFORE UPDATE ON source_documents FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- ---- views (002 names kept; money exposed in dollars for compat + cents) -----
CREATE VIEW receipt_details_view AS
SELECT r.id AS receipt_id, r.store_name, r.store_category, r.store_address, r.store_phone,
       r.date AS receipt_date,
       (r.subtotal_cents / 100.0)::numeric(12,2) AS receipt_subtotal,
       (r.total_cents / 100.0)::numeric(12,2)    AS receipt_total,
       (r.tax_cents / 100.0)::numeric(12,2)      AS receipt_tax,
       r.merchant_id, r.store_id,
       ri.id AS item_id, ri.sku, ri.item_number, ri.upc, ri.product_id,
       ri.name AS item_name, ri.description AS item_description, ri.quantity,
       (ri.unit_price_cents / 100.0)::numeric(12,2) AS unit_price,
       (ri.net_total_cents / 100.0)::numeric(12,2)  AS item_total,
       r.created_at AS receipt_created_at
FROM receipts r LEFT JOIN receipt_items ri ON r.id = ri.receipt_id
ORDER BY r.date DESC, r.id, ri.id;

CREATE VIEW store_spending_view AS
SELECT m.name AS store_name, m.category AS store_category, r.merchant_id,
       COUNT(DISTINCT r.id) AS total_visits,
       (SUM(r.total_cents) / 100.0)::numeric(12,2) AS total_spent,
       (AVG(r.total_cents) / 100.0)::numeric(12,2) AS avg_per_visit,
       MIN(r.date) AS first_visit, MAX(r.date) AS last_visit,
       COUNT(CASE WHEN r.date >= CURRENT_DATE - INTERVAL '30 days' THEN 1 END) AS visits_last_30_days,
       (COALESCE(SUM(CASE WHEN r.date >= CURRENT_DATE - INTERVAL '30 days' THEN r.total_cents END), 0) / 100.0)::numeric(12,2) AS spent_last_30_days,
       COUNT(CASE WHEN r.date >= CURRENT_DATE - INTERVAL '90 days' THEN 1 END) AS visits_last_90_days,
       (COALESCE(SUM(CASE WHEN r.date >= CURRENT_DATE - INTERVAL '90 days' THEN r.total_cents END), 0) / 100.0)::numeric(12,2) AS spent_last_90_days
FROM receipts r JOIN merchants m ON m.id = r.merchant_id
GROUP BY m.name, m.category, r.merchant_id
ORDER BY total_spent DESC;

CREATE VIEW item_purchase_history_view AS
SELECT ri.sku, ri.item_number, ri.upc, ri.product_id,
       ri.name AS item_name, ri.description AS item_description,
       m.name AS store_name, m.category AS store_category, r.store_id,
       r.date AS purchase_date, ri.quantity,
       (ri.unit_price_cents / 100.0)::numeric(12,2) AS unit_price,
       (ri.net_total_cents / 100.0)::numeric(12,2)  AS total_paid,
       r.id AS receipt_id,
       ROW_NUMBER() OVER (PARTITION BY COALESCE(ri.product_id::text, ri.sku, ri.name) ORDER BY r.date DESC) AS purchase_rank
FROM receipt_items ri JOIN receipts r ON ri.receipt_id = r.id JOIN merchants m ON m.id = r.merchant_id
ORDER BY r.date DESC;

CREATE VIEW recent_purchases_view AS
SELECT r.id AS receipt_id, r.store_name, r.store_category, r.date AS receipt_date,
       (r.subtotal_cents / 100.0)::numeric(12,2) AS subtotal,
       (r.total_cents / 100.0)::numeric(12,2)    AS total,
       COUNT(ri.id) AS item_count,
       STRING_AGG(ri.name, ', ' ORDER BY ri.name) AS items_purchased,
       ROW_NUMBER() OVER (PARTITION BY r.merchant_id ORDER BY r.date DESC) AS visit_rank
FROM receipts r LEFT JOIN receipt_items ri ON r.id = ri.receipt_id
GROUP BY r.id
ORDER BY r.date DESC;

CREATE VIEW spending_trends_view AS
SELECT DATE_TRUNC('month', date) AS month, COUNT(DISTINCT id) AS receipt_count,
       (SUM(total_cents) / 100.0)::numeric(12,2) AS total_spent,
       (AVG(total_cents) / 100.0)::numeric(12,2) AS avg_per_receipt,
       COUNT(DISTINCT merchant_id) AS unique_stores,
       (MIN(total_cents) / 100.0)::numeric(12,2) AS min_receipt,
       (MAX(total_cents) / 100.0)::numeric(12,2) AS max_receipt,
       (SUM(tax_cents) / 100.0)::numeric(12,2) AS total_tax,
       (SUM(savings_cents) / 100.0)::numeric(12,2) AS total_savings
FROM receipts GROUP BY DATE_TRUNC('month', date) ORDER BY month DESC;

-- "where did I buy X": product -> merchant + store location + date + price
CREATE VIEW product_purchase_locations_view AS
SELECT p.id AS product_id, p.canonical_name, m.id AS merchant_id, m.name AS merchant_name,
       s.id AS store_id, s.store_number, s.name AS store_location_name, s.address AS store_address,
       r.id AS receipt_id, r.date AS purchase_date, r.purchased_at,
       ri.id AS item_id, ri.quantity, ri.unit_price_cents, ri.net_total_cents
FROM receipt_items ri
JOIN products p  ON p.id = ri.product_id
JOIN receipts r  ON r.id = ri.receipt_id
JOIN merchants m ON m.id = r.merchant_id
LEFT JOIN stores s ON s.id = r.store_id
WHERE NOT ri.is_return;
