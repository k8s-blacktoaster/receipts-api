# Roadmap: receipts-api v1.0

- [x] **Phase 1: Schema & model hardening** - merchants/stores, line items, products/aliases, members, pipeline status, cents money (merged 2026-10-27, PR #1)
- [x] **Phase 2: Upload + extraction pipeline** - photo upload, object store, queue, pluggable vision LLM (Grok default), validation
- [x] **Phase 3: Mobile web UI** - camera capture, multi-photo, upload status, receipt list, review/edit
- [x] **Phase 4: Normalization & review** - product matching, alias learning, review endpoints
- [x] **Phase 5: MCP server** - read-only tools over Streamable HTTP + auth
- [ ] **Phase 6: Kubernetes deploy & backups** (PR open on phase-06-deploy) - CNPG, object store, secrets, ingress, restore drill

### Phase 1: Schema & model hardening
**Goal**: DB can represent a real receipt from any merchant faithfully
**Requirements**: DATA-01..DATA-08
**Success Criteria**:
  1. Migrations 003/004 apply and roll back cleanly (up/down/up) on a copy of existing data
  2. A hand-entered receipt with instant savings round-trips through the repository with exact cent totals
  3. Analytics views use total (not subtotal); "where did I buy X" is one indexed query
**Plans**: 01-01 (phases/01-schema-model-hardening)
**Status**: Executed - see 01-01-SUMMARY.md

### Phase 2: Upload + extraction pipeline
**Goal**: A phone photo becomes a stored, validated receipt without manual entry
**Depends on**: Phase 1
**Requirements**: ING-01..ING-05
**Plan notes**:
  - `Extractor` interface (image(s) + JSON schema -> structured receipt). Default provider:
    Grok via xAI API (vision model, structured JSON output). Anthropic/others selectable by
    config (`EXTRACTOR_PROVIDER`, model name). API key from k8s Secret / env var only.
  - Tests run against recorded fixtures and a mock provider; no live API calls in CI.
  - Status transitions: uploaded -> parsing -> parsed | needs_review | failed (parse_error).
  - ingest_jobs queue table deferred: receipts.status is the durable state (re-queued on start).
**Success Criteria**:
  1. POST /api/v1/uploads returns 202 + job id; duplicate image returns existing receipt
  2. Golden set of 20 real receipts across several merchants: total matches on >=95%
  3. Mismatched receipts land in needs_review, never silently saved as parsed
**Plans**: 02-01 (phases/02-upload-extraction)
**Status**: Executed - see 02-01-SUMMARY.md (golden-set run pending real photos + key)

### Phase 3: Mobile web UI
**Depends on**: Phase 2
**Requirements**: UI-01..UI-07, ING-09
**Scope**: camera capture on the phone, upload status, receipt list with parse/review status,
detail page (image beside fields/items), edit/review/approve, re-extract, member picker,
merchant rename. Server-rendered html/template + small vanilla JS, embedded with go:embed,
VPN-only, no chat. Multi-photo receipts deferred (one photo per upload).
**Plans**: 03-01 (phases/03-web-ui)
**Status**: Executed on phase-03-web-ui, PR open - see 03-01-SUMMARY.md
**Success Criteria**: from a phone on VPN, capture -> upload -> see status -> fix a line -> mark reviewed

### Phase 4: Normalization & review
**Depends on**: Phase 2 (UI from Phase 3 consumes it)
**Requirements**: NORM-01..NORM-04 (NORM-03: colliding aliases kept with item_number cleared, not dropped; NORM-04: substring merchant/location check)
**Success Criteria**: >=90% of lines with an item number/UPC auto-map; corrections create merchant-scoped aliases reused next time (pg_trgm fuzzy indexes land here)

### Phase 5: MCP server
**Depends on**: Phase 4
**Requirements**: MCP-01..MCP-06
**Notes**: merchant-name inputs are normalized with the shared Go function (MCP-04); list_merchants
(MCP-05); pg_trgm fuzzy fallback for nicknames like "TJ's" (MCP-06) uses the merchants.name GIN
trigram index from migration 006.
**Plans**: 05-01 (phases/05-mcp-server)
**Status**: Executed on phase-05-mcp, PR open - see 05-01-SUMMARY.md

### Phase 6: Kubernetes deploy & backups
**Depends on**: Phase 2 (can run in parallel with 3-5)
**Requirements**: OPS-01..OPS-07 (OPS-03: UTF-8 locale/collation, or explicit collation in merchant_name_key; OPS-07: memory limit + global pixel budget)
**Plan notes** (mirror /Users/michael/_code/world-cup-site, read 2026-10-27):
  - `.github/workflows/build-api.yml`: on push to main (paths filter) + workflow_dispatch,
    `runs-on: [self-hosted, k3s]`, sha7 tag, `docker build --target test .` gate, build, push
    `registry.k8s.blacktoaster.com/receipts/api:<sha7>`, step summary.
  - `helm/`: Chart.yaml, values.yaml (namespace, host, api.tag, ingress {className nginx,
    ssl-redirect, tlsSecretName}, certificate {issuerRef vault-issuer/ClusterIssuer/cert-manager.io},
    externalSecrets {secretStore vault-backend, vaultKey apps/receipts/app}); templates
    api.yaml (Deployment + Service; XAI_API_KEY and DB password from the synced Secret),
    externalsecret.yaml (properties xai-api-key, db-password), certificate.yaml, ingress.yaml,
    postgres (CNPG or StatefulSet) and a PVC for UPLOAD_DIR until the object store lands.

**Plans**: 06-01 (phases/06-deploy) - see 06-01-SUMMARY.md

### Phase 7: Legacy column cleanup (later)
**Depends on**: Phases 2-4 (all readers on merchant_id/store_id)
**Scope**: drop legacy receipts.store_* columns (store_name/category/address/phone as stored
columns) once every reader uses merchants/stores; paired down migration restores them.

## Decisions (2026-10-27, Jack via CoS)
- Never edit a migration that has already been pushed; add a new one (from 013 on).
- Rolling back 004 collapses status to review_state ('auto'/'needs_review'/'reviewed'); accepted, documented in 004 down.
- Case-insensitive merchant matching/merging (e.g. in store_spending_view) is intended.
- Legacy store_* columns kept for now; cleanup is Phase 7.
- members FKs (member_id, uploaded_by_id, reviewed_by_id) stay nullable.

## Future ideas (not planned)
- Store-account imports (e.g. Costco online receipts) via a generic import path. Unofficial
  APIs / ToS risk would need review first.
- Email-in ingestion; local vision model A/B on the golden set.
