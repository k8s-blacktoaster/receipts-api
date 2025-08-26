-- Drop the products table and its associated indexes
DROP INDEX IF EXISTS idx_products_created_at;
DROP INDEX IF EXISTS idx_products_name;
DROP INDEX IF EXISTS idx_products_sku;
DROP TABLE IF EXISTS products;