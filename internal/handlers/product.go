package handlers

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"receipts-db/internal/models"
	"receipts-db/internal/repository"
)

// ProductHandler serves canonical products and the NORM-02 product review queue.
type ProductHandler struct {
	Products repository.ProductRepository
	Logger   *slog.Logger
}

func (h *ProductHandler) json(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil && h.Logger != nil {
		h.Logger.Error("failed to encode response", "error", err)
	}
}

func (h *ProductHandler) fail(w http.ResponseWriter, code int, msg string) {
	h.json(w, code, models.NewErrorResponse(code, msg))
}

func intQuery(r *http.Request, name string, def, max int) int {
	v, err := strconv.Atoi(r.URL.Query().Get(name))
	if err != nil || v < 0 {
		return def
	}
	if max > 0 && v > max {
		return max
	}
	return v
}

// SearchProducts handles GET /api/v1/products
//
//	@Summary		Search products
//	@Description	Canonical products whose name or any merchant alias matches q (substring or trigram); empty q lists products.
//	@Tags			products
//	@Produce		json
//	@Param			q		query		string	false	"Search text"
//	@Param			limit	query		int		false	"Max results (max 50)"	default(10)
//	@Success		200		{object}	models.SuccessResponse{data=[]models.Product}
//	@Failure		500		{object}	models.ErrorResponse
//	@Router			/products [get]
func (h *ProductHandler) SearchProducts(w http.ResponseWriter, r *http.Request) {
	ps, err := h.Products.SearchProducts(r.Context(), r.URL.Query().Get("q"), intQuery(r, "limit", 10, 50))
	if err != nil {
		h.Logger.Error("product search failed", "error", err)
		h.fail(w, http.StatusInternalServerError, "Failed to search products")
		return
	}
	h.json(w, http.StatusOK, models.NewSuccessResponse(http.StatusOK, "Products retrieved", ps))
}

// CreateProduct handles POST /api/v1/products
//
//	@Summary		Create product
//	@Tags			products
//	@Accept			json
//	@Produce		json
//	@Param			product	body		models.Product	true	"canonical_name required; brand, category, upc optional"
//	@Success		201		{object}	models.SuccessResponse{data=models.Product}
//	@Failure		400		{object}	models.ErrorResponse
//	@Failure		415		{object}	models.ErrorResponse
//	@Router			/products [post]
func (h *ProductHandler) CreateProduct(w http.ResponseWriter, r *http.Request) {
	var p models.Product
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		h.fail(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if err := p.Validate(); err != nil {
		h.fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.Products.CreateProduct(r.Context(), &p); err != nil {
		h.Logger.Error("create product failed", "error", err)
		h.fail(w, http.StatusInternalServerError, "Failed to create product")
		return
	}
	h.json(w, http.StatusCreated, models.NewSuccessResponse(http.StatusCreated, "Product created", p))
}

// ListReviewItems handles GET /api/v1/review/items
//
//	@Summary		Product review queue
//	@Description	Unmapped receipt lines (no product yet) on receipts that are not uploaded/parsing, newest first, with the fuzzy suggestion if any.
//	@Tags			review
//	@Produce		json
//	@Param			limit	query		int	false	"Page size (max 200)"	default(50)
//	@Param			offset	query		int	false	"Offset"				default(0)
//	@Success		200		{object}	models.PaginatedResponse{data=[]models.ReviewItem}
//	@Failure		500		{object}	models.ErrorResponse
//	@Router			/review/items [get]
func (h *ProductHandler) ListReviewItems(w http.ResponseWriter, r *http.Request) {
	limit, offset := intQuery(r, "limit", 50, 200), intQuery(r, "offset", 0, 0)
	if limit == 0 {
		limit = 50
	}
	items, total, err := h.Products.ListReviewItems(r.Context(), limit, offset)
	if err != nil {
		h.Logger.Error("review queue failed", "error", err)
		h.fail(w, http.StatusInternalServerError, "Failed to load review queue")
		return
	}
	h.json(w, http.StatusOK, models.NewPaginatedResponse(http.StatusOK, "Review items retrieved", items,
		&models.PaginationMeta{Limit: limit, Offset: offset, Total: total}))
}

// MapItemRequest maps a line to an existing product, or creates one first.
type MapItemRequest struct {
	ProductID  int64           `json:"product_id,omitempty"`
	NewProduct *models.Product `json:"new_product,omitempty"`
}

// MapItem handles POST /api/v1/review/items/{id}/map
//
//	@Summary		Map a line to a product
//	@Description	Sets the line's product (match_source manual), learns a merchant-scoped alias (printed text + item number) and applies it to that merchant's other unmapped lines. Give exactly one of product_id or new_product.
//	@Tags			review
//	@Accept			json
//	@Produce		json
//	@Param			id		path		int				true	"Receipt item ID"
//	@Param			body	body		MapItemRequest	true	"Target product"
//	@Success		200		{object}	models.SuccessResponse{data=models.MapResult}
//	@Failure		400		{object}	models.ErrorResponse
//	@Failure		404		{object}	models.ErrorResponse	"Item or product not found"
//	@Failure		409		{object}	models.ErrorResponse	"Receipt is uploaded/parsing"
//	@Failure		415		{object}	models.ErrorResponse
//	@Router			/review/items/{id}/map [post]
func (h *ProductHandler) MapItem(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		h.fail(w, http.StatusBadRequest, "Invalid item ID")
		return
	}
	var req MapItemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.fail(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if (req.ProductID > 0) == (req.NewProduct != nil) || req.ProductID < 0 {
		h.fail(w, http.StatusBadRequest, "Give exactly one of product_id or new_product")
		return
	}
	if p := req.NewProduct; p != nil {
		if err := p.Validate(); err != nil {
			h.fail(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := h.Products.CreateProduct(r.Context(), p); err != nil {
			h.Logger.Error("create product failed", "error", err)
			h.fail(w, http.StatusInternalServerError, "Failed to create product")
			return
		}
		req.ProductID = p.ID
	}
	res, err := h.Products.MapItem(r.Context(), id, req.ProductID)
	if code, msg := MapItemError(err); code != 0 {
		if code == http.StatusInternalServerError {
			h.Logger.Error("map item failed", "error", err, "item_id", id)
		}
		h.fail(w, code, msg)
		return
	}
	h.json(w, http.StatusOK, models.NewSuccessResponse(http.StatusOK, "Item mapped", res))
}

// MapItemError maps repository errors from MapItem to HTTP (0 = success). Shared with the web UI.
func MapItemError(err error) (int, string) {
	switch {
	case err == nil:
		return 0, ""
	case errors.Is(err, repository.ErrNotFound):
		return http.StatusNotFound, "Receipt item not found"
	case errors.Is(err, repository.ErrProductNotFound):
		return http.StatusNotFound, "Product not found"
	case errors.Is(err, repository.ErrReceiptBusy):
		return http.StatusConflict, "Receipt is queued for or undergoing extraction; try again when it finishes"
	}
	return http.StatusInternalServerError, "Failed to map item"
}
