# State

**Milestone:** v1.0
**Current phase:** 1 - Schema & model hardening
**Status:** executed, awaiting QA verify (/gsd-verify-work 1); phase 2 on hold until QA reports
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
