# syntax=docker/dockerfile:1
# Stages mirror world-cup-site/backend: base -> test (CI gate) -> build -> runtime.
# HEIC uploads are decoded by the libheif CLI (heif-dec), so the binaries stay CGO_ENABLED=0
# but the runtime needs libheif-tools. That rules out distroless; the runtime is a minimal
# alpine image running as a fixed non-root uid (1000) with no shell tooling beyond busybox.
FROM golang:1.23-alpine AS base
RUN apk add --no-cache git libheif-tools
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

# Unit tests. DB integration tests skip without TEST_DATABASE_URL; CI runs this stage's image
# again with TEST_DATABASE_URL pointing at a throwaway Postgres (see .github/workflows/build-api.yml).
# The race detector needs cgo, so only this stage gets a C toolchain (build stays CGO_ENABLED=0).
FROM base AS test
RUN apk add --no-cache build-base
ENV CGO_ENABLED=1
RUN go vet ./... && go test ./...

FROM base AS build
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/receipts-api ./cmd/api \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/migrate ./cmd/migrate

FROM alpine:3.21 AS runtime
RUN apk add --no-cache ca-certificates tzdata libheif-tools \
 && addgroup -g 1000 -S appuser && adduser -u 1000 -S appuser -G appuser \
 && mkdir -p /app/data/uploads && chown -R 1000:1000 /app/data
WORKDIR /app
COPY --from=build /out/receipts-api /out/migrate ./
COPY --from=build /src/migrations ./migrations
ENV MIGRATIONS_PATH=/app/migrations \
    UPLOAD_DIR=/app/data/uploads \
    PORT=8080
USER 1000:1000
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:8080/api/v1/health || exit 1
ENTRYPOINT ["./receipts-api"]
