package extraction

import (
	"fmt"
	"strings"
	"time"
)

// Config selects and configures the provider (all from env; see config.Load).
type Config struct {
	Provider string // EXTRACTION_PROVIDER: xai (default) | anthropic | none
	Model    string // EXTRACTION_MODEL
	// ReasoningEffort (EXTRACTION_REASONING_EFFORT) is sent only if the model supports it.
	ReasoningEffort string
	XAIAPIKey       string // XAI_API_KEY (k8s Secret -> env). Never commit.
	XAIBaseURL      string // XAI_BASE_URL (tests point this at httptest)
	Timeout         time.Duration
}

// NewProvider returns the configured provider, or (nil, nil) when extraction is disabled.
func NewProvider(c Config) (Provider, error) {
	switch strings.ToLower(strings.TrimSpace(c.Provider)) {
	case "", "xai", "grok":
		p, err := NewXAIProvider(c.XAIBaseURL, c.XAIAPIKey, c.Model, c.Timeout)
		if err != nil {
			return nil, err
		}
		p.ReasoningEffort = c.ReasoningEffort
		return p, nil
	case "anthropic":
		return NewAnthropicProvider(c.Model), nil
	case "none", "off", "disabled":
		return nil, nil
	default:
		return nil, fmt.Errorf("unknown EXTRACTION_PROVIDER %q (want xai, anthropic or none)", c.Provider)
	}
}
