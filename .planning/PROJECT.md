# Project: receipts-api

## What This Is
Self-hosted household receipt ledger. Receipt photos from **any merchant** (home
improvement, grocery, warehouse clubs, gas, restaurants, ...) are parsed by a vision LLM
into structured Postgres data and exposed through a read-only MCP server so Grok Bot can
answer "when / where did I buy X", "how much did we spend at <merchant> last month", and
"has the price of X gone up over the last year".

## Core Value
Ask a natural-language question about past purchases and get a correct, cited answer.

## Context
- Brownfield: Go + chi + lib/pq + Postgres 15 REST API (receipts, receipt_items,
  analytics views), devcontainer, docker-compose, Dockerfile.
- Runs on home Kubernetes cluster reached over VPN (same pattern as other apps).
- Working copy: ~/_code/receipts-api (Mac). All work on branch feature/receipt-ingestion-mcp.

## Constraints
- **Merchant-agnostic.** Every merchant is modelled identically (merchant -> store
  location -> receipt -> line items with optional sku / item_number / upc). No
  merchant-specific columns, code paths, types or tests.
- Self-hosted; receipt images stay in-cluster. LLM may be hosted API or in-cluster.
- MCP server is read-only.
- Money stored as integer cents (BIGINT), never float.

## Validated (existing)
- CRUD REST for receipts + items, swagger docs, SQL migration runner, health endpoint.

## Key Decisions
| Decision | Status |
|---|---|
| Money as BIGINT cents | Decided (phase 1) |
| Merchant-agnostic schema, no merchant special-casing | Decided (Jack, 2026-10-27) |
| Single user vs household members | **Pending** - `household_member_id` kept NULLABLE |
| Vision model: hosted vs in-cluster | Pending (Phase 2) |
| How Grok Bot reaches MCP: tunnel + OAuth vs VPN-only | Pending (Phase 4/5) |
