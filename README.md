# Go PostgreSQL API Template

A production-ready Go API template with PostgreSQL, featuring CRUD operations, database migrations, Swagger documentation, and dev container support.

## Features

- 🚀 **Ready-to-use REST API** with CRUD operations
- 🐘 **PostgreSQL integration** with connection pooling and migrations
- 📚 **Swagger/OpenAPI documentation** with auto-generation
- 🐳 **Dev Container support** for consistent development environment
- 🔧 **Hot reload** with Air for development
- ✅ **Structured logging** with slog
- 🧪 **Testing setup** with database integration
- 📦 **Clean architecture** with separated layers (handlers, repository, models)

## Quick Start

### Option 1: Dev Container (Recommended)

1. **Clone and customize the template:**
   ```bash
   # Clone the repository
   git clone <your-repo-url>
   cd <your-project-name>
   
   # Run the setup script to customize the template
   ./setup.sh
   ```

2. **Open in VS Code:**
   - Open the project in VS Code
   - Click "Reopen in Container" when prompted
   - Wait for the container to build

3. **Start debugging:**
   - Press `F5` to start the API with debugging
   - The API will be available at `http://localhost:8080`

### Option 2: Local Development

1. **Prerequisites:**
   - Go 1.21+
   - PostgreSQL
   - Docker (optional, for local PostgreSQL)

2. **Setup:**
   ```bash
   # Start PostgreSQL (using Docker)
   docker-compose up -d postgres
   
   # Or set your own DATABASE_URL
   export DATABASE_URL="postgres://username:password@host:port/database?sslmode=disable"
   
   # Run the API
   go run cmd/api/main.go
   ```

## Template Customization

After cloning, customize the following placeholders:

| Placeholder | Description | Example |
|-------------|-------------|---------|
| `{{MODULE_NAME}}` | Go module name | `github.com/yourusername/your-api` |
| `{{PROJECT_NAME}}` | Project display name | `My Product API` |
| `{{SERVICE_NAME}}` | Service name for logging | `my-product-api` |
| `{{API_TITLE}}` | API title in Swagger | `Product Management API` |
| `{{API_DESCRIPTION}}` | API description | `API for managing product inventory` |
| `{{DB_NAME}}` | Database name | `products_db` |

### Manual Setup (if not using setup script)

1. **Replace all placeholders** in these files:
   - `go.mod`
   - `cmd/api/main.go`
   - `internal/` (all files with imports)
   - `.devcontainer/`
   - `.vscode/launch.json`
   - `docker-compose.yml`

2. **Update the domain model** in `internal/models/` if needed

3. **Regenerate Swagger docs:**
   ```bash
   swag init -g cmd/api/main.go
   ```

## API Endpoints

