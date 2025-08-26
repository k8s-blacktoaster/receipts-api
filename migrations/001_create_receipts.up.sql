-- Create the receipts table
-- This table stores receipt header information including store details and totals
CREATE TABLE IF NOT EXISTS receipts (
    id SERIAL PRIMARY KEY,
    store_name VARCHAR(255) NOT NULL,
    store_category VARCHAR(255),
    store_address TEXT,
    store_phone VARCHAR(50),
    date DATE NOT NULL,
    subtotal DECIMAL(10,2) NOT NULL DEFAULT 0.00,
    
    -- Metadata
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Create the receipt_items table
-- This table stores individual items purchased on each receipt
CREATE TABLE IF NOT EXISTS receipt_items (
    id SERIAL PRIMARY KEY,
    receipt_id INTEGER NOT NULL REFERENCES receipts(id) ON DELETE CASCADE,
    sku VARCHAR(255),
    name VARCHAR(500) NOT NULL,
    description TEXT,
    quantity INTEGER NOT NULL DEFAULT 1,
    unit_price DECIMAL(10,2) NOT NULL DEFAULT 0.00,
    
    -- Metadata
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Create indexes for better query performance
CREATE INDEX idx_receipts_store_name ON receipts(store_name);
CREATE INDEX idx_receipts_date ON receipts(date DESC);
CREATE INDEX idx_receipts_store_date ON receipts(store_name, date DESC);
CREATE INDEX idx_receipts_created_at ON receipts(created_at DESC);

CREATE INDEX idx_receipt_items_receipt_id ON receipt_items(receipt_id);
CREATE INDEX idx_receipt_items_sku ON receipt_items(sku);
CREATE INDEX idx_receipt_items_name ON receipt_items(name);
CREATE INDEX idx_receipt_items_name_text ON receipt_items USING gin(to_tsvector('english', name));

-- Create triggers to automatically update the updated_at timestamp
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ language 'plpgsql';

CREATE TRIGGER update_receipts_updated_at BEFORE UPDATE ON receipts
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_receipt_items_updated_at BEFORE UPDATE ON receipt_items
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();