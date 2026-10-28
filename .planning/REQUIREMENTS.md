# Requirements: receipts-api

## v1 Requirements
### Data model
- [x] **DATA-01**: Schema stores merchants, store locations, receipts, line items, adjustments (instant savings/coupons/discounts), subtotal, tax, total
- [x] **DATA-02**: Each receipt can link to its source image/document, raw extraction JSON and parse/review state
- [x] **DATA-03**: Money columns are integer cents; API models stop using float64 for money
- [x] **DATA-04**: Receipts can record a household member (NULLABLE until single-user vs household is decided)
- [x] **DATA-05**: Merchant-agnostic model - receipts from any merchant (home improvement, grocery, gas, restaurants, warehouse clubs, ...) use the same tables. `merchants` (chain) + `stores` (location: address, store number). Line items carry generic optional `sku`, `item_number`, `upc`. No merchant-specific columns, JSONB shapes, code paths, types or tests; only a generic optional `metadata` JSONB.
- [x] **DATA-06**: Product aliases are merchant-scoped (unique per merchant + alias text, and per merchant + item number) and map to canonical products
- [x] **DATA-07**: "Where did I buy X" (product -> merchant + store location + date + price) is a single indexed query, exposed as a repository method with a test
### Ingestion
- [ ] **ING-01**: Upload a receipt photo via HTTPS (multipart) from phone over VPN
- [ ] **ING-02**: Images stored in object storage keyed by sha256; duplicates rejected
- [ ] **ING-03**: Background worker extracts JSON via vision LLM with strict schema
- [ ] **ING-04**: Validation (items vs subtotal, subtotal+tax vs total) flags receipts for review
### Normalization
- [ ] **NORM-01**: Line items map to canonical products via merchant-scoped item number / UPC, then alias, then fuzzy/LLM suggestion
- [ ] **NORM-02**: Unmapped/low-confidence lines appear in a review queue and are fixable
### MCP
- [ ] **MCP-01**: Streamable HTTP MCP endpoint with read-only tools (search, where-bought, spend, price history)
- [ ] **MCP-02**: Every answer returns receipt ids/dates so results are verifiable
- [ ] **MCP-03**: MCP requires authentication
### Deploy
- [ ] **OPS-01**: Manifests for api, worker, mcp, Postgres (CNPG), object store, secrets
- [ ] **OPS-02**: Nightly DB backup + image bucket backup, tested restore

## v2 Requirements
- Email-in ingestion, review web UI, in-cluster model, budgets/alerts

## Maybe later / backlog
- Merchant online-receipt sync (e.g. a retailer's unofficial receipts API). Not planned;
  would have to go through the same generic source_documents path, no special-casing.

## Out of Scope
- Bank/card transaction import, multi-tenant SaaS, write tools in MCP
- Merchant-specific schema or code