The API includes a complete Receipts CRUD API:

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/v1/health` | Health check endpoint |
| GET | `/readyz` | Readiness: 200 when the database answers a ping within 2s **and** its applied migrations match this binary's (none missing, none unknown), else 503 (k8s readiness probe) |
| GET | `/api/v1/receipts` | List all receipts |
| GET | `/api/v1/receipts/{id}` | Get a single receipt |
| POST | `/api/v1/receipts` | Create a new receipt |
| PUT | `/api/v1/receipts/{id}` | Update an existing receipt; omitted or unchanged `status` is kept; users may only make review moves (parsed -> needs_review/reviewed, needs_review -> reviewed, failed -> reviewed, reviewed -> needs_review), anything else is 409; every PUT while `parsing` is 409; 400 for unknown values |
| DELETE | `/api/v1/receipts/{id}` | Delete a receipt |
| POST | `/api/v1/uploads` | Upload a receipt photo (multipart `image`, JPEG/PNG/HEIC, max 20 MiB); 202 + queued extraction, 200 if the same image was already uploaded |
| POST | `/api/v1/receipts/{id}/extract` | Re-run extraction for a photo receipt; 409 while parsing and always 409 for `reviewed` receipts (no force option in v1) |
| GET | `/api/v1/products?q=` | Search canonical products by name or any merchant alias (substring or trigram) |
| POST | `/api/v1/products` | Create a product (`canonical_name` required; `brand`, `category`, `upc`) |
| GET | `/api/v1/review/items` | Product review queue: unmapped lines (not on uploaded/parsing receipts) with the fuzzy suggestion |
| POST | `/api/v1/review/items/{id}/map` | Map a line (`{"product_id":N}` or `{"new_product":{...}}`); learns the merchant alias and maps matching unmapped lines; 409 while the receipt is uploaded/parsing |

### Product matching (phase 4)

Every saved line (manual create, edit, or extraction) without a product goes through the matcher,
scoped to the receipt's merchant:

1. merchant item number (`product_aliases.item_number`) -> `match_source=item_number`
2. merchant UPC (`product_aliases.upc`, unique per merchant) -> `upc`. UPCs must be 8, 12, 13 or
   14 digits (400 otherwise); codes read by the model are cleaned and dropped if invalid.
3. printed text (`raw_text`, then `name`) against the merchant's aliases, compared with
   `item_alias_key` (lowercase, whitespace runs collapsed; ALL punctuation kept, so "1/2" != "12"
   and "1.5L" != "1,5L") -> `exact`. Near misses like "COKE-2L" vs "COKE 2L" are fuzzy suggestions.
4. pg_trgm similarity >= 0.4 over the merchant's aliases and all canonical names -> **suggestion
   only** (`suggested_product_id`, `match_confidence`); the line stays unmapped and appears in the
   review queue.

Lines that already carry a `product_id` are kept as-is (`manual`). Mapping a line in the review
queue (API or `/review`) records a manual alias (printed text plus item number) for that merchant,
maps the merchant's other unmapped lines with the same item number or text, and is reused for every
later receipt. No LLM is called for matching (the `llm` source is reserved).

Merging merchants (`/merchants`) moves receipts, folds stores with the same number (or, for a store
without a number, the same address; a numbered store is never folded by address, both are kept),
and moves aliases: a text collision keeps the target's alias; an item-number or UPC collision keeps
the moved alias with that code cleared (NORM-03; migration 006 applies the same rule). The merged
name is recorded in `merchant_name_redirects`, so later receipts printed with it resolve to the
survivor; renaming another merchant onto a redirected name is a 409.

### Web UI

`/` serves a phone-friendly UI from the same binary (templates and static files via `go:embed`;
server-rendered `html/template` plus a little vanilla JS, and it works without JS). It is meant
for the VPN only: there is no login, just a member picker stored in the `receipts_member`
cookie (HttpOnly, SameSite=Lax). Cross-site POSTs are rejected (403, Origin / Sec-Fetch-Site
check; `Origin: null` and `Sec-Fetch-Site: same-site` are refused too). The same guard covers
every write under `/api/v1`, and JSON endpoints require `Content-Type: application/json` (415
otherwise; bodies capped at 1 MiB). There is no chat or bot in the UI.

- **Receipts** (`/receipts`): camera button (`capture=environment`) and library picker
  (`accept="image/*,.heic,.heif"`), list with status badges (uploaded, parsing, parsed,
  needs review, failed, reviewed).
- **Detail** (`/receipts/{id}`): the photo beside the header fields, items and adjustments;
  edit, Approve (-> reviewed), Re-extract, Delete. While a receipt is `uploaded` or `parsing` the
  page auto-refreshes and edits are disabled; the server refuses them with 409 anyway. Re-extract
  explains its 409s (parsing; reviewed receipts can't be re-extracted in v1).
- **Review** (`/review`): unmapped lines; accept the suggestion, pick a candidate product, or
  create a product. The correction becomes a merchant alias used from then on.
- **Merchants** (`/merchants`): rename and merge. The new name is re-normalized; a name that already
  belongs to another merchant is refused with 409 naming it, with a button to merge into it.

### Upload normalization (HEIC)

Every upload (JPEG, PNG, HEIC/HEIF) is converted on the server into **one normalized JPEG**:
EXIF orientation applied, long edge at most 2000 px, quality 80, and **all metadata (EXIF, GPS)
stripped** because the image is re-encoded from pixels. Only that JPEG is stored
(`receipts/<sha[:2]>/<sha>.jpg`) and used for extraction, re-extract and the UI. Duplicate
detection uses the sha256 of the **original** bytes (`source_documents.sha256`); the JPEG's hash
is in `jpeg_sha256`. With `KEEP_ORIGINAL_UPLOADS=true` the original is also kept
(`<sha>.orig.<ext>`, `original_object_key`); by default it is never written.

HEIC is decoded by the libheif CLI (`heif-dec`, or `heif-convert`; `sips` on macOS for dev), so
the binary stays pure Go (`CGO_ENABLED=0`) and a decoder crash can't take down the server. The
container images install `libheif-tools` and the devcontainer `libheif-examples`. Override the
binary with `HEIC_DECODER`. Without a decoder, HEIC uploads get 415 with guidance. xAI only
accepts JPEG/PNG images, so the provider always gets the JPEG.

### Photo extraction

Uploaded photos are stored under `UPLOAD_DIR` and a receipt is created with status `uploaded`.
A background worker moves it `uploaded -> parsing -> parsed | needs_review | failed`, recording
`parse_error`, `parse_started_at` and `parsed_at`. A receipt lands in `needs_review` when a
required field is missing or the printed total does not match subtotal + tax - savings (or
subtotal + tax + adjustments) within `EXTRACTION_TOLERANCE_CENTS`.

Extraction also captures the store location (store number, street, city, state, zip, phone);
it is upserted onto the `stores` row for that merchant (by store number, else by address) and
linked to the receipt, so repeat receipts from the same store share one row.

Status changes follow a fixed transition table (`internal/models/status.go`); invalid changes are
rejected with 409, including via `PUT /receipts/{id}`, which can never set `parsing`. A worker claims a receipt with one atomic `UPDATE ... WHERE status IN (...) RETURNING`,
so concurrent workers, replicas or repeated `/extract` calls never parse the same receipt twice.
`POST /receipts/{id}/extract` returns **409** while the receipt is `parsing`, and **always 409 for
a `reviewed` receipt**: v1 never overwrites human-reviewed data and has no force option.

Uploads are validated before anything is written: the type is detected from magic bytes
(JPEG, PNG, HEIC/HEIF; anything else gets 415), the image must fully decode (truncated files get
400) and `member_id` / `uploaded_by_id` must be existing members (400
otherwise). Provider errors are redacted (API key, `Authorization`/`Bearer` values) before they
are stored in `parse_error`, logged, or returned.

Each claim stores a token (its exact `parse_started_at`). The worker's final write (fields,
items, status), a failure, or a release only applies while the row is still `parsing` under that
token, so a reset/re-claimed receipt or a racing edit is never overwritten. User edits are
refused (409) while a receipt is `uploaded` (queued) or `parsing`, and DELETE is 409 while
`parsing`. Re-extract moves the receipt to `uploaded` first, so a queued job can never overwrite
an edit.

On SIGTERM the server stops accepting HTTP requests and extraction jobs, waits up to
`EXTRACTION_SHUTDOWN_TIMEOUT` seconds (default 25) for in-flight provider calls, then cancels
them; a cancelled job puts its own receipt back to `uploaded` (best-effort, short background
context). As a backstop, at startup and every `EXTRACTION_STALE_SWEEP_INTERVAL` seconds
(default 60, 0 = off) receipts `parsing` longer than `EXTRACTION_TIMEOUT_SECONDS` + 60s (queue job timeout = provider
timeout + 30s, plus 30s grace) are reset to
`uploaded`, and everything `uploaded` is requeued. Credentials in errors are always replaced by
the literal `[REDACTED]`.

Rolling back migration 007 deletes receipts that were never parsed but leaves their image files
in `UPLOAD_DIR` (orphaned; remove by hand).

The extraction provider is chosen with `EXTRACTION_PROVIDER` (`xai` default, `anthropic` stub,
`none`). The xAI provider calls the Grok vision model (`EXTRACTION_MODEL`, default `grok-4.7`)
with a strict JSON schema and prompt `receipt-v4`. `EXTRACTION_REASONING_EFFORT` (default `low`;
`none|low|medium|high|xhigh`) is sent as `reasoning_effort` only to models that accept it
(grok-4.3, 4.5, 4.6, 4.7; not grok-4.20). `EXTRACTION_TIMEOUT_SECONDS` defaults to 300.

Besides the header totals, the review check reconciles line items: sum(line totals) -
sum(item discounts) + adjustments must equal the subtotal within `EXTRACTION_TOLERANCE_CENTS`
(a pre-discount subtotal is accepted when the total reflects the discounts). Items with a
negative price (not returns), discount-looking names (SAVINGS/DISCOUNT/COUPON, a trailing
"3.00-" amount, "/ 12345" references), a deposit/fee counted both as item and adjustment, or a
savings amount equal to an item price put the receipt in `needs_review` with a reason, as does
a merchant name that looks like the branch line (contains a store number, or equals the store
city/address). The prompt asks for the brand only (no "Wholesale"/"Inc." style descriptors,
never the location line).

**API key:** `XAI_API_KEY` is read only from the environment. Never commit it. In Kubernetes:

the helm chart maps the Vault property `xai-api-key` (ExternalSecret) to `XAI_API_KEY`; see DEPLOY.md.

Without a key the API still accepts uploads; they stay `uploaded` until a provider is configured
(they are re-queued on startup).

### MCP server (phase 5)
Read-only [Model Context Protocol](https://modelcontextprotocol.io) tools over Streamable HTTP at
`POST /mcp` on the same server (JSON responses, stateless, no SSE; `GET /mcp` is 405).

- **Auth:** `Authorization: Bearer $MCP_TOKEN`. `MCP_TOKEN` (at least 32 characters; generate with `openssl rand -hex 32`)
  is read from the environment; when it is unset the endpoint is not mounted. Requests with an
  `Origin` header from another host get 403. Keep it VPN-only like the rest of the app.
- **Data:** only receipts with status `parsed` or `reviewed` by default. Every tool takes an optional
  `include_needs_review: true` to also count receipts still in `needs_review`; each answer lists the
  `counted_statuses` it used. Money is integer cents. Every answer carries receipt ids and dates.
- **Merchant names** are normalized with `NormalizeMerchantName` ("Trader Joe's" -> `trader_joes`);
  merged-away names follow redirects; no exact match returns an error with pg_trgm candidates.

| Tool | Arguments | Returns |
|---|---|---|
| `list_merchants` | - | name, receipt_count, total_spent_cents, last_visit, last_receipt_id |
| `search_products` | `query`, `limit` | products + last purchase (receipt id/date) |
| `search_purchases` | `query`, `merchant`, `from`, `to`, `limit` | matching line items, newest first |
| `where_bought` | `product_id`, `limit` | all locations (merchant/store, purchases, last date/price, up to 50 receipt ids) + newest purchases |
| `spend` | `merchant`, `from`, `to`, `group_by` (merchant/month) | totals per group with up to 100 receipt ids (newest first) |
| `price_history` | `product_id`, `merchant`, `limit` | the newest `limit` purchases' unit prices, shown oldest first |

Lists say `truncated` / `receipt_ids_truncated` when more rows exist than were returned.

Decisions (phase 5.1): static bearer token for now (OAuth later); MCP stays on the API server;
stateless JSON transport (no sessions/SSE); `search_purchases` is a case-insensitive substring
match; `spend` sums receipt totals, tax included (tax and savings are reported per group).

Try it:
```bash
export MCP_TOKEN=$(openssl rand -hex 32)   # start the API with the same MCP_TOKEN
curl -s localhost:8080/mcp -H "Authorization: Bearer $MCP_TOKEN" -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"spend","arguments":{"merchant":"Trader Joe'"'"'s","group_by":"month"}}}'
```
MCP clients (Claude Desktop, Cursor, etc.): add a remote/"streamable-http" server with URL
`https://<host>/mcp` and header `Authorization: Bearer <token>`.

### Example Receipt JSON:
```json
{
  "store_name": "Sephora",
  "store_category": "Beauty & Cosmetics",
  "store_address": "26736 Portola Parkway Suite 6-C, Foothill Ranch, CA 92610",
  "store_phone": "(949) 382-1982",
  "date": "2024-08-25",
  "subtotal": 19.00,
  "items": [
    {
      "sku": "2380749",
      "name": "OS EYE PENCIL BOHIAS BLK",
      "description": "12HR Contour Eye Pencil Eyeliner in Bodacious Black - waterproof gel eyeliner pencil",
      "quantity": 1,
      "unit_price": 19.00
    }
  ]
}
```

## API Documentation

- **Swagger UI:** `http://localhost:8080/swagger/index.html`
- **JSON Schema:** `http://localhost:8080/swagger/doc.json`

To regenerate documentation after changes:
```bash
swag init -g cmd/api/main.go
```

## Database

### Schema
The template includes a `products` table with:
- `id` (SERIAL PRIMARY KEY)
- `sku` (VARCHAR, UNIQUE)
- `name` (VARCHAR)
- `description` (TEXT)
- `quantity` (INTEGER)
- `unit_price` (DECIMAL)
- `created_at`, `updated_at` (TIMESTAMP)

### Migrations
- Migration files: `migrations/`
- 014 (phase 7) dropped the legacy `receipts.store_category/store_address/store_phone` columns and
  renamed `store_name` to `printed_merchant_name`. The API's `store_*` JSON fields are unchanged:
  `store_name` is the printed name, category comes from `merchants`, and address, phone and
  number come from `stores`.
- Because those values now live on shared rows, a write that can't be stored gets **422** and
  changes nothing. It is never a 200 that drops the value. This applies to `POST`/`PUT
  /api/v1/receipts` and the web form:
  - `store_category` is written to the merchant only if the merchant has no category yet. The
    same value in any case is accepted. A different value is a 422, because changing it would
    change every receipt of that merchant.
  - `store_phone` is saved on the store row, so it needs `store_number` or `store_address` in the
    same request. Without either, it's a 422. With one, it writes through to the store, like the
    address does.
  - `store_category`, `store_number` or `store_address` without a `store_name` is a 422.
  - Photo extraction is lenient: it never fails a parse over these. It drops a phone that has no
    store location and keeps the merchant's existing category.
- 015 turns blank merchant categories and store addresses/phones into NULL. CHECK constraints keep
  blanks out from then on.
- Auto-run on startup
- Uses `golang-migrate` library

### Configuration
- **Development:** Uses Docker Compose PostgreSQL
- **Production:** Set `DATABASE_URL` environment variable
- **Testing:** Automatic test database creation

## Development

### Local secrets (.env)
Put `XAI_API_KEY=...` (and any other overrides) in `.env` at the repo root. It is gitignored and
in `.dockerignore`; never commit it. The VS Code "Launch API" config loads it via `envFile`.
From a shell:

```bash
set -a; . ./.env; set +a
go run ./cmd/api
```

Automated tests never use the real key; they use fake providers.

### VS Code Integration
- **F5 Debugging:** Automatically configured for dev container (loads `.env` via `envFile`)
- **Extensions:** Pre-installed Go, Docker, and Git extensions
- **Settings:** Go tools, formatting, and linting pre-configured

### Environment Variables
Key configuration options:

```bash
# Database
DATABASE_URL=postgres://user:pass@host:port/dbname?sslmode=disable

# Server
PORT=8080
HOST=0.0.0.0

# Logging
LOG_LEVEL=info  # debug, info, warn, error
ENVIRONMENT=development  # development, production

# Database Pool
DB_MAX_CONNS=25
DB_MAX_IDLE=5

# Uploads / extraction (see "Photo extraction"; XAI_API_KEY from env or k8s Secret only)
UPLOAD_DIR=data/uploads
KEEP_ORIGINAL_UPLOADS=false  # also store the original upload next to the normalized JPEG
HEIC_DECODER=             # default: search heif-dec, heif-convert, sips (macOS)
EXTRACTION_PROVIDER=xai   # xai | anthropic | none
EXTRACTION_MODEL=         # default grok-4.7
EXTRACTION_REASONING_EFFORT=low  # none|low|medium|high|xhigh; only sent if the model supports it
EXTRACTION_TIMEOUT_SECONDS=300
XAI_API_KEY=              # never commit
EXTRACTION_SHUTDOWN_TIMEOUT=25  # seconds SIGTERM waits for in-flight extractions
EXTRACTION_STALE_SWEEP_INTERVAL=60  # seconds between stale-parsing sweeps (0 = off)
IMAGE_DECODE_BUDGET_BYTES=0  # decoded-bitmap bytes in flight across uploads (0 = 200 MiB, OPS-07)
```

### Testing

#### Unit Tests
```bash
# Run tests
go test ./...

# Run with coverage
go test -cover ./...

# Run specific test
go test -v ./internal/repository
```

#### HTTP API Tests
Manual API testing is available using HTTP files in the `tests/http/` directory:

1. **Start the API server:**
   ```bash
   go run cmd/api/main.go
   ```

2. **Run HTTP tests:**
   - Use any HTTP client that supports `.http` files (VS Code REST Client, IntelliJ HTTP Client, etc.)
   - Open `tests/http/receipts.http`
   - Execute requests to test CRUD operations

3. **Available test files:**
   - `receipts.http` - Receipt CRUD operations (POST, GET, DELETE) with base URL configured

### Docker
`docker build --target test .` runs `go vet` and `go test` in the image (DB integration tests
skip without `TEST_DATABASE_URL`; the HEIC test uses `heif-dec`). `docker build .` produces the
runtime image (alpine + `libheif-tools`).

### Building
```bash
# Build binary
go build -o bin/api cmd/api/main.go

# Build for production
CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o bin/api cmd/api/main.go
```

## Project Structure

```
.
├── cmd/api/                 # Application entry point
├── internal/                # Private application code
│   ├── config/             # Configuration management
│   ├── database/           # Database connection and migrations
│   ├── handlers/           # HTTP handlers (controllers)
│   ├── models/             # Domain models and DTOs
│   ├── repository/         # Data access layer
│   └── router/             # HTTP routing and middleware
├── migrations/             # SQL migration files
├── docs/                   # Generated Swagger documentation
├── tests/                  # Test files and utilities
├── .devcontainer/          # Dev container configuration
├── .vscode/                # VS Code settings and launch config
├── docker-compose.yml      # Local PostgreSQL setup
└── Dockerfile              # Production container (optional)
```

## Production Deployment

receipts-api deploys to the k3s cluster with the helm chart in `helm/`. CI
(`.github/workflows/build-api.yml`) builds and pushes `registry.k8s.blacktoaster.com/receipts/api:<sha7>`
after vet, unit and Postgres integration tests pass. Secrets come from Vault `apps/receipts/app`
(`xai-api-key`, `mcp-token`, `db-password`) through an ExternalSecret.
**See [DEPLOY.md](DEPLOY.md)** for the Vault keys, deploy, smoke test, rollback and restore steps.

## Customizing for Your Domain

To adapt this template for your specific use case:

1. **Update the Model:** Modify `internal/models/product.go` to match your domain
2. **Update Database Schema:** Modify `migrations/001_create_products.up.sql`
3. **Update Handlers:** Modify validation and business logic in `internal/handlers/`
4. **Update API Routes:** Modify endpoints in `internal/router/router.go`
5. **Update Tests:** Modify `internal/repository/product_test.go`

## Contributing

This template is designed to be a starting point. Feel free to:
- Add authentication/authorization
- Add caching layers (Redis)
- Add monitoring/metrics
- Add more sophisticated error handling
- Add rate limiting
- Add more comprehensive testing

## License

This template is provided under the MIT License. Use it freely for your projects!