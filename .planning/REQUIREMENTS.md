# Requirements: receipts-api

## v1 Requirements
### Data model
- [x] **DATA-01**: Schema stores merchants, store locations, receipts, line items, adjustments (instant savings/coupons/discounts), subtotal, tax, total
- [x] **DATA-02**: Each receipt can link to its source image/document; minimal provenance on receipts (`source` = photo|manual, nullable `external_id`)
- [x] **DATA-03**: Money columns are integer cents; API models stop using float64 for money
- [x] **DATA-04**: Household members: `members` table (display name, optional email); receipts reference member (`member_id`) and uploader (`uploaded_by_id`), reviewer (`reviewed_by_id`)
- [x] **DATA-05**: Merchant-agnostic model - receipts from any merchant use the same tables. `merchants` (chain) + `stores` (location: address, store number). Line items carry generic optional `sku`, `item_number`, `upc`. No merchant-specific columns, JSONB shapes, code paths, types or tests; only a generic optional `metadata` JSONB.
- [x] **DATA-06**: Product aliases are merchant-scoped (unique per merchant + alias text, and per merchant + item number) and map to canonical products
- [x] **DATA-07**: "Where did I buy X" (product -> merchant + store location + date + price) is a single indexed query, exposed as a repository method with a test
- [x] **DATA-08**: Receipt status covers the photo pipeline (uploaded, parsing, parsed, needs_review, reviewed, failed) with parse error text and timestamps; line items are editable during review
### Ingestion
- [ ] **ING-01**: Upload one or more receipt photos via HTTPS (multipart) from phone over VPN
- [ ] **ING-02**: Images stored in object storage keyed by sha256; duplicates rejected
- [ ] **ING-03**: Background worker extracts JSON via vision LLM with strict schema
- [ ] **ING-04**: Validation (items vs subtotal, subtotal+tax vs total) flags receipts needs_review
- [ ] **ING-05**: Pluggable extraction LLM: `Extractor` provider interface; Grok (xAI API, vision model, structured JSON output) is the default; Anthropic or others selectable by config. API key from k8s secret / env var, never in the repo. Tests use mocks and fixtures only, no live API calls.
### Web UI
- [ ] **UI-01**: Mobile-friendly web UI: camera capture on the phone, multiple photos per receipt, upload status
- [ ] **UI-02**: Receipt list showing parse/review status; simple edit/review of parsed line items
- [ ] **UI-03**: Served from the same deployment (static assets embedded in the Go binary), VPN-only. No chat or bot in the UI.
### Normalization
- [ ] **NORM-01**: Line items map to canonical products via merchant-scoped item number / UPC, then alias, then fuzzy/LLM suggestion
- [ ] **NORM-02**: Unmapped/low-confidence lines appear in the review queue and are fixable
### MCP
- [ ] **MCP-01**: Streamable HTTP MCP endpoint with read-only tools (search, where-bought, spend, price history)
- [ ] **MCP-02**: Every answer returns receipt ids/dates so results are verifiable
- [ ] **MCP-03**: MCP requires authentication
### Deploy
- [ ] **OPS-01**: Manifests for api(+UI), worker, mcp, Postgres (CNPG), object store, secrets
- [ ] **OPS-02**: Nightly DB backup + image bucket backup, tested restore

## v2 Requirements
- Email-in ingestion, in-cluster model, budgets/alerts

## Out of Scope (v1)
- Store-account / connector imports (see ROADMAP "Future ideas")
- Bank/card transaction import, multi-tenant SaaS, write tools in MCP
- Merchant-specific schema or code
