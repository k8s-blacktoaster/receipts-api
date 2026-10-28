-- 005: QA phase-1 integrity hardening (F1-F4, F6, F7).
-- Sign convention (F2): receipts.savings_cents and receipt_items.discount_cents are stored as
-- POSITIVE magnitudes (>= 0). receipt_adjustments.amount_cents is SIGNED: negative reduces the
-- total (instant_savings/coupon/discount <= 0), positive adds (deposit/fee/tip >= 0), 'other' either.
-- No subtotal+tax-savings=total constraint: arithmetic mismatches go to needs_review (phase 2).

-- ---- F1: merchant dedupe on lower(btrim(name)) ------------------------------
DROP INDEX merchants_name_uniq;
UPDATE merchants SET name = btrim(name) WHERE name <> btrim(name);
CREATE TEMP TABLE _merchant_map ON COMMIT DROP AS
SELECT id, min(id) OVER (PARTITION BY lower(btrim(name))) AS keep_id FROM merchants;
DELETE FROM _merchant_map WHERE id = keep_id;
UPDATE receipts r SET merchant_id = m.keep_id FROM _merchant_map m WHERE r.merchant_id = m.id;
UPDATE stores s SET merchant_id = m.keep_id FROM _merchant_map m WHERE s.merchant_id = m.id;
DELETE FROM product_aliases a USING _merchant_map m, product_aliases k
 WHERE a.merchant_id = m.id AND k.merchant_id = m.keep_id AND lower(k.alias_text) = lower(a.alias_text);
UPDATE product_aliases a SET merchant_id = m.keep_id FROM _merchant_map m WHERE a.merchant_id = m.id;
DELETE FROM merchants WHERE id IN (SELECT id FROM _merchant_map);
CREATE UNIQUE INDEX merchants_name_uniq ON merchants (lower(btrim(name)));
ALTER TABLE merchants ADD CONSTRAINT merchants_name_trimmed CHECK (name = btrim(name) AND name <> '');

-- ---- F6: store dedupe by (merchant, normalized address) when no store number -
UPDATE stores SET address = NULLIF(btrim(address), '') WHERE address IS DISTINCT FROM NULLIF(btrim(address), '');
UPDATE stores SET store_number = NULLIF(btrim(store_number), '') WHERE store_number IS DISTINCT FROM NULLIF(btrim(store_number), '');
CREATE TEMP TABLE _store_map ON COMMIT DROP AS
SELECT id, min(id) OVER (PARTITION BY merchant_id, lower(address)) AS keep_id
FROM stores WHERE store_number IS NULL AND address IS NOT NULL;
DELETE FROM _store_map WHERE id = keep_id;
UPDATE receipts r SET store_id = m.keep_id FROM _store_map m WHERE r.store_id = m.id;
DELETE FROM stores WHERE id IN (SELECT id FROM _store_map);
CREATE UNIQUE INDEX stores_merchant_address_uniq ON stores (merchant_id, lower(address))
    WHERE store_number IS NULL AND address IS NOT NULL;

-- ---- F3: a receipt's store must belong to its merchant ------------------------
-- (PG14 has no ON DELETE SET NULL (col); the existing single-column store_id FK keeps
--  ON DELETE SET NULL, and the composite FK is skipped once store_id is NULL.)
UPDATE receipts r SET store_id = NULL FROM stores s WHERE s.id = r.store_id AND s.merchant_id <> r.merchant_id;
ALTER TABLE stores ADD CONSTRAINT stores_id_merchant_uniq UNIQUE (id, merchant_id);
ALTER TABLE receipts ADD CONSTRAINT receipts_store_merchant_fkey
    FOREIGN KEY (store_id, merchant_id) REFERENCES stores (id, merchant_id) ON UPDATE CASCADE;

-- ---- F2: non-negative money + consistent signs --------------------------------
ALTER TABLE receipts
    ADD CONSTRAINT receipts_subtotal_nonneg CHECK (subtotal_cents >= 0),
    ADD CONSTRAINT receipts_tax_nonneg      CHECK (tax_cents >= 0),
    ADD CONSTRAINT receipts_total_nonneg    CHECK (total_cents >= 0),
    ADD CONSTRAINT receipts_savings_nonneg  CHECK (savings_cents >= 0);
ALTER TABLE receipt_items
    ADD CONSTRAINT receipt_items_unit_price_nonneg CHECK (unit_price_cents >= 0 OR is_return),
    ADD CONSTRAINT receipt_items_line_total_nonneg CHECK (line_total_cents >= 0 OR is_return),
    ADD CONSTRAINT receipt_items_discount_nonneg   CHECK (discount_cents >= 0),
    ADD CONSTRAINT receipt_items_quantity_pos      CHECK (quantity > 0);
ALTER TABLE receipt_adjustments ADD CONSTRAINT receipt_adjustments_sign CHECK (
    (kind IN ('instant_savings','coupon','discount') AND amount_cents <= 0) OR
    (kind IN ('deposit','fee','tip') AND amount_cents >= 0) OR kind = 'other');

-- ---- F4: FK indexes -------------------------------------------------------------
CREATE INDEX idx_receipts_source_document_id ON receipts (source_document_id) WHERE source_document_id IS NOT NULL;
CREATE INDEX idx_receipts_uploaded_by_id ON receipts (uploaded_by_id) WHERE uploaded_by_id IS NOT NULL;
CREATE INDEX idx_receipts_reviewed_by_id ON receipts (reviewed_by_id) WHERE reviewed_by_id IS NOT NULL;
CREATE INDEX idx_source_documents_uploaded_by_id ON source_documents (uploaded_by_id) WHERE uploaded_by_id IS NOT NULL;
CREATE INDEX idx_receipt_adjustments_applies_to_item_id ON receipt_adjustments (applies_to_item_id) WHERE applies_to_item_id IS NOT NULL;

-- ---- F7: legacy store_* nullable; views read merchant/store link ---------------
-- (legacy columns are dropped in Phase 7)
ALTER TABLE receipts ALTER COLUMN store_name DROP NOT NULL;

DROP VIEW receipt_details_view;
CREATE VIEW receipt_details_view AS
SELECT r.id AS receipt_id, m.name AS store_name, m.category AS store_category,
       s.address AS store_address, s.phone AS store_phone,
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
FROM receipts r JOIN merchants m ON m.id = r.merchant_id
LEFT JOIN stores s ON s.id = r.store_id
LEFT JOIN receipt_items ri ON r.id = ri.receipt_id
ORDER BY r.date DESC, r.id, ri.id;

DROP VIEW recent_purchases_view;
CREATE VIEW recent_purchases_view AS
SELECT r.id AS receipt_id, m.name AS store_name, m.category AS store_category,
       r.date AS receipt_date,
       (r.subtotal_cents / 100.0)::numeric(12,2) AS subtotal,
       (r.total_cents / 100.0)::numeric(12,2)    AS total,
       COUNT(ri.id) AS item_count,
       STRING_AGG(ri.name, ', ' ORDER BY ri.name) AS items_purchased,
       ROW_NUMBER() OVER (PARTITION BY r.merchant_id ORDER BY r.date DESC) AS visit_rank
FROM receipts r JOIN merchants m ON m.id = r.merchant_id
LEFT JOIN receipt_items ri ON r.id = ri.receipt_id
GROUP BY r.id, m.name, m.category
ORDER BY r.date DESC;
