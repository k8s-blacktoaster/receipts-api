package handlers

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"receipts-db/internal/models"
	"receipts-db/internal/repository"
)

type ReceiptHandler struct {
	repo   repository.ReceiptRepository
	logger *slog.Logger
}

func NewReceiptHandler(repo repository.ReceiptRepository, logger *slog.Logger) *ReceiptHandler {
	return &ReceiptHandler{
		repo:   repo,
		logger: logger,
	}
}

// ListReceipts handles GET /api/v1/receipts
// It returns a paginated list of receipts
//
//	@Summary		List receipts
//	@Description	Get a paginated list of receipts
//	@Tags			receipts
//	@Accept			json
//	@Produce		json
//	@Param			limit	query		int	false	"Number of items to return (max 100)"	default(50)
//	@Param			offset	query		int	false	"Number of items to skip"				default(0)
//	@Success		200		{object}	models.PaginatedResponse	"List of receipts with pagination metadata"
//	@Failure		500		{object}	models.ErrorResponse	"Internal server error"
//	@Router			/receipts [get]
func (h *ReceiptHandler) ListReceipts(w http.ResponseWriter, r *http.Request) {
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

	receipts, err := h.repo.List(ctx, limit, offset)
	if err != nil {
		h.logger.Error("failed to list receipts", "error", err)
		h.respondWithError(w, http.StatusInternalServerError, "Failed to retrieve receipts")
		return
	}

	total, err := h.repo.Count(ctx)
	if err != nil {
		h.logger.Error("failed to count receipts", "error", err)
		h.respondWithError(w, http.StatusInternalServerError, "Failed to count receipts")
		return
	}

	pagination := &models.PaginationMeta{
		Limit:  limit,
		Offset: offset,
		Total:  total,
	}
	response := models.NewPaginatedResponse(http.StatusOK, "Receipts retrieved successfully", receipts, pagination)

	h.respondWithJSON(w, http.StatusOK, response)
}

// GetReceipt handles GET /api/v1/receipts/{id}
// It returns a single receipt by ID with its items
//
//	@Summary		Get receipt by ID
//	@Description	Get a single receipt with all details and items
//	@Tags			receipts
//	@Accept			json
//	@Produce		json
//	@Param			id	path		int	true	"Receipt ID"
//	@Success		200	{object}	models.SuccessResponse	"Receipt details"
//	@Failure		400	{object}	models.ErrorResponse	"Bad request"
//	@Failure		404	{object}	models.ErrorResponse	"Receipt not found"
//	@Failure		500	{object}	models.ErrorResponse	"Internal server error"
//	@Router			/receipts/{id} [get]
func (h *ReceiptHandler) GetReceipt(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	idStr := chi.URLParam(r, "id")

	if idStr == "" {
		h.respondWithError(w, http.StatusBadRequest, "Receipt ID is required")
		return
	}

	id, err := strconv.Atoi(idStr)
	if err != nil {
		h.respondWithError(w, http.StatusBadRequest, "Invalid receipt ID")
		return
	}

	receipt, err := h.repo.GetByID(ctx, id)
	if err != nil {
		if err.Error() == "receipt not found" {
			h.respondWithError(w, http.StatusNotFound, "Receipt not found")
			return
		}
		h.logger.Error("failed to get receipt", "error", err, "receipt_id", id)
		h.respondWithError(w, http.StatusInternalServerError, "Failed to retrieve receipt")
		return
	}

	response := models.NewSuccessResponse(http.StatusOK, "Receipt retrieved successfully", receipt)

	h.respondWithJSON(w, http.StatusOK, response)
}

