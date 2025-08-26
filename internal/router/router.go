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

func New(productHandler *handlers.ProductHandler, logger *slog.Logger) http.Handler {
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

	r.Get("/api/v1/health", productHandler.HealthCheck)

	r.Route("/api/v1/products", func(r chi.Router) {
		r.Get("/", productHandler.ListProducts)         // GET /api/v1/products
		r.Post("/", productHandler.CreateProduct)       // POST /api/v1/products
		r.Get("/{id}", productHandler.GetProduct)       // GET /api/v1/products/{id}
		r.Put("/{id}", productHandler.UpdateProduct)    // PUT /api/v1/products/{id}
		r.Delete("/{id}", productHandler.DeleteProduct) // DELETE /api/v1/products/{id}
	})

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
