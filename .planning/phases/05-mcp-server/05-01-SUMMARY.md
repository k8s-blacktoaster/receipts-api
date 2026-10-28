# Summary 05-01: MCP server

**Branch:** phase-05-mcp. **Requirements:** MCP-01..MCP-06. No migration needed (pg_trgm index from 006).

## Delivered
- `internal/mcp`: stateless Streamable HTTP MCP server (`POST /mcp`, JSON responses; initialize,
  ping, tools/list, tools/call; 2025-06-18 + 2025-03-26), bearer auth (constant time) + Origin guard.
  Internal errors are not leaked to clients (logged server-side only).
- `repository.QueryRepository`: read-only (READ ONLY tx) queries; counts parsed/needs_review/reviewed.
- Tools: list_merchants, search_products, search_purchases, where_bought, spend, price_history;
  strict argument decoding (unknown fields rejected), date/limit validation, receipt ids + dates in
  every answer, merchant inputs normalized (MCP-04), redirects followed, fuzzy candidates (MCP-06).
- `MCP_TOKEN` config (>= 32 chars; unset = endpoint not mounted); `router.Mount`; README section.

## Tests
Protocol/auth/tool unit tests with fakes; QueryRepository + HTTP end-to-end integration tests on
the :55434 test DB. go build, go vet, go test ./... green (146 PASS, 0 SKIP). No live LLM calls.

## Open questions (PR)
See PR description: static bearer vs OAuth 2.1, which statuses count, same server vs separate
deployment, no session/SSE, where_bought window, search_products vs alias-only.
