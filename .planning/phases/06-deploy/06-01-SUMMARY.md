# Phase 6: deploy readiness (06-01 summary)

Branch `phase-06-deploy`. Nothing was applied to the cluster or pushed to the registry.

## Delivered
- **OPS-07:** `internal/imaging/budget.go` is a FIFO weighted semaphore over decoded bytes
  (w*h*bpp, plus 16 MB working set), on top of the 2-way count. The default is 200 MiB, which
  allows one maximum-size image at a time. Requests larger than the cap are clamped and run alone.
  Configured with `IMAGE_DECODE_BUDGET_BYTES`. The chart sets requests 512Mi, limits 1536Mi and
  GOMEMLIMIT 1200MiB.
- **Dockerfile:** base, test, build and runtime stages. The runtime is alpine 3.21 (libheif-tools
  for HEIC rules out distroless) running as uid 1000. It ships `receipts-api`, `migrate` and the
  migrations.
- **helm/:** api Deployment (Recreate, migrate init container, uploads PVC, read-only rootfs plus
  /tmp emptyDir), Service, ConfigMap, Postgres StatefulSet (16-bookworm, initdb en_US.UTF-8),
  ExternalSecret, Ingress (proxy-body-size 25m), Certificate, and a pg_dump CronJob with a PVC.
- **CI:** `.github/workflows/build-api.yml` runs on `[self-hosted, k3s]` and uses the host Docker,
  which is already logged in to the registry, same as world-cup-site. The steps are:
  1. `--target test`.
  2. The test image runs `go test` against a throwaway Postgres container on a private Docker
     network.
  3. Build, verify, then push `registry.k8s.blacktoaster.com/receipts/api:<sha7>`.
- **DEPLOY.md:** the Vault keys, deploy, verify, smoke-test, rollback and restore steps.

## Validation (2026-10-29)
- `helm lint` passes. `helm template` renders 11 objects.
- `docker build --target test` and the full image build both pass. The runtime image is 55 MB,
  uid 1000, and has heif-dec.
- The full suite, integration included, passes inside the test image against
  postgres:16-bookworm with en_US.UTF-8 (`lower('ÉCOLE ŻABKA ÅÄÖ')` gives `école żabka åäö`).
- The backup script was run 3 times with KEEP=2 under uid 999 and a read-only rootfs. It kept the
  newest 2 dumps.
- actionlint passes, with `.github/actionlint.yaml` declaring the `k3s` runner label.
