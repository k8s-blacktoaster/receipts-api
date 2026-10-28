-- Reverse of 003: restore DECIMAL dollar columns from cents, drop new tables, restore 002 views.
DROP VIEW IF EXISTS product_purchase_locations_view;
DROP VIEW IF EXISTS spending_trends_view;
DROP VIEW IF EXISTS recent_purchases_view;
DROP VIEW IF EXISTS item_purchase_history_view;
DROP VIEW IF EXISTS store_spending_view;
DROP VIEW IF EXISTS receipt_details_view;

DROP TABLE IF EXISTS receipt_adjustments;

-- receipt_items: cents -> DECIMAL dollars, integer quantity
ALTER TABLE receipt_items DROP COLUMN net_total_cents;
ALTER TABLE receipt_items ADD COLUMN unit_price DECIMAL(10,2) NOT NULL DEFAULT 0.00;
UPDATE receipt_items SET unit_price = (unit_price_cents / 100.0)::numeric(10,2);
ALTER TABLE receipt_items ALTER COLUMN quantity TYPE INTEGER USING GREATEST(round(quantity), 1)::integer;
ALTER TABLE receipt_items ALTER COLUMN quantity SET DEFAULT 1;
DROP INDEX IF EXISTS idx_receipt_items_product;
DROP INDEX IF EXISTS idx_receipt_items_item_number;
DROP INDEX IF EXISTS idx_receipt_items_upc;
ALTER TABLE receipt_items
    DROP COLUMN line_no, DROP COLUMN raw_text, DROP COLUMN item_number, DROP COLUMN upc,
    DROP COLUMN product_id, DROP COLUMN unit, DROP COLUMN unit_price_cents,
    DROP COLUMN line_total_cents, DROP COLUMN discount_cents, DROP COLUMN taxable,
    DROP COLUMN tax_flag, DROP COLUMN is_return, DROP COLUMN confidence, DROP COLUMN metadata;

-- receipts: restore legacy subtotal from subtotal_cents (tax/total are dropped)
ALTER TABLE receipts ADD COLUMN subtotal DECIMAL(10,2) NOT NULL DEFAULT 0.00;
UPDATE receipts SET subtotal = (subtotal_cents / 100.0)::numeric(10,2);
DROP INDEX IF EXISTS idx_receipts_merchant_date;
DROP INDEX IF EXISTS idx_receipts_store_id_date;
DROP INDEX IF EXISTS idx_receipts_member;
DROP INDEX IF EXISTS idx_receipts_review_state;
ALTER TABLE receipts
    DROP COLUMN merchant_id, DROP COLUMN store_id, DROP COLUMN source_document_id,
    DROP COLUMN household_member_id, DROP COLUMN purchased_at, DROP COLUMN subtotal_cents,
    DROP COLUMN tax_cents, DROP COLUMN total_cents, DROP COLUMN savings_cents,
    DROP COLUMN currency, DROP COLUMN payment_method, DROP COLUMN payment_last4,
    DROP COLUMN confidence, DROP COLUMN review_state,
    DROP COLUMN metadata, DROP COLUMN source, DROP COLUMN external_id;

DROP TABLE IF EXISTS product_aliases;
DROP TABLE IF EXISTS products;
DROP TABLE IF EXISTS source_documents;
DROP TABLE IF EXISTS stores;
DROP TABLE IF EXISTS merchants;
DROP TABLE IF EXISTS household_members;
-- Create analytical views for MCP queries

-- Complete receipt details view - JOIN receipts + items for "what did I buy" queries
CREATE VIEW receipt_details_view AS
SELECT 
    r.id as receipt_id,
    r.store_name,
    r.store_category,
    r.store_address,
    r.store_phone,
    r.date as receipt_date,
    r.subtotal as receipt_subtotal,
    ri.id as item_id,
    ri.sku,
    ri.name as item_name,
    ri.description as item_description,
    ri.quantity,
    ri.unit_price,
    (ri.quantity * ri.unit_price) as item_total,
    r.created_at as receipt_created_at
FROM receipts r
LEFT JOIN receipt_items ri ON r.id = ri.receipt_id
ORDER BY r.date DESC, r.id, ri.id;

-- Store spending view - Aggregated spending by store for "how much did I spend at X" queries
CREATE VIEW store_spending_view AS
SELECT 
    store_name,
    store_category,
    COUNT(DISTINCT id) as total_visits,
    SUM(subtotal) as total_spent,
    AVG(subtotal) as avg_per_visit,
    MIN(date) as first_visit,
    MAX(date) as last_visit,
    COUNT(CASE WHEN date >= CURRENT_DATE - INTERVAL '30 days' THEN 1 END) as visits_last_30_days,
    COALESCE(SUM(CASE WHEN date >= CURRENT_DATE - INTERVAL '30 days' THEN subtotal END), 0) as spent_last_30_days,
    COUNT(CASE WHEN date >= CURRENT_DATE - INTERVAL '90 days' THEN 1 END) as visits_last_90_days,
    COALESCE(SUM(CASE WHEN date >= CURRENT_DATE - INTERVAL '90 days' THEN subtotal END), 0) as spent_last_90_days
FROM receipts
GROUP BY store_name, store_category
ORDER BY total_spent DESC;

-- Item purchase history view - Track when items were purchased for "when did I last buy X" queries
CREATE VIEW item_purchase_history_view AS
SELECT 
    ri.sku,
    ri.name as item_name,
    ri.description as item_description,
    r.store_name,
    r.store_category,
    r.date as purchase_date,
    ri.quantity,
    ri.unit_price,
    (ri.quantity * ri.unit_price) as total_paid,
    r.id as receipt_id,
    ROW_NUMBER() OVER (PARTITION BY COALESCE(ri.sku, ri.name) ORDER BY r.date DESC) as purchase_rank
FROM receipt_items ri
JOIN receipts r ON ri.receipt_id = r.id
ORDER BY r.date DESC;

-- Recent purchases view - Latest purchases with store info for "my last trip to X" queries
CREATE VIEW recent_purchases_view AS
SELECT DISTINCT
    r.id as receipt_id,
    r.store_name,
    r.store_category,
    r.date as receipt_date,
    r.subtotal,
    COUNT(ri.id) as item_count,
    STRING_AGG(ri.name, ', ' ORDER BY ri.name) as items_purchased,
    ROW_NUMBER() OVER (PARTITION BY r.store_name ORDER BY r.date DESC) as visit_rank
FROM receipts r
LEFT JOIN receipt_items ri ON r.id = ri.receipt_id
GROUP BY r.id, r.store_name, r.store_category, r.date, r.subtotal
ORDER BY r.date DESC;

-- Monthly spending trends view - Spending patterns over time
CREATE VIEW spending_trends_view AS
SELECT 
    DATE_TRUNC('month', date) as month,
    COUNT(DISTINCT id) as receipt_count,
    SUM(subtotal) as total_spent,
    AVG(subtotal) as avg_per_receipt,
    COUNT(DISTINCT store_name) as unique_stores,
    MIN(subtotal) as min_receipt,
    MAX(subtotal) as max_receipt
FROM receipts
GROUP BY DATE_TRUNC('month', date)
ORDER BY month DESC;