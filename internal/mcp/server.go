// Package mcp is a minimal, read-only Model Context Protocol server over Streamable HTTP
// (phase 5, MCP-01). Each POST carries one JSON-RPC message and gets one application/json
// response; the server never opens an SSE stream (GET -> 405) and keeps no session.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
)

// SupportedVersions lists protocol revisions this server speaks, newest first.
var SupportedVersions = []string{"2025-06-18", "2025-03-26"}

const maxBody = 1 << 20

// Tool is one MCP tool. Call returns the structured result; a *ToolError becomes an
// isError tool result (visible to the model), any other error a generic isError result.
type Tool struct {
	Name        string
	Description string
	InputSchema map[string]any
	Call        func(ctx context.Context, args json.RawMessage) (any, error)
}

// ToolError is an error the caller can act on (bad input, unknown merchant, ...).
type ToolError struct {
	Message string `json:"error"`
	Details any    `json:"details,omitempty"`
}

func (e *ToolError) Error() string { return e.Message }

type Server struct {
	Name, Version string
	Tools         []Tool
	Logger        *slog.Logger
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

const (
	codeParse          = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternal       = -32603
)

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if v := r.Header.Get("MCP-Protocol-Version"); v != "" && !slices.Contains(SupportedVersions, v) {
		http.Error(w, "unsupported MCP-Protocol-Version", http.StatusBadRequest)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}
	trimmed := strings.TrimSpace(string(body))
	if strings.HasPrefix(trimmed, "[") {
		writeJSON(w, rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"),
			Error: &rpcError{codeInvalidRequest, "batch requests are not supported"}})
		return
	}
	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{codeParse, "parse error"}})
		return
	}
	if req.JSONRPC != "2.0" {
		writeJSON(w, rpcResponse{JSONRPC: "2.0", ID: idOrNull(req.ID), Error: &rpcError{codeInvalidRequest, "jsonrpc must be 2.0"}})
		return
	}
	if req.Method == "" { // a response or malformed message from the client: accept, nothing to say
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if len(req.ID) == 0 { // notification (e.g. notifications/initialized)
		w.WriteHeader(http.StatusAccepted)
		return
	}
	result, rerr := s.dispatch(r.Context(), req)
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	if rerr != nil {
		resp.Error = rerr
	} else {
		resp.Result = result
	}
	writeJSON(w, resp)
}

func idOrNull(id json.RawMessage) json.RawMessage {
	if len(id) == 0 {
		return json.RawMessage("null")
	}
	return id
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) dispatch(ctx context.Context, req rpcRequest) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		v := SupportedVersions[0]
		if slices.Contains(SupportedVersions, p.ProtocolVersion) {
			v = p.ProtocolVersion
		}
		return map[string]any{
			"protocolVersion": v,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": s.Name, "version": s.Version},
			"instructions": "Read-only access to household receipts. Money is integer cents. " +
				"Every answer includes receipt ids and dates; cite them. Merchant names are normalized " +
				"keys (e.g. trader_joes); call list_merchants when unsure.",
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		tools := make([]map[string]any, 0, len(s.Tools))
		for _, t := range s.Tools {
			tools = append(tools, map[string]any{"name": t.Name, "description": t.Description,
				"inputSchema": t.InputSchema, "annotations": map[string]any{"readOnlyHint": true, "openWorldHint": false}})
		}
		return map[string]any{"tools": tools}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil || p.Name == "" {
			return nil, &rpcError{codeInvalidParams, "params.name is required"}
		}
		i := slices.IndexFunc(s.Tools, func(t Tool) bool { return t.Name == p.Name })
		if i < 0 {
			return nil, &rpcError{codeInvalidParams, "unknown tool: " + p.Name}
		}
		args := p.Arguments
		if len(args) == 0 || string(args) == "null" {
			args = json.RawMessage("{}")
		}
		out, err := s.Tools[i].Call(ctx, args)
		if err != nil {
			var te *ToolError
			if !errors.As(err, &te) {
				if s.Logger != nil {
					s.Logger.Error("mcp tool failed", "tool", p.Name, "error", err)
				}
				te = &ToolError{Message: "internal error"}
			}
			return toolResult(te, true), nil
		}
		return toolResult(out, false), nil
	default:
		return nil, &rpcError{codeMethodNotFound, "method not found: " + req.Method}
	}
}

// toolResult returns the JSON both as text content (for older clients) and structuredContent.
func toolResult(v any, isErr bool) map[string]any {
	b, err := json.Marshal(v)
	if err != nil {
		b, isErr = []byte(`{"error":"internal error"}`), true
	}
	res := map[string]any{"content": []map[string]any{{"type": "text", "text": string(b)}}, "isError": isErr}
	var obj map[string]any
	if json.Unmarshal(b, &obj) == nil {
		res["structuredContent"] = obj
	}
	return res
}
