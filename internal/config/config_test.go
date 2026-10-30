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

func TestExtractionSweepInterval(t *testing.T) {
	t.Setenv("EXTRACTION_STALE_SWEEP_INTERVAL", "")
	if c, _ := Load(); c.ExtractionSweepSec != 60 {
		t.Fatalf("default = %d", c.ExtractionSweepSec)
	}
	t.Setenv("EXTRACTION_STALE_SWEEP_INTERVAL", "0")
	if c, _ := Load(); c.ExtractionSweepSec != 0 {
		t.Fatalf("disabled = %d", c.ExtractionSweepSec)
	}
}

func TestExtractionTimeoutAndReasoningEffort(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x/y_test")
	cfg, err := Load()
	if err != nil || cfg.ExtractionTimeoutSec != 300 || cfg.ExtractionReasoningEffort != "low" {
		t.Fatalf("defaults: %v %+v", err, cfg)
	}
	t.Setenv("EXTRACTION_REASONING_EFFORT", " LOW ")
	if cfg, err = Load(); err != nil || cfg.ExtractionReasoningEffort != "low" {
		t.Fatalf("low: %v %q", err, cfg.ExtractionReasoningEffort)
	}
	t.Setenv("EXTRACTION_REASONING_EFFORT", "max")
	if _, err := Load(); err == nil {
		t.Fatal("invalid effort must fail")
	}
}

func TestMCPToken(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x/y_test")
	t.Setenv("MCP_TOKEN", "")
	if cfg, err := Load(); err != nil || cfg.MCPToken != "" {
		t.Fatalf("default: %v %v", cfg, err)
	}
	t.Setenv("MCP_TOKEN", "short")
	if _, err := Load(); err == nil {
		t.Fatal("short MCP_TOKEN must fail")
	}
	t.Setenv("MCP_TOKEN", " 0123456789abcdef0123456789abcdef ")
	if cfg, err := Load(); err != nil || cfg.MCPToken != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("token: %v", err)
	}
}

func TestImageDecodeBudget(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x/y_test")
	t.Setenv("IMAGE_DECODE_BUDGET_BYTES", "")
	if cfg, err := Load(); err != nil || cfg.ImageDecodeBudgetBytes != 0 {
		t.Fatalf("default: %v %v", cfg, err)
	}
	t.Setenv("IMAGE_DECODE_BUDGET_BYTES", "419430400")
	if cfg, err := Load(); err != nil || cfg.ImageDecodeBudgetBytes != 400<<20 {
		t.Fatalf("set: %v %v", cfg, err)
	}
	t.Setenv("IMAGE_DECODE_BUDGET_BYTES", "-1")
	if _, err := Load(); err == nil {
		t.Fatal("negative budget must fail")
	}
}
