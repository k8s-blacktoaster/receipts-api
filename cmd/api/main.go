// @title			Receipts API
// @version		1.0
// @description	API for Receipts API
// @termsOfService	http://swagger.io/terms/
//
// @contact.name	API Support
// @contact.url	http://www.swagger.io/support
// @contact.email	support@swagger.io
//
// @license.name	MIT
// @license.url	https://opensource.org/licenses/MIT
//
// @host		localhost:8080
// @BasePath	/api/v1
//
// @schemes	http https
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"receipts-db/internal/config"
	"receipts-db/internal/database"
	"receipts-db/internal/handlers"
	"receipts-db/internal/repository"
	"receipts-db/internal/router"
)

func main() {
	if err := godotenv.Load(); err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("failed to load .env file", "error", err)
		}
	}

	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load configuration", "error", err)
		os.Exit(1)
	}

	logger := setupLogger(cfg.LogLevel)
	logger.Info("starting receipts-db",
		"environment", cfg.Environment,
		"port", cfg.Port,
		"database_type", "postgres",
	)

	dbConfig := database.Config{
		URL:      cfg.DatabaseURL,
		MaxConns: cfg.DBMaxConns,
		MaxIdle:  cfg.DBMaxIdle,
	}

	db, err := database.NewConnection(dbConfig)
	if err != nil {
		logger.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	// Run migrations
	logger.Info("running database migrations")
	migrationsPath := os.Getenv("MIGRATIONS_PATH")
	if migrationsPath == "" {
		// Auto-detect migrations path relative to project root
		if _, err := os.Stat("migrations"); err == nil {
			migrationsPath = "migrations"
		} else {
			migrationsPath = "../../migrations"
		}
	}
	if err := database.RunMigrations(db, migrationsPath); err != nil {
		logger.Error("failed to run migrations", "error", err)
		os.Exit(1)
	}

	productRepo := repository.NewProductRepository(db)

	productHandler := handlers.NewProductHandler(productRepo, logger)

	handler := router.New(productHandler, logger)

	srv := &http.Server{
		Addr:         fmt.Sprintf("%s:%s", cfg.Host, cfg.Port),
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		logger.Info("server starting", "address", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server failed to start", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("server forced to shutdown", "error", err)
		os.Exit(1)
	}

	logger.Info("server stopped")
}

// setupLogger configures structured logging with slog
func setupLogger(level string) *slog.Logger {
	var logLevel slog.Level

	switch level {
	case "debug":
		logLevel = slog.LevelDebug
	case "info":
		logLevel = slog.LevelInfo
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{
		Level:     logLevel,
		AddSource: level == "debug",
	}

	var handler slog.Handler
	if os.Getenv("ENVIRONMENT") == "production" {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}

	return slog.New(handler)
}
