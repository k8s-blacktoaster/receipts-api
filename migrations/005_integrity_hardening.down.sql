-- Reverse of 005. Merged duplicate merchants/stores and trimmed names are NOT un-merged (lossy, accepted).
DROP VIEW recent_purchases_view;
DROP VIEW receipt_details_view;
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

-- store_name back to NOT NULL: fill from merchant first
UPDATE receipts r SET store_name = m.name FROM merchants m WHERE m.id = r.merchant_id AND r.store_name IS NULL;
ALTER TABLE receipts ALTER COLUMN store_name SET NOT NULL;

DROP INDEX IF EXISTS idx_receipt_adjustments_applies_to_item_id;
DROP INDEX IF EXISTS idx_source_documents_uploaded_by_id;
DROP INDEX IF EXISTS idx_receipts_reviewed_by_id;
DROP INDEX IF EXISTS idx_receipts_uploaded_by_id;
DROP INDEX IF EXISTS idx_receipts_source_document_id;

ALTER TABLE receipt_adjustments DROP CONSTRAINT receipt_adjustments_sign;
ALTER TABLE receipt_items
    DROP CONSTRAINT receipt_items_unit_price_nonneg, DROP CONSTRAINT receipt_items_line_total_nonneg,
    DROP CONSTRAINT receipt_items_discount_nonneg, DROP CONSTRAINT receipt_items_quantity_pos;
ALTER TABLE receipts
    DROP CONSTRAINT receipts_subtotal_nonneg, DROP CONSTRAINT receipts_tax_nonneg,
    DROP CONSTRAINT receipts_total_nonneg, DROP CONSTRAINT receipts_savings_nonneg;

ALTER TABLE receipts DROP CONSTRAINT receipts_store_merchant_fkey;
ALTER TABLE stores DROP CONSTRAINT stores_id_merchant_uniq;

DROP INDEX IF EXISTS stores_merchant_address_uniq;

ALTER TABLE merchants DROP CONSTRAINT merchants_name_trimmed;
DROP INDEX merchants_name_uniq;
CREATE UNIQUE INDEX merchants_name_uniq ON merchants (lower(name));
