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
	"receipts-db/internal/extraction"
	"receipts-db/internal/handlers"
	"receipts-db/internal/imaging"
	"receipts-db/internal/mcp"
	"receipts-db/internal/redact"
	"receipts-db/internal/repository"
	"receipts-db/internal/router"
	"receipts-db/internal/storage"
	"receipts-db/internal/web"
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
	logger.Info("starting receipts-api",
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

	receiptRepo := repository.NewReceiptRepository(db)

	receiptHandler := handlers.NewReceiptHandler(receiptRepo, logger)

	// ---- photo upload + extraction (phase 2) ----
	docRepo := repository.NewSourceDocumentRepository(db)
	blobs, err := storage.NewLocal(cfg.UploadDir)
	if err != nil {
		logger.Error("failed to init upload storage", "error", err)
		os.Exit(1)
	}
	provider, err := extraction.NewProvider(extraction.Config{
		Provider: cfg.ExtractionProvider, Model: cfg.ExtractionModel, ReasoningEffort: cfg.ExtractionReasoningEffort,
		XAIAPIKey:  cfg.XAIAPIKey,
		XAIBaseURL: cfg.XAIBaseURL, Timeout: time.Duration(cfg.ExtractionTimeoutSec) * time.Second,
	})
	if err != nil {
		// uploads still work; receipts stay "uploaded" until a provider is configured
		logger.Warn("extraction disabled", "reason", redact.String(err.Error()))
		provider = nil
	}
	if xp, ok := provider.(*extraction.XAIProvider); ok {
		if os.Getenv("EXTRACTION_REASONING_EFFORT") != "" && xp.SentReasoningEffort() == "" {
			logger.Warn("EXTRACTION_REASONING_EFFORT not supported by model; not sent",
				"model", xp.Model(), "effort", cfg.ExtractionReasoningEffort)
		}
		logger.Info("extraction provider", "model", xp.Model(), "reasoning_effort", xp.SentReasoningEffort(),
			"timeout_s", cfg.ExtractionTimeoutSec)
	}
	workerCtx, stopWorkers := context.WithCancel(context.Background())
	defer stopWorkers()
	var queue *extraction.Queue
	if provider == nil && err == nil {
		logger.Warn("extraction disabled", "reason", "EXTRACTION_PROVIDER=none")
	}
	if provider != nil {
		logger.Info("extraction enabled", "provider", provider.Name(), "model", provider.Model())
		pipeline := &extraction.Pipeline{Receipts: receiptRepo, Docs: docRepo, Blobs: blobs, Provider: provider,
			ToleranceCents: cfg.ExtractionToleranceCents, Logger: logger}
		queue = extraction.NewQueue(pipeline, 100, time.Duration(cfg.ExtractionTimeoutSec+30)*time.Second, logger)
		queue.Start(workerCtx, cfg.ExtractionWorkers)
		// Same rule at startup and periodically: "parsing" rows older than job timeout + grace
		// go back to "uploaded", then everything "uploaded" is (re)queued.
		queue.Recover(workerCtx, receiptRepo.ResetStaleParsing, receiptRepo.ListIDsByStatus)
		queue.StartSweeper(workerCtx, time.Duration(cfg.ExtractionSweepSec)*time.Second,
			receiptRepo.ResetStaleParsing, receiptRepo.ListIDsByStatus)
	}
	uploadHandler := &handlers.UploadHandler{Store: docRepo, Receipts: receiptRepo, Blobs: blobs,
		MaxBytes: cfg.MaxUploadBytes, Logger: logger, KeepOriginal: cfg.KeepOriginalUploads}
	imaging.SetDecodeBudget(cfg.ImageDecodeBudgetBytes)
	logger.Info("image decode budget", "bytes", imaging.DecodeBudget().Cap())
	if dec := imaging.FindHEICDecoder(cfg.HEICDecoder); dec != nil {
		uploadHandler.HEIC = dec // avoid a typed-nil interface
		logger.Info("HEIC uploads enabled", "decoder", dec.Name())
	} else {
		logger.Warn("no HEIC decoder found (install libheif-tools); HEIC uploads will get 415")
	}
	if queue != nil {
		uploadHandler.Queue = queue
	}

	ui, err := web.New(web.UI{Receipts: receiptRepo, Members: repository.NewMemberRepository(db),
		Merchants: repository.NewMerchantRepository(db), Products: repository.NewProductRepository(db), Docs: docRepo, Blobs: blobs, Uploads: uploadHandler,
		MaxBytes: cfg.MaxUploadBytes, Logger: logger})
	if err != nil {
		logger.Error("web UI templates", "error", err)
		os.Exit(1)
	}
	if queue != nil {
		ui.Queue = queue
	}
	productHandler := &handlers.ProductHandler{Products: repository.NewProductRepository(db), Logger: logger}
	mounts := []router.Mount{{Path: "/readyz", Handler: handlers.Readiness{DB: db, Timeout: 2 * time.Second}}}
	if cfg.MCPToken != "" {
		mcpServer := &mcp.Server{Name: "receipts-api", Version: "1.0", Logger: logger,
			Tools: mcp.Tools(mcp.Deps{Query: repository.NewQueryRepository(db), Products: repository.NewProductRepository(db)})}
		mounts = append(mounts, router.Mount{Path: "/mcp", Handler: mcp.RequireBearer(cfg.MCPToken, mcpServer)})
		logger.Info("MCP endpoint enabled", "path", "/mcp", "tools", len(mcpServer.Tools))
	} else {
		logger.Warn("MCP endpoint disabled (set MCP_TOKEN to enable)")
	}
	handler := router.New(receiptHandler, uploadHandler, productHandler, ui, logger, mounts...)

	srv := &http.Server{
		Addr:         fmt.Sprintf("%s:%s", cfg.Host, cfg.Port),
		Handler:      handler,
		ReadTimeout:  60 * time.Second, // photo uploads from phones on slow links
		WriteTimeout: 60 * time.Second,
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

	httpErr := srv.Shutdown(ctx)
	if httpErr != nil {
		logger.Error("server forced to shutdown", "error", httpErr)
	}

	if queue != nil {
		// stop taking jobs; let in-flight provider calls finish up to EXTRACTION_SHUTDOWN_TIMEOUT
		if !queue.Shutdown(time.Duration(cfg.ExtractionShutdownSec) * time.Second) {
			logger.Warn("in-flight extractions cancelled; they will be retried on next start")
		}
	}
	if httpErr != nil {
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
