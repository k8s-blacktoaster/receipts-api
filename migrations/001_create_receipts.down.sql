-- Drop the receipt_items table first due to foreign key constraint
DROP TABLE IF EXISTS receipt_items;

-- Drop the receipts table
DROP TABLE IF EXISTS receipts;

-- Drop the trigger function
DROP FUNCTION IF EXISTS update_updated_at_column();