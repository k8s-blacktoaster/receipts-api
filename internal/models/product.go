package models

import (
	"time"
)

type Product struct {
	ID          int     `json:"id" db:"id"`
	SKU         string  `json:"sku" db:"sku"`
	Name        string  `json:"name" db:"name"`
	Description string  `json:"description" db:"description"`
	Quantity    int     `json:"quantity" db:"quantity"`
	UnitPrice   float64 `json:"unit_price" db:"unit_price"`

	// Metadata
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}
