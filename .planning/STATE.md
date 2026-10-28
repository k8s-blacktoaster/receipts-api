# State

**Milestone:** v1.0
**Current phase:** 3 - Mobile web UI
**Status:** phases 1-2 merged (PR #1, PR #2); phase 3 executed on phase-03-web-ui, PR open, awaiting review
**Last updated:** 2026-10-27

## Notes
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
