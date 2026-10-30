# State

**Milestone:** v1.0
**Current phase:** 5.1 - MCP defaults (phase 6 deploy in parallel)
**Status:** phases 1-5 merged (PR #1-#5); 5.1 on phase-05.1-mcp-defaults
**Last updated:** 2026-10-29

## Notes
- 2026-10-29 phase 7 on phase-07-store-cleanup: migration 014 drops receipts.store_category/
  store_address/store_phone (copied into merchants/stores first), renames store_name ->
  printed_merchant_name. API JSON unchanged. See phases/07-store-cleanup/07-01-SUMMARY.md.
- 2026-10-29 phase 6 (deploy readiness) on phase-06-deploy, independent of 5.1: Dockerfile, helm
  chart, build-api CI, ExternalSecret apps/receipts/app, pg_dump CronJob, UTF-8 initdb, OPS-07
  decode budget, DEPLOY.md. Nothing applied to the cluster. See phases/06-deploy/06-01-SUMMARY.md.
- .planning/ created by hand following the gsd-core layout (PROJECT, REQUIREMENTS,
  ROADMAP, STATE, config.json, phases/NN-slug/). The `npx @opengsd/gsd-core` installer is
  interactive and installs runtime commands, so it was not run on this machine.
  Run it later (local install) and `/gsd-map-codebase` to fill `.planning/codebase/`.
- 2026-10-27 decisions: 004 rollback lossiness accepted; case-insensitive merchant merge
  intended; legacy store_* kept (cleanup = Phase 7); member FKs stay nullable.
- tests/http/receipts.http updated to *_cents API (Jack's edits preserved).
- Phase 2 on hold until QA reports on phase 1.
- 2026-10-27 QA follow-ups F1-F7 done: migration 005 (merchant trim dedupe, money CHECKs,
  store/merchant composite FK, FK indexes, store address upsert, store_name nullable + views
  read merchant/store); `migrate up -steps N`. Awaiting QA re-verify; phase 2 still on hold.
- 2026-10-27 phase 1 merged to main (PR #1). Phase 2 carried over from local WIP onto
  phase-02-upload-extraction: migration 007, uploads, extraction provider (xAI default,
  Anthropic stub), pipeline/worker, docs. See 02-01-SUMMARY.md.
- 2026-10-27 phase 2 merged (PR #2, 9f48834). Phase 3 on phase-03-web-ui: embedded web UI,
  edit/delete guards, rerun-on-skip queue, merchant rename (409 on collision), store location
  capture, upload normalization to one JPEG + HEIC via libheif CLI (migration 008), Docker
  test stage. See phases/03-web-ui/03-01-SUMMARY.md. Live golden check blocked: xAI rejected
  the local .env key ("Incorrect API key provided").
- 2026-10-28 phase 4 merged (PR #4, 248f3a4). Phase 5 (MCP server) executed on phase-05-mcp,
  PR open, awaiting QA. See phases/05-mcp-server/05-01-SUMMARY.md.
- 2026-10-29 phase 5 merged (PR #5). Phase 5.1 decisions: static bearer, needs_review excluded by
  default (include_needs_review opt-in), MCP on API server, stateless JSON, substring search,
  spend incl. tax. See phases/05.1-mcp-defaults/05.1-01-SUMMARY.md.
