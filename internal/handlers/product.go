package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"receipts-db/internal/models"
	"receipts-db/internal/repository"
)

type ProductHandler struct {
	repo   repository.ProductRepository
	logger *slog.Logger
}

func NewProductHandler(repo repository.ProductRepository, logger *slog.Logger) *ProductHandler {
	return &ProductHandler{
		repo:   repo,
		logger: logger,
	}
}

// ListProducts handles GET /api/v1/products
// It returns a paginated list of products
//
//	@Summary		List products
//	@Description	Get a paginated list of products in inventory
//	@Tags			products
//	@Accept			json
//	@Produce		json
//	@Param			limit	query		int	false	"Number of items to return (max 100)"	default(50)
//	@Param			offset	query		int	false	"Number of items to skip"				default(0)
//	@Success		200		{object}	models.PaginatedResponse	"List of products with pagination metadata"
//	@Failure		500		{object}	models.ErrorResponse	"Internal server error"
//	@Router			/products [get]
func (h *ProductHandler) ListProducts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	limit := 50
	offset := 0

	if l := r.URL.Query().Get("limit"); l != "" {
		if parsedLimit, err := strconv.Atoi(l); err == nil && parsedLimit > 0 {
			limit = parsedLimit
			if limit > 100 {
				limit = 100
			}
		}
	}

	if o := r.URL.Query().Get("offset"); o != "" {
		if parsedOffset, err := strconv.Atoi(o); err == nil && parsedOffset >= 0 {
			offset = parsedOffset
		}
	}

	products, err := h.repo.List(ctx, limit, offset)
	if err != nil {
		h.logger.Error("failed to list products", "error", err)
		h.respondWithError(w, http.StatusInternalServerError, "Failed to retrieve products")
		return
	}

	total, err := h.repo.Count(ctx)
	if err != nil {
		h.logger.Error("failed to count products", "error", err)
		h.respondWithError(w, http.StatusInternalServerError, "Failed to count products")
		return
	}

	pagination := &models.PaginationMeta{
		Limit:  limit,
		Offset: offset,
		Total:  total,
	}
	response := models.NewPaginatedResponse(http.StatusOK, "Products retrieved successfully", products, pagination)

	h.respondWithJSON(w, http.StatusOK, response)
}

// GetProduct handles GET /api/v1/products/{id}
// It returns a single product by ID
//
//	@Summary		Get product by ID
//	@Description	Get a single product with all details
//	@Tags			products
//	@Accept			json
//	@Produce		json
//	@Param			id	path		int	true	"Product ID"
//	@Success		200	{object}	models.SuccessResponse	"Product details"
//	@Failure		400	{object}	models.ErrorResponse	"Bad request"
//	@Failure		404	{object}	models.ErrorResponse	"Product not found"
//	@Failure		500	{object}	models.ErrorResponse	"Internal server error"
//	@Router			/products/{id} [get]
func (h *ProductHandler) GetProduct(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	idStr := chi.URLParam(r, "id")

	if idStr == "" {
		h.respondWithError(w, http.StatusBadRequest, "Product ID is required")
		return
	}

	id, err := strconv.Atoi(idStr)
	if err != nil {
		h.respondWithError(w, http.StatusBadRequest, "Invalid product ID")
		return
	}

	product, err := h.repo.GetByID(ctx, id)
	if err != nil {
		if err.Error() == "product not found" {
			h.respondWithError(w, http.StatusNotFound, "Product not found")
			return
		}
		h.logger.Error("failed to get product", "error", err, "product_id", id)
		h.respondWithError(w, http.StatusInternalServerError, "Failed to retrieve product")
		return
	}

	response := models.NewSuccessResponse(http.StatusOK, "Product retrieved successfully", product)

	h.respondWithJSON(w, http.StatusOK, response)
}

