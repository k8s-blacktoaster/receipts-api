package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testServer() http.Handler {
	s := &Server{Name: "t", Version: "0", Tools: []Tool{
		{Name: "echo", InputSchema: map[string]any{"type": "object"},
			Call: func(_ context.Context, a json.RawMessage) (any, error) { return map[string]any{"args": a}, nil }},
		{Name: "bad", Call: func(context.Context, json.RawMessage) (any, error) { return nil, &ToolError{Message: "nope"} }},
		{Name: "boom", Call: func(context.Context, json.RawMessage) (any, error) { return nil, errors.New("pq: secret detail") }},
	}}
	return RequireBearer("s3cret", s)
}

func post(t *testing.T, h http.Handler, body string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "http://example.test/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer s3cret")
	for k, v := range hdr {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return m
}

func TestAuth(t *testing.T) {
	h := testServer()
	for name, hdr := range map[string]map[string]string{
		"missing": {"Authorization": ""}, "wrong": {"Authorization": "Bearer nope"},
		"basic": {"Authorization": "Basic czNjcmV0"}, "prefix": {"Authorization": "Bearer s3cret2"},
	} {
		rec := post(t, h, `{"jsonrpc":"2.0","id":1,"method":"ping"}`, hdr)
		if rec.Code != 401 || rec.Header().Get("WWW-Authenticate") == "" {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	if rec := post(t, RequireBearer("", testServer()), `{}`, nil); rec.Code != 401 {
		t.Errorf("empty token must reject: %d", rec.Code)
	}
	if rec := post(t, h, `{"jsonrpc":"2.0","id":1,"method":"ping"}`, map[string]string{"Origin": "http://evil.test"}); rec.Code != 403 {
		t.Errorf("cross origin: %d", rec.Code)
	}
	if rec := post(t, h, `{"jsonrpc":"2.0","id":1,"method":"ping"}`, map[string]string{"Origin": "http://example.test"}); rec.Code != 200 {
		t.Errorf("same origin: %d", rec.Code)
	}
}

func TestProtocol(t *testing.T) {
	h := testServer()
	rec := post(t, h, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"c","version":"1"}}}`, nil)
	m := decode(t, rec)
	res := m["result"].(map[string]any)
	if rec.Code != 200 || res["protocolVersion"] != "2025-03-26" || res["capabilities"].(map[string]any)["tools"] == nil {
		t.Fatalf("initialize %v", m)
	}
	if res := decode(t, post(t, h, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`, nil))["result"].(map[string]any); res["protocolVersion"] != SupportedVersions[0] {
		t.Fatalf("fallback version %v", res)
	}
	if rec := post(t, h, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, nil); rec.Code != 202 || rec.Body.Len() != 0 {
		t.Fatalf("notification %d %q", rec.Code, rec.Body.String())
	}
	if rec := post(t, h, `{"jsonrpc":"2.0","id":2,"method":"ping"}`, map[string]string{"MCP-Protocol-Version": "2000-01-01"}); rec.Code != 400 {
		t.Fatalf("bad version header %d", rec.Code)
	}
	if m := decode(t, post(t, h, `{"jsonrpc":"2.0","id":"a","method":"ping"}`, map[string]string{"MCP-Protocol-Version": "2025-06-18"})); m["id"] != "a" || m["error"] != nil {
		t.Fatalf("ping %v", m)
	}
	tools := decode(t, post(t, h, `{"jsonrpc":"2.0","id":3,"method":"tools/list"}`, nil))["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 3 || tools[0].(map[string]any)["annotations"].(map[string]any)["readOnlyHint"] != true {
		t.Fatalf("tools %v", tools)
	}
	for body, code := range map[string]float64{
		`{"jsonrpc":"2.0","id":4,"method":"nope"}`:                             codeMethodNotFound,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"x"}}`: codeInvalidParams,
		`{bad`: codeParse,
		`[{"jsonrpc":"2.0","id":1,"method":"ping"}]`: codeInvalidRequest,
		`{"jsonrpc":"1.0","id":1,"method":"ping"}`:   codeInvalidRequest,
	} {
		m := decode(t, post(t, h, body, nil))
		if e, _ := m["error"].(map[string]any); e == nil || e["code"] != code {
			t.Errorf("%s -> %v", body, m)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "http://example.test/mcp", nil)
	req.Header.Set("Authorization", "Bearer s3cret")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 405 {
		t.Fatalf("GET %d", rec.Code)
	}
	if rec := post(t, h, `{"jsonrpc":"2.0","id":1,"method":"ping","params":{"x":"`+strings.Repeat("a", maxBody)+`"}}`, nil); rec.Code != 413 {
		t.Fatalf("big body %d", rec.Code)
	}
}

func TestToolCall(t *testing.T) {
	h := testServer()
	res := decode(t, post(t, h, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{"a":1}}}`, nil))["result"].(map[string]any)
	if res["isError"] != false || res["structuredContent"].(map[string]any)["args"].(map[string]any)["a"] != 1.0 {
		t.Fatalf("echo %v", res)
	}
	if !strings.Contains(res["content"].([]any)[0].(map[string]any)["text"].(string), `"a":1`) {
		t.Fatalf("text content %v", res)
	}
	res = decode(t, post(t, h, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"bad"}}`, nil))["result"].(map[string]any)
	if res["isError"] != true || res["structuredContent"].(map[string]any)["error"] != "nope" {
		t.Fatalf("tool error %v", res)
	}
	rec := post(t, h, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"boom"}}`, nil)
	if strings.Contains(rec.Body.String(), "secret detail") || !strings.Contains(rec.Body.String(), "internal error") {
		t.Fatalf("internal error leaked: %s", rec.Body.String())
	}
}
