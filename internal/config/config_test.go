package config

import "testing"

func TestExtractionConfigFromEnv(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x/y_test")
	t.Setenv("XAI_API_KEY", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ExtractionProvider != "xai" || cfg.XAIBaseURL != "https://api.x.ai/v1" || cfg.ExtractionToleranceCents != 2 ||
		cfg.UploadDir != "data/uploads" || cfg.MaxUploadBytes != 20<<20 || cfg.XAIAPIKey != "" {
		t.Fatalf("defaults: %+v", cfg)
	}
	t.Setenv("EXTRACTION_PROVIDER", "anthropic")
	t.Setenv("EXTRACTION_MODEL", "some-model")
	t.Setenv("XAI_API_KEY", "from-env")
	t.Setenv("EXTRACTION_TOLERANCE_CENTS", "5")
	t.Setenv("EXTRACTION_WORKERS", "4")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ExtractionProvider != "anthropic" || cfg.ExtractionModel != "some-model" || cfg.XAIAPIKey != "from-env" ||
		cfg.ExtractionToleranceCents != 5 || cfg.ExtractionWorkers != 4 {
		t.Fatalf("overrides: %+v", cfg)
	}
	t.Setenv("EXTRACTION_TOLERANCE_CENTS", "-1")
	if _, err := Load(); err == nil {
		t.Fatal("negative tolerance must fail")
	}
}

func TestExtractionShutdownTimeout(t *testing.T) {
	t.Setenv("EXTRACTION_SHUTDOWN_TIMEOUT", "")
	if c, _ := Load(); c.ExtractionShutdownSec != 25 {
		t.Fatalf("default = %d", c.ExtractionShutdownSec)
	}
	t.Setenv("EXTRACTION_SHUTDOWN_TIMEOUT", "7")
	if c, _ := Load(); c.ExtractionShutdownSec != 7 {
		t.Fatalf("env = %d", c.ExtractionShutdownSec)
	}
}
