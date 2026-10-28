package extraction

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"receipts-db/internal/redact"
)

// DefaultXAIModel is the default Grok vision model; override with EXTRACTION_MODEL.
const DefaultXAIModel = "grok-4.20"

// DefaultXAIBaseURL is the xAI OpenAI-compatible API root.
const DefaultXAIBaseURL = "https://api.x.ai/v1"

// XAIProvider calls xAI chat completions with an image and a strict JSON schema.
type XAIProvider struct {
	BaseURL string
	APIKey  string // from XAI_API_KEY only; never logged
	model   string
	HTTP    *http.Client
}

func NewXAIProvider(baseURL, apiKey, model string, timeout time.Duration) (*XAIProvider, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("XAI_API_KEY is required for the xai extraction provider")
	}
	if baseURL == "" {
		baseURL = DefaultXAIBaseURL
	}
	if model == "" {
		model = DefaultXAIModel
	}
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	redact.Register(apiKey)
	return &XAIProvider{BaseURL: strings.TrimRight(baseURL, "/"), APIKey: apiKey, model: model, HTTP: &http.Client{Timeout: timeout}}, nil
}

func (p *XAIProvider) Name() string  { return "xai" }
func (p *XAIProvider) Model() string { return p.model }

type chatRequest struct {
	Model          string         `json:"model"`
	Messages       []chatMessage  `json:"messages"`
	ResponseFormat map[string]any `json:"response_format"`
	Temperature    float64        `json:"temperature"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type contentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *imageURL `json:"image_url,omitempty"`
}

type imageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

type chatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// BuildRequest builds the chat completion body (exported for tests).
func (p *XAIProvider) BuildRequest(images []Image) chatRequest {
	parts := []contentPart{{Type: "text", Text: "Extract this receipt. If there are several photos they are parts of one receipt, in order."}}
	for _, img := range images {
		parts = append(parts, contentPart{Type: "image_url", ImageURL: &imageURL{
			URL: "data:" + img.MIME + ";base64," + base64.StdEncoding.EncodeToString(img.Data), Detail: "high"}})
	}
	return chatRequest{
		Model: p.model,
		Messages: []chatMessage{
			{Role: "system", Content: SystemPrompt},
			{Role: "user", Content: parts},
		},
		ResponseFormat: map[string]any{"type": "json_schema", "json_schema": map[string]any{
			"name": "receipt", "strict": true, "schema": ReceiptJSONSchema()}},
		Temperature: 0,
	}
}

func (p *XAIProvider) Extract(ctx context.Context, images []Image) (*Result, error) {
	if len(images) == 0 {
		return nil, fmt.Errorf("no images")
	}
	body, err := json.Marshal(p.BuildRequest(images))
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.APIKey)
	resp, err := p.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("xai request failed: %s", redact.String(err.Error(), p.APIKey))
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("xai read failed: %w", err)
	}
	var cr chatResponse
	_ = json.Unmarshal(data, &cr)
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(data))
		if cr.Error != nil && cr.Error.Message != "" {
			msg = cr.Error.Message
		}
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return nil, fmt.Errorf("xai returned %d: %s", resp.StatusCode, redact.String(msg, p.APIKey))
	}
	if len(cr.Choices) == 0 || strings.TrimSpace(cr.Choices[0].Message.Content) == "" {
		return nil, fmt.Errorf("xai returned no content")
	}
	raw := json.RawMessage(cr.Choices[0].Message.Content)
	var ex Extracted
	if err := json.Unmarshal(raw, &ex); err != nil {
		return nil, fmt.Errorf("xai returned invalid receipt JSON: %w", err)
	}
	model := cr.Model
	if model == "" {
		model = p.model
	}
	return &Result{Receipt: &ex, Raw: raw, Model: model}, nil
}
