package config

import (
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	Port string
	Host string

	DatabaseURL string // PostgreSQL connection string

	DBMaxConns int
	DBMaxIdle  int

	LogLevel string

	Environment string // "development", "production", etc.

	// Uploads / extraction (phase 2). XAI_API_KEY must come from the environment (k8s Secret);
	// it is never read from files in the repo and never logged.
	UploadDir            string
	MaxUploadBytes       int64
	ExtractionProvider   string
	ExtractionModel      string
	XAIAPIKey            string
	XAIBaseURL           string
	ExtractionTimeoutSec int
	// ExtractionShutdownSec is how long SIGTERM waits for in-flight extractions.
	ExtractionShutdownSec    int
	ExtractionWorkers        int
	ExtractionToleranceCents int64
}

func Load() (*Config, error) {
	cfg := &Config{
		Port: getEnv("PORT", "8080"),
		Host: getEnv("HOST", "0.0.0.0"),

		DatabaseURL: getEnv("DATABASE_URL", "postgres://postgres:password@localhost:5432/receipts_db?sslmode=disable"),

		DBMaxConns: getEnvAsInt("DB_MAX_CONNS", 25),
		DBMaxIdle:  getEnvAsInt("DB_MAX_IDLE", 5),

		LogLevel: getEnv("LOG_LEVEL", "info"),

		Environment: getEnv("ENVIRONMENT", "development"),

		UploadDir:                getEnv("UPLOAD_DIR", "data/uploads"),
		MaxUploadBytes:           int64(getEnvAsInt("MAX_UPLOAD_BYTES", 20<<20)),
		ExtractionProvider:       getEnv("EXTRACTION_PROVIDER", "xai"),
		ExtractionModel:          os.Getenv("EXTRACTION_MODEL"),
		XAIAPIKey:                os.Getenv("XAI_API_KEY"),
		XAIBaseURL:               getEnv("XAI_BASE_URL", "https://api.x.ai/v1"),
		ExtractionTimeoutSec:     getEnvAsInt("EXTRACTION_TIMEOUT_SECONDS", 120),
		ExtractionShutdownSec:    getEnvAsInt("EXTRACTION_SHUTDOWN_TIMEOUT", 25),
		ExtractionWorkers:        getEnvAsInt("EXTRACTION_WORKERS", 2),
		ExtractionToleranceCents: int64(getEnvAsInt("EXTRACTION_TOLERANCE_CENTS", 2)),
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return cfg, nil
}

func (c *Config) Validate() error {
	if c.DatabaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}

	port, err := strconv.Atoi(c.Port)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("invalid PORT: must be between 1 and 65535")
	}

	validLogLevels := map[string]bool{
		"debug": true,
		"info":  true,
		"warn":  true,
		"error": true,
	}
	if !validLogLevels[c.LogLevel] {
		return fmt.Errorf("invalid LOG_LEVEL: must be debug, info, warn, or error")
	}

	if c.ExtractionToleranceCents < 0 {
		return fmt.Errorf("invalid EXTRACTION_TOLERANCE_CENTS: must be >= 0")
	}
	return nil
}

func (c *Config) IsDevelopment() bool {
	return c.Environment == "development"
}

func (c *Config) IsProduction() bool {
	return c.Environment == "production"
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvAsInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return defaultValue
}
