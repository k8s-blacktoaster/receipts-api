package extraction

import "context"

// AnthropicProvider is a placeholder so the provider can be swapped by config later.
type AnthropicProvider struct{ model string }

func NewAnthropicProvider(model string) *AnthropicProvider { return &AnthropicProvider{model: model} }

func (p *AnthropicProvider) Name() string  { return "anthropic" }
func (p *AnthropicProvider) Model() string { return p.model }
func (p *AnthropicProvider) Extract(context.Context, []Image) (*Result, error) {
	return nil, ErrNotImplemented
}
