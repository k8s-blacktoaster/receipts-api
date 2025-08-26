package repository

import (
	"context"
	"testing"
	"time"

	"receipts-db/internal/database"
	"receipts-db/internal/models"
)

// Note: These tests require a running PostgreSQL instance
// Run: docker-compose up -d postgres
// Or use the test database from devcontainer setup
func setupTestDB(t *testing.T) *database.DB {
	// Skip tests if no test database is available
	testURL := "postgres://postgres:postgres@localhost:5432/receipts_db_test?sslmode=disable"
	cfg := database.Config{
		URL: testURL,
	}

	db, err := database.NewConnection(cfg)
	if err != nil {
		t.Skipf("Skipping test - PostgreSQL not available: %v", err)
	}

	_, _ = db.Exec("DROP TABLE IF EXISTS products")

	schema := `
		CREATE TABLE products (
			id SERIAL PRIMARY KEY,
			sku VARCHAR(255) NOT NULL UNIQUE,
			name VARCHAR(255) NOT NULL,
			description TEXT,
			quantity INTEGER NOT NULL DEFAULT 0,
			unit_price DECIMAL(10,2) NOT NULL DEFAULT 0.00,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`

	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("failed to create schema: %v", err)
	}

	return db
}

func TestProductRepository_Create(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	repo := NewProductRepository(db)
	ctx := context.Background()

	product := &models.Product{
		SKU:         "TEST-123",
		Name:        "Test Product",
		Description: "A test product for unit testing",
		Quantity:    10,
		UnitPrice:   19.99,
	}

	err := repo.Create(ctx, product)
	if err != nil {
		t.Fatalf("failed to create product: %v", err)
	}

	// Check that ID was set
	if product.ID == 0 {
		t.Error("expected product ID to be set after creation")
	}

	retrieved, err := repo.GetByID(ctx, product.ID)
	if err != nil {
		t.Fatalf("failed to retrieve product: %v", err)
	}

	if retrieved.SKU != product.SKU {
		t.Errorf("SKU = %v, want %v", retrieved.SKU, product.SKU)
	}
	if retrieved.Name != product.Name {
		t.Errorf("Name = %v, want %v", retrieved.Name, product.Name)
	}
	if retrieved.Description != product.Description {
		t.Errorf("Description = %v, want %v", retrieved.Description, product.Description)
	}
	if retrieved.Quantity != product.Quantity {
		t.Errorf("Quantity = %v, want %v", retrieved.Quantity, product.Quantity)
	}
	if retrieved.UnitPrice != product.UnitPrice {
		t.Errorf("UnitPrice = %v, want %v", retrieved.UnitPrice, product.UnitPrice)
	}

	// Test duplicate SKU
	duplicate := &models.Product{
		SKU:       "TEST-123",
		Name:      "Duplicate",
		Quantity:  1,
		UnitPrice: 1.00,
	}
	err = repo.Create(ctx, duplicate)
	if err == nil {
		t.Error("expected error when creating product with duplicate SKU")
	}
}

func TestProductRepository_GetBySKU(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	repo := NewProductRepository(db)
	ctx := context.Background()

	product := &models.Product{
		SKU:         "SKU-TEST",
		Name:        "SKU Test Product",
		Description: "Testing GetBySKU",
		Quantity:    5,
		UnitPrice:   25.00,
	}

	if err := repo.Create(ctx, product); err != nil {
		t.Fatalf("failed to create product: %v", err)
	}

	retrieved, err := repo.GetBySKU(ctx, "SKU-TEST")
	if err != nil {
		t.Fatalf("failed to retrieve product by SKU: %v", err)
	}

	if retrieved.ID != product.ID {
		t.Errorf("ID = %v, want %v", retrieved.ID, product.ID)
	}
	if retrieved.Name != product.Name {
		t.Errorf("Name = %v, want %v", retrieved.Name, product.Name)
	}

	_, err = repo.GetBySKU(ctx, "NON-EXISTENT")
	if err == nil {
		t.Error("expected error when getting non-existent SKU")
	}
}