// CreateProduct handles POST /api/v1/products
// It creates a new product
//
//	@Summary		Create a new product
//	@Description	Create a new product in the inventory
//	@Tags			products
//	@Accept			json
//	@Produce		json
//	@Param			product	body		models.Product			true	"Product data"
//	@Success		201		{object}	models.SuccessResponse	"Created product"
//	@Failure		400		{object}	models.ErrorResponse	"Bad request"
//	@Failure		409		{object}	models.ErrorResponse	"Product with SKU already exists"
//	@Failure		500		{object}	models.ErrorResponse	"Internal server error"
//	@Router			/products [post]
func (h *ProductHandler) CreateProduct(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var product models.Product
	if err := json.NewDecoder(r.Body).Decode(&product); err != nil {
		h.respondWithError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if product.SKU == "" {
		h.respondWithError(w, http.StatusBadRequest, "SKU is required")
		return
	}

	if product.Name == "" {
		h.respondWithError(w, http.StatusBadRequest, "Product name is required")
		return
	}

	// Check if SKU already exists
	existing, err := h.repo.GetBySKU(ctx, product.SKU)
	if err == nil && existing != nil {
		h.respondWithError(w, http.StatusConflict, "Product with this SKU already exists")
		return
	}

	if err := h.repo.Create(ctx, &product); err != nil {
		h.logger.Error("failed to create product", "error", err, "sku", product.SKU)
		h.respondWithError(w, http.StatusInternalServerError, "Failed to create product")
		return
	}

	h.logger.Info("product created", "product_id", product.ID, "sku", product.SKU)
	response := models.NewSuccessResponse(http.StatusCreated, "Product created successfully", product)
	h.respondWithJSON(w, http.StatusCreated, response)
}

// UpdateProduct handles PUT /api/v1/products/{id}
// It updates an existing product
//
//	@Summary		Update product
//	@Description	Update an existing product's information
//	@Tags			products
//	@Accept			json
//	@Produce		json
//	@Param			id		path		int				true	"Product ID"
//	@Param			product	body		models.Product	true	"Updated product data"
//	@Success		200		{object}	models.SuccessResponse	"Updated product"
//	@Failure		400		{object}	models.ErrorResponse	"Bad request"
//	@Failure		404		{object}	models.ErrorResponse	"Product not found"
//	@Failure		500		{object}	models.ErrorResponse	"Internal server error"
//	@Router			/products/{id} [put]
func (h *ProductHandler) UpdateProduct(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	idStr := chi.URLParam(r, "id")

	if idStr == "" {
		h.respondWithError(w, http.StatusBadRequest, "Product ID is required")
		return
	}

	id, err := strconv.Atoi(idStr)
	if err != nil {
		h.respondWithError(w, http.StatusBadRequest, "Invalid product ID")
		return
	}

	var product models.Product
	if err := json.NewDecoder(r.Body).Decode(&product); err != nil {
		h.respondWithError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	product.ID = id

	if product.SKU == "" {
		h.respondWithError(w, http.StatusBadRequest, "SKU is required")
		return
	}

	if product.Name == "" {
		h.respondWithError(w, http.StatusBadRequest, "Product name is required")
		return
	}

	if err := h.repo.Update(ctx, &product); err != nil {
		if err.Error() == "product not found" {
			h.respondWithError(w, http.StatusNotFound, "Product not found")
			return
		}
		h.logger.Error("failed to update product", "error", err, "product_id", id)
		h.respondWithError(w, http.StatusInternalServerError, "Failed to update product")
		return
	}

	h.logger.Info("product updated", "product_id", id, "sku", product.SKU)
	response := models.NewSuccessResponse(http.StatusOK, "Product updated successfully", product)
	h.respondWithJSON(w, http.StatusOK, response)
}

// DeleteProduct handles DELETE /api/v1/products/{id}
// It deletes a product
//
//	@Summary		Delete product
//	@Description	Delete a product by ID
//	@Tags			products
//	@Accept			json
//	@Produce		json
//	@Param			id	path	int	true	"Product ID"
//	@Success		204	{object}	models.SuccessResponse	"Product deleted successfully"
//	@Failure		400	{object}	models.ErrorResponse	"Bad request"
//	@Failure		404	{object}	models.ErrorResponse	"Product not found"
//	@Failure		500	{object}	models.ErrorResponse	"Internal server error"
//	@Router			/products/{id} [delete]
func (h *ProductHandler) DeleteProduct(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	idStr := chi.URLParam(r, "id")

	if idStr == "" {
		h.respondWithError(w, http.StatusBadRequest, "Product ID is required")
		return
	}

	id, err := strconv.Atoi(idStr)
	if err != nil {
		h.respondWithError(w, http.StatusBadRequest, "Invalid product ID")
		return
	}

	if err := h.repo.Delete(ctx, id); err != nil {
		if err.Error() == "product not found" {
			h.respondWithError(w, http.StatusNotFound, "Product not found")
			return
		}
		h.logger.Error("failed to delete product", "error", err, "product_id", id)
		h.respondWithError(w, http.StatusInternalServerError, "Failed to delete product")
		return
	}

	h.logger.Info("product deleted", "product_id", id)
	response := models.NewSuccessResponse(http.StatusNoContent, "Product deleted successfully", nil)
	h.respondWithJSON(w, http.StatusNoContent, response)
}

// HealthCheck handles GET /api/v1/health
// It returns the health status of the API
//
//	@Summary		Health check
//	@Description	Check if the API is healthy and running
//	@Tags			health
//	@Accept			json
//	@Produce		json
//	@Success		200	{object}	models.SuccessResponse	"Health status"
//	@Router			/health [get]
func (h *ProductHandler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	data := map[string]interface{}{
		"service": "receipts-db",
		"version": "1.0.0",
	}
	response := models.NewSuccessResponse(http.StatusOK, "Service is healthy", data)
	h.respondWithJSON(w, http.StatusOK, response)
}

// Helper methods for consistent JSON responses

func (h *ProductHandler) respondWithJSON(w http.ResponseWriter, code int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		h.logger.Error("failed to encode response", "error", err)
	}
}

func (h *ProductHandler) respondWithError(w http.ResponseWriter, code int, message string) {
	response := models.NewErrorResponse(code, message)
	h.respondWithJSON(w, code, response)
}