// CreateReceipt handles POST /api/v1/receipts
// It creates a new receipt with items
//
//	@Summary		Create a new receipt
//	@Description	Create a new receipt with items
//	@Tags			receipts
//	@Accept			json
//	@Produce		json
//	@Param			receipt	body		models.Receipt			true	"Receipt data"
//	@Success		201		{object}	models.SuccessResponse	"Created receipt"
//	@Failure		400		{object}	models.ErrorResponse	"Bad request"
//	@Failure		500		{object}	models.ErrorResponse	"Internal server error"
//	@Router			/receipts [post]
func (h *ReceiptHandler) CreateReceipt(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var receipt models.Receipt
	if err := json.NewDecoder(r.Body).Decode(&receipt); err != nil {
		h.respondWithError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Validate required fields
	if receipt.StoreName == "" {
		h.respondWithError(w, http.StatusBadRequest, "Store name is required")
		return
	}

	if receipt.Date == "" {
		h.respondWithError(w, http.StatusBadRequest, "Date is required")
		return
	}

	if receipt.Subtotal < 0 {
		h.respondWithError(w, http.StatusBadRequest, "Subtotal cannot be negative")
		return
	}

	// Validate items
	for i, item := range receipt.Items {
		if item.Name == "" {
			h.respondWithError(w, http.StatusBadRequest, fmt.Sprintf("Item name is required for item %d", i+1))
			return
		}
		if item.Quantity <= 0 {
			h.respondWithError(w, http.StatusBadRequest, fmt.Sprintf("Item quantity must be positive for item %d", i+1))
			return
		}
		if item.UnitPrice < 0 {
			h.respondWithError(w, http.StatusBadRequest, fmt.Sprintf("Item unit price cannot be negative for item %d", i+1))
			return
		}
	}

	if err := h.repo.Create(ctx, &receipt); err != nil {
		h.logger.Error("failed to create receipt", "error", err, "store_name", receipt.StoreName)
		h.respondWithError(w, http.StatusInternalServerError, "Failed to create receipt")
		return
	}

	h.logger.Info("receipt created", "receipt_id", receipt.ID, "store_name", receipt.StoreName, "items_count", len(receipt.Items))
	response := models.NewSuccessResponse(http.StatusCreated, "Receipt created successfully", receipt)
	h.respondWithJSON(w, http.StatusCreated, response)
}

// UpdateReceipt handles PUT /api/v1/receipts/{id}
// It updates an existing receipt
//
//	@Summary		Update receipt
//	@Description	Update an existing receipt's information
//	@Tags			receipts
//	@Accept			json
//	@Produce		json
//	@Param			id		path		int				true	"Receipt ID"
//	@Param			receipt	body		models.Receipt	true	"Updated receipt data"
//	@Success		200		{object}	models.SuccessResponse	"Updated receipt"
//	@Failure		400		{object}	models.ErrorResponse	"Bad request"
//	@Failure		404		{object}	models.ErrorResponse	"Receipt not found"
//	@Failure		500		{object}	models.ErrorResponse	"Internal server error"
//	@Router			/receipts/{id} [put]
func (h *ReceiptHandler) UpdateReceipt(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	idStr := chi.URLParam(r, "id")

	if idStr == "" {
		h.respondWithError(w, http.StatusBadRequest, "Receipt ID is required")
		return
	}

	id, err := strconv.Atoi(idStr)
	if err != nil {
		h.respondWithError(w, http.StatusBadRequest, "Invalid receipt ID")
		return
	}

	var receipt models.Receipt
	if err := json.NewDecoder(r.Body).Decode(&receipt); err != nil {
		h.respondWithError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	receipt.ID = id

	// Validate required fields
	if receipt.StoreName == "" {
		h.respondWithError(w, http.StatusBadRequest, "Store name is required")
		return
	}

	if receipt.Date == "" {
		h.respondWithError(w, http.StatusBadRequest, "Date is required")
		return
	}

	if receipt.Subtotal < 0 {
		h.respondWithError(w, http.StatusBadRequest, "Subtotal cannot be negative")
		return
	}

	if err := h.repo.Update(ctx, &receipt); err != nil {
		if err.Error() == "receipt not found" {
			h.respondWithError(w, http.StatusNotFound, "Receipt not found")
			return
		}
		h.logger.Error("failed to update receipt", "error", err, "receipt_id", id)
		h.respondWithError(w, http.StatusInternalServerError, "Failed to update receipt")
		return
	}

	h.logger.Info("receipt updated", "receipt_id", id, "store_name", receipt.StoreName)
	response := models.NewSuccessResponse(http.StatusOK, "Receipt updated successfully", receipt)
	h.respondWithJSON(w, http.StatusOK, response)
}

// DeleteReceipt handles DELETE /api/v1/receipts/{id}
// It deletes a receipt
//
//	@Summary		Delete receipt
//	@Description	Delete a receipt by ID
//	@Tags			receipts
//	@Accept			json
//	@Produce		json
//	@Param			id	path	int	true	"Receipt ID"
//	@Success		204	{object}	models.SuccessResponse	"Receipt deleted successfully"
//	@Failure		400	{object}	models.ErrorResponse	"Bad request"
//	@Failure		404	{object}	models.ErrorResponse	"Receipt not found"
//	@Failure		500	{object}	models.ErrorResponse	"Internal server error"
//	@Router			/receipts/{id} [delete]
func (h *ReceiptHandler) DeleteReceipt(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	idStr := chi.URLParam(r, "id")

	if idStr == "" {
		h.respondWithError(w, http.StatusBadRequest, "Receipt ID is required")
		return
	}

	id, err := strconv.Atoi(idStr)
	if err != nil {
		h.respondWithError(w, http.StatusBadRequest, "Invalid receipt ID")
		return
	}

	if err := h.repo.Delete(ctx, id); err != nil {
		if err.Error() == "receipt not found" {
			h.respondWithError(w, http.StatusNotFound, "Receipt not found")
			return
		}
		h.logger.Error("failed to delete receipt", "error", err, "receipt_id", id)
		h.respondWithError(w, http.StatusInternalServerError, "Failed to delete receipt")
		return
	}

	h.logger.Info("receipt deleted", "receipt_id", id)
	response := models.NewSuccessResponse(http.StatusNoContent, "Receipt deleted successfully", nil)
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
func (h *ReceiptHandler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	data := map[string]interface{}{
		"service": "receipts-api",
		"version": "1.0.0",
	}
	response := models.NewSuccessResponse(http.StatusOK, "Service is healthy", data)
	h.respondWithJSON(w, http.StatusOK, response)
}

// Helper methods for consistent JSON responses

func (h *ReceiptHandler) respondWithJSON(w http.ResponseWriter, code int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		h.logger.Error("failed to encode response", "error", err)
	}
}

func (h *ReceiptHandler) respondWithError(w http.ResponseWriter, code int, message string) {
	response := models.NewErrorResponse(code, message)
	h.respondWithJSON(w, code, response)
}