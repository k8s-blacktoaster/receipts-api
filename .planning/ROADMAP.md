# Roadmap: receipts-api v1.0

- [x] **Phase 1: Schema & model hardening** - merchants/stores, line items, products/aliases, source docs, cents money (complete 2026-10-27, pending /gsd-verify-work)
- [ ] **Phase 2: Upload + extraction pipeline** - image upload, object store, queue, vision LLM, validation
- [ ] **Phase 3: Normalization & review** - product matching, alias learning, review endpoints
- [ ] **Phase 4: MCP server** - read-only tools over Streamable HTTP + auth
- [ ] **Phase 5: Kubernetes deploy & backups** - CNPG, object store, secrets, ingress/tunnel, restore drill

### Phase 1: Schema & model hardening
**Goal**: DB can represent a real receipt from any merchant faithfully
**Depends on**: Nothing (first phase)
**Requirements**: DATA-01..DATA-07
**Success Criteria** (what must be TRUE):
  1. Migration 003 applies and rolls back cleanly (up/down/up) on a copy of existing data
  2. A hand-entered receipt with instant savings round-trips through the repository with exact cent totals
  3. Analytics views use total (not subtotal); "where did I buy X" is one indexed query
**Plans**: 01-01 (see phases/01-schema-model-hardening)
**Status**: Executed

### Phase 2: Upload + extraction pipeline
**Goal**: A phone photo becomes a stored, validated receipt without manual entry
**Depends on**: Phase 1
**Requirements**: ING-01..ING-04
**Success Criteria**:
  1. POST /api/v1/uploads returns 202 + job id; duplicate image returns existing receipt
  2. Golden set of 20 real receipts across several merchants: total matches on >=95%
  3. Mismatched receipts land in needs_review, never silently saved as done
  (ingest_jobs queue table lands here, not in phase 1)

### Phase 3: Normalization & review
**Depends on**: Phase 2
**Requirements**: NORM-01, NORM-02
**Success Criteria**: >=90% of lines with an item number/UPC auto-map; corrections create merchant-scoped aliases reused next time (pg_trgm fuzzy indexes land here)

### Phase 4: MCP server
**Depends on**: Phase 3
**Requirements**: MCP-01..MCP-03

### Phase 5: Kubernetes deploy & backups
**Depends on**: Phase 2 (can run in parallel with 3-4)
**Requirements**: OPS-01, OPS-02

## Milestone v1.1 (later)
- Phase 6: Email-in ingestion, share-sheet polish
- Phase 7: Local vision model A/B on golden set

## Maybe later
- Merchant online-receipt sync (see REQUIREMENTS backlog)
