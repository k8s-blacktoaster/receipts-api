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