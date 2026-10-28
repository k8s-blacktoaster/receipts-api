# syntax=docker/dockerfile:1
# Stages mirror world-cup-site/backend: base -> test (CI gate: docker build --target test) -> build -> runtime.
# HEIC uploads are decoded by the libheif CLI (heif-dec), so the binary stays CGO_ENABLED=0.
FROM golang:1.23-alpine AS base
RUN apk add --no-cache git libheif-tools
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

# Unit tests (DB integration tests skip without TEST_DATABASE_URL). The HEIC test uses heif-dec.
FROM base AS test
RUN go vet ./... && go test ./...

FROM base AS build
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/receipts-api ./cmd/api

FROM alpine:3.21 AS runtime
RUN apk add --no-cache ca-certificates libheif-tools \
 && addgroup -g 1000 -S appuser && adduser -u 1000 -S appuser -G appuser
WORKDIR /app
COPY --from=build /out/receipts-api .
COPY --from=build /src/migrations ./migrations
RUN mkdir -p /app/data/uploads && chown -R appuser:appuser /app
USER appuser
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:8080/api/v1/health || exit 1
ENTRYPOINT ["./receipts-api"]
