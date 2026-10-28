package handlers

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

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
		if errors.Is(err, repository.ErrNotFound) {
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

	if err := receipt.Validate(); err != nil {
		h.respondWithError(w, http.StatusBadRequest, err.Error())
		return
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
//	@Description	Update an existing receipt's information. Omitting "status" (or sending the current one)
//	@Description	keeps it. Users may only make review moves: parsed -> needs_review|reviewed, needs_review -> reviewed,
//	@Description	failed -> reviewed, reviewed -> needs_review. Anything else (e.g. to parsing, failed or uploaded) is 409.
//	@Description	While the receipt is "uploaded" (queued) or "parsing" every PUT is 409. On 409 the receipt is left unchanged.
//	@Tags			receipts
//	@Accept			json
//	@Produce		json
//	@Param			id		path		int				true	"Receipt ID"
//	@Param			receipt	body		models.Receipt	true	"Updated receipt data"
//	@Success		200		{object}	models.SuccessResponse	"Updated receipt"
//	@Failure		400		{object}	models.ErrorResponse	"Bad request"
//	@Failure		404		{object}	models.ErrorResponse	"Receipt not found"
//	@Failure		409		{object}	models.ErrorResponse	"Invalid status transition, or receipt is queued/being extracted"
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

	if err := receipt.Validate(); err != nil {
		h.respondWithError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.repo.Update(ctx, &receipt); err != nil {
		if errors.Is(err, repository.ErrReceiptBusy) {
			h.respondWithError(w, http.StatusConflict, "Receipt is queued for or being extracted; retry after extraction finishes (receipt unchanged)")
			return
		}
		if errors.Is(err, repository.ErrInvalidTransition) {
			move := strings.Replace(strings.TrimPrefix(err.Error(), repository.ErrInvalidTransition.Error()+": "), " -> ", " to ", 1)
			h.respondWithError(w, http.StatusConflict, "Invalid status transition from "+move+"; receipt unchanged")
			return
		}
		if errors.Is(err, repository.ErrNotFound) {
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
//	@Failure		409	{object}	models.ErrorResponse	"Receipt is being extracted"
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
		if errors.Is(err, repository.ErrReceiptBusy) {
			h.respondWithError(w, http.StatusConflict, "Receipt is being extracted; delete it after extraction finishes")
			return
		}
		if errors.Is(err, repository.ErrNotFound) {
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
