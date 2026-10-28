package router

import (
	"encoding/json"
	"log/slog"
	"net/http"
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

func New(receiptHandler *handlers.ReceiptHandler, uploadHandler *handlers.UploadHandler, ui UI, logger *slog.Logger) http.Handler {
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
		r.Get("/", receiptHandler.ListReceipts)         // GET /api/v1/receipts
		r.Post("/", receiptHandler.CreateReceipt)       // POST /api/v1/receipts
		r.Get("/{id}", receiptHandler.GetReceipt)       // GET /api/v1/receipts/{id}
		r.Put("/{id}", receiptHandler.UpdateReceipt)    // PUT /api/v1/receipts/{id}
		r.Delete("/{id}", receiptHandler.DeleteReceipt) // DELETE /api/v1/receipts/{id}
		if uploadHandler != nil {
			r.Post("/{id}/extract", uploadHandler.ReextractReceipt) // POST /api/v1/receipts/{id}/extract
		}
	})

	if uploadHandler != nil {
		r.Post("/api/v1/uploads", uploadHandler.UploadReceiptPhoto) // POST /api/v1/uploads (multipart)
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
