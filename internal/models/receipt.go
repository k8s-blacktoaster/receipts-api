package models

import (
	"time"
)

type Receipt struct {
	ID            int           `json:"id" db:"id"`
	StoreName     string        `json:"store_name" db:"store_name"`
	StoreCategory string        `json:"store_category" db:"store_category"`
	StoreAddress  string        `json:"store_address" db:"store_address"`
	StorePhone    string        `json:"store_phone" db:"store_phone"`
	Date          string        `json:"date" db:"date"`
	Subtotal      float64       `json:"subtotal" db:"subtotal"`
	Items         []ReceiptItem `json:"items,omitempty"`

	// Metadata
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

type ReceiptItem struct {
	ID          int     `json:"id,omitempty" db:"id"`
	ReceiptID   int     `json:"-" db:"receipt_id"`
	SKU         string  `json:"sku" db:"sku"`
	Name        string  `json:"name" db:"name"`
	Description string  `json:"description" db:"description"`
	Quantity    int     `json:"quantity" db:"quantity"`
	UnitPrice   float64 `json:"unit_price" db:"unit_price"`

	// Metadata
	CreatedAt time.Time `json:"created_at,omitempty" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at,omitempty" db:"updated_at"`
}
