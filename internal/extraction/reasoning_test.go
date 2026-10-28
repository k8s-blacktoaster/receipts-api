package extraction

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestReasoningEffortOnlySentWhenSupported(t *testing.T) {
	cases := []struct {
		model, effort, want string
	}{
		{"grok-4.20", "low", ""},                    // grok-4.20 has no reasoning_effort
		{"grok-4.20-0309-non-reasoning", "low", ""}, // non-reasoning
		{"grok-4.3", "none", "none"},
		{"grok-4.3-latest", "low", "low"},
		{"grok-4.5", "xhigh", ""}, // 4.5 has no xhigh
		{"grok-4.5", "high", "high"},
		{"grok-4.7", "xhigh", "xhigh"},
		{"grok-4.7", "", ""},
		{"grok-4.30", "low", ""}, // prefix must end at a version boundary
	}
	for _, c := range cases {
		p, err := NewXAIProvider("", "test-key", c.model, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		p.ReasoningEffort = c.effort
		body, _ := json.Marshal(p.BuildRequest([]Image{{MIME: "image/jpeg", Data: []byte{1}}}))
		has := strings.Contains(string(body), `"reasoning_effort"`)
		if p.SentReasoningEffort() != c.want || has != (c.want != "") {
			t.Errorf("%s/%s: sent %q (in body %v), want %q", c.model, c.effort, p.SentReasoningEffort(), has, c.want)
		}
	}
}

func TestNewProviderPassesReasoningEffort(t *testing.T) {
	p, err := NewProvider(Config{Provider: "xai", XAIAPIKey: "k", Model: "grok-4.3", ReasoningEffort: "low"})
	if err != nil || p.(*XAIProvider).SentReasoningEffort() != "low" {
		t.Fatalf("%v %v", p, err)
	}
}
