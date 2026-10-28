# Project: receipts-api

## What This Is
Self-hosted **household** receipt ledger (Jack + wife). Receipt **photos** from any merchant
(home improvement, grocery, warehouse clubs, gas, restaurants, ...) are parsed by a vision
LLM into structured Postgres data, reviewed/corrected in a mobile-friendly web UI, and
exposed through a read-only MCP server so Grok Bot can answer "when / where did I buy X",
"how much did we spend at <merchant> last month", and "has the price of X gone up".

v1 ingests receipt photos only. No store-account connectors in v1.

## Core Value
Ask a natural-language question about past purchases and get a correct, cited answer.

## Context
- Brownfield: Go + chi + lib/pq + Postgres REST API, devcontainer, docker-compose, Dockerfile.
- Runs on home Kubernetes cluster reached over VPN (same pattern as other apps).
- Working copy: ~/_code/receipts-api (Mac). Work on branch feature/receipt-ingestion-mcp.

## Constraints
- **Merchant-agnostic.** Every merchant is modelled identically (merchant -> store
  location -> receipt -> line items with optional sku / item_number / upc). No
  merchant-specific columns, code paths, types or tests.
- Self-hosted; images stay in-cluster. Web UI and API are VPN-only.
- Extraction LLM is pluggable behind a provider interface; API keys only in k8s secrets / env.
- MCP server is read-only. Money stored as integer cents (BIGINT), never float.

## Validated (existing)
- CRUD REST for receipts + items, swagger docs, SQL migration runner, health endpoint.

## Key Decisions
| Decision | Status |
|---|---|
| Money as BIGINT cents | Decided (phase 1) |
| Merchant-agnostic schema, no merchant special-casing | Decided (Jack) |
| v1 = receipt-photo parsing only, no connectors | Decided (Jack) |
| Household app: `members` table; receipts.member_id / uploaded_by_id FKs | Decided (Jack); FKs nullable for legacy/unattributed rows |
| Extraction LLM: Grok (xAI vision, structured JSON) preferred, provider interface, swappable by config | Decided (Jack) |
| Mobile-friendly web UI embedded in the Go binary, VPN-only | Decided (Jack) |
| How Grok Bot reaches MCP: tunnel + OAuth vs VPN-only | Pending (Phase 5) |
