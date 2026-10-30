package router

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"receipts-db/internal/httpguard"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	httpSwagger "github.com/swaggo/http-swagger"
	"receipts-db/internal/handlers"
	"receipts-db/internal/models"

	_ "receipts-db/docs" // This is required for Swagger
)

// UI registers the web pages (internal/web); nil disables the UI.
type UI interface{ Register(r chi.Router) }

// Mount is an extra handler served at an exact path for every method (e.g. the MCP endpoint
// at /mcp). It gets the common middleware but not the JSON/same-origin guards: it does its own
// auth and origin checks.
type Mount struct {
	Path    string
	Handler http.Handler
}

func New(receiptHandler *handlers.ReceiptHandler, uploadHandler *handlers.UploadHandler, productHandler *handlers.ProductHandler, ui UI, logger *slog.Logger, mounts ...Mount) http.Handler {
	r := chi.NewRouter()

	// Middleware stack
	r.Use(middleware.RequestID)                 // Add request ID for tracing
	r.Use(middleware.RealIP)                    // Get real IP from headers
	r.Use(middleware.Recoverer)                 // Recover from panics
	r.Use(LoggerMiddleware(logger))             // Custom logging middleware
	r.Use(middleware.Timeout(60 * time.Second)) // Request timeout

	r.Get("/swagger/*", httpSwagger.Handler(
		httpSwagger.URL("/swagger/doc.json"), // Use relative URL instead of absolute
	))

	r.Get("/api/v1/health", receiptHandler.HealthCheck)

	r.Route("/api/v1/receipts", func(r chi.Router) {
		r.Use(httpguard.SameOrigin)
		r.Get("/", receiptHandler.ListReceipts)                                  // GET /api/v1/receipts
		r.With(httpguard.RequireJSON).Post("/", receiptHandler.CreateReceipt)    // POST /api/v1/receipts
		r.Get("/{id}", receiptHandler.GetReceipt)                                // GET /api/v1/receipts/{id}
		r.With(httpguard.RequireJSON).Put("/{id}", receiptHandler.UpdateReceipt) // PUT /api/v1/receipts/{id}
		r.Delete("/{id}", receiptHandler.DeleteReceipt)                          // DELETE /api/v1/receipts/{id}
		if uploadHandler != nil {
			r.Post("/{id}/extract", uploadHandler.ReextractReceipt) // POST /api/v1/receipts/{id}/extract
		}
	})

	if uploadHandler != nil {
		r.With(httpguard.SameOrigin).Post("/api/v1/uploads", uploadHandler.UploadReceiptPhoto) // POST /api/v1/uploads (multipart)
	}

	if productHandler != nil {
		r.Group(func(r chi.Router) {
			r.Use(httpguard.SameOrigin)
			r.Get("/api/v1/products", productHandler.SearchProducts)                                    // GET /api/v1/products?q=
			r.With(httpguard.RequireJSON).Post("/api/v1/products", productHandler.CreateProduct)        // POST /api/v1/products
			r.Get("/api/v1/review/items", productHandler.ListReviewItems)                               // GET /api/v1/review/items
			r.With(httpguard.RequireJSON).Post("/api/v1/review/items/{id}/map", productHandler.MapItem) // POST /api/v1/review/items/{id}/map
		})
	}

	for _, m := range mounts {
		r.Handle(m.Path, m.Handler)
	}

	if ui != nil {
		ui.Register(r)
	}

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		response := models.NewErrorResponse(http.StatusNotFound, "Route not found")
		json.NewEncoder(w).Encode(response)
	})

	return r
}

func LoggerMiddleware(logger *slog.Logger) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			wrapped := &responseWriter{
				ResponseWriter: w,
				statusCode:     http.StatusOK,
			}

			next.ServeHTTP(wrapped, r)

			logger.Info("http request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", wrapped.statusCode,
				"duration", time.Since(start).String(),
				"request_id", middleware.GetReqID(r.Context()),
				"remote_addr", r.RemoteAddr,
			)
		})
	}
}

type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}