func TestProductRepository_Update(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	repo := NewProductRepository(db)
	ctx := context.Background()

	product := &models.Product{
		SKU:         "UPDATE-TEST",
		Name:        "Original Name",
		Description: "Original Description",
		Quantity:    10,
		UnitPrice:   15.00,
	}

	if err := repo.Create(ctx, product); err != nil {
		t.Fatalf("failed to create product: %v", err)
	}

	// Update product
	product.Name = "Updated Name"
	product.Description = "Updated Description"
	product.Quantity = 20
	product.UnitPrice = 25.00

	if err := repo.Update(ctx, product); err != nil {
		t.Fatalf("failed to update product: %v", err)
	}

	retrieved, err := repo.GetByID(ctx, product.ID)
	if err != nil {
		t.Fatalf("failed to retrieve product: %v", err)
	}

	if retrieved.Name != "Updated Name" {
		t.Errorf("Name = %v, want %v", retrieved.Name, "Updated Name")
	}
	if retrieved.Description != "Updated Description" {
		t.Errorf("Description = %v, want %v", retrieved.Description, "Updated Description")
	}
	if retrieved.Quantity != 20 {
		t.Errorf("Quantity = %v, want %v", retrieved.Quantity, 20)
	}
	if retrieved.UnitPrice != 25.00 {
		t.Errorf("UnitPrice = %v, want %v", retrieved.UnitPrice, 25.00)
	}

	// Test updating non-existent product
	nonExistent := &models.Product{
		ID:   99999,
		SKU:  "NON-EXISTENT",
		Name: "Does not exist",
	}
	err = repo.Update(ctx, nonExistent)
	if err == nil {
		t.Error("expected error when updating non-existent product")
	}
}

func TestProductRepository_Delete(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	repo := NewProductRepository(db)
	ctx := context.Background()

	product := &models.Product{
		SKU:       "DELETE-TEST",
		Name:      "Delete Test Product",
		Quantity:  1,
		UnitPrice: 10.00,
	}

	if err := repo.Create(ctx, product); err != nil {
		t.Fatalf("failed to create product: %v", err)
	}

	if err := repo.Delete(ctx, product.ID); err != nil {
		t.Fatalf("failed to delete product: %v", err)
	}

	_, err := repo.GetByID(ctx, product.ID)
	if err == nil {
		t.Error("expected error when getting deleted product")
	}

	// Test deleting non-existent product
	err = repo.Delete(ctx, 99999)
	if err == nil {
		t.Error("expected error when deleting non-existent product")
	}
}

func TestProductRepository_List(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	repo := NewProductRepository(db)
	ctx := context.Background()

	products := []models.Product{
		{SKU: "LIST-1", Name: "Product 1", Quantity: 1, UnitPrice: 10.00},
		{SKU: "LIST-2", Name: "Product 2", Quantity: 2, UnitPrice: 20.00},
		{SKU: "LIST-3", Name: "Product 3", Quantity: 3, UnitPrice: 30.00},
		{SKU: "LIST-4", Name: "Product 4", Quantity: 4, UnitPrice: 40.00},
		{SKU: "LIST-5", Name: "Product 5", Quantity: 5, UnitPrice: 50.00},
	}

	for i := range products {
		time.Sleep(1 * time.Millisecond) // Ensure different timestamps
		if err := repo.Create(ctx, &products[i]); err != nil {
			t.Fatalf("failed to create product %s: %v", products[i].SKU, err)
		}
	}

	tests := []struct {
		name   string
		limit  int
		offset int
		want   int
	}{
		{"first page", 2, 0, 2},
		{"second page", 2, 2, 2},
		{"third page", 2, 4, 1},
		{"all items", 10, 0, 5},
		{"offset beyond total", 10, 10, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, err := repo.List(ctx, tt.limit, tt.offset)
			if err != nil {
				t.Fatalf("failed to list products: %v", err)
			}

			if len(results) != tt.want {
				t.Errorf("List() returned %d items, want %d", len(results), tt.want)
			}
		})
	}

	count, err := repo.Count(ctx)
	if err != nil {
		t.Fatalf("failed to count products: %v", err)
	}

	if count != 5 {
		t.Errorf("Count() = %d, want 5", count)
	}
}

func TestProductRepository_GetByID_NotFound(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	repo := NewProductRepository(db)
	ctx := context.Background()

	_, err := repo.GetByID(ctx, 99999)
	if err == nil {
		t.Error("expected error when getting non-existent product")
	}

	if err.Error() != "product not found" {
		t.Errorf("unexpected error message: %v", err)
	}
}