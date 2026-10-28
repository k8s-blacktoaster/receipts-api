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
| GET | `/api/v1/receipts` | List all receipts |
| GET | `/api/v1/receipts/{id}` | Get a single receipt |
| POST | `/api/v1/receipts` | Create a new receipt |
| PUT | `/api/v1/receipts/{id}` | Update an existing receipt |
| DELETE | `/api/v1/receipts/{id}` | Delete a receipt |
| POST | `/api/v1/uploads` | Upload a receipt photo (multipart `image`, JPEG/PNG, max 20 MiB); 202 + queued extraction, 200 if the same image was already uploaded |
| POST | `/api/v1/receipts/{id}/extract` | Re-run extraction for a photo receipt; 409 while parsing and always 409 for `reviewed` receipts (no force option in v1) |

### Photo extraction

Uploaded photos are stored under `UPLOAD_DIR` and a receipt is created with status `uploaded`.
A background worker moves it `uploaded -> parsing -> parsed | needs_review | failed`, recording
`parse_error`, `parse_started_at` and `parsed_at`. A receipt lands in `needs_review` when a
required field is missing or the printed total does not match subtotal + tax - savings (or
subtotal + tax + adjustments) within `EXTRACTION_TOLERANCE_CENTS`.

Status changes follow a fixed transition table (`internal/models/status.go`); invalid changes are
rejected. A worker claims a receipt with one atomic `UPDATE ... WHERE status IN (...) RETURNING`,
so concurrent workers, replicas or repeated `/extract` calls never parse the same receipt twice.
`POST /receipts/{id}/extract` returns **409** while the receipt is `parsing`, and **always 409 for
a `reviewed` receipt**: v1 never overwrites human-reviewed data and has no force option.

Uploads are validated before anything is written: the image must fully decode as JPEG/PNG
(truncated files get 400) and `member_id` / `uploaded_by_id` must be existing members (400
otherwise). Provider errors are redacted (API key, `Authorization`/`Bearer` values) before they
are stored in `parse_error`, logged, or returned.

On SIGTERM the server stops accepting HTTP requests and extraction jobs, waits up to
`EXTRACTION_SHUTDOWN_TIMEOUT` seconds (default 25) for in-flight provider calls, then cancels
them. On the next start, receipts left `parsing` longer than the job timeout are reset to
`uploaded` and requeued with everything else still `uploaded`.

Rolling back migration 007 deletes receipts that were never parsed but leaves their image files
in `UPLOAD_DIR` (orphaned; remove by hand).

The extraction provider is chosen with `EXTRACTION_PROVIDER` (`xai` default, `anthropic` stub,
`none`). The xAI provider calls the Grok vision model (`EXTRACTION_MODEL`, default `grok-4.20`)
with a strict JSON schema.

**API key:** `XAI_API_KEY` is read only from the environment. Never commit it. In Kubernetes:

```yaml
# kubectl create secret generic receipts-api-xai --from-literal=XAI_API_KEY=...
env:
  - name: XAI_API_KEY
    valueFrom:
      secretKeyRef: { name: receipts-api-xai, key: XAI_API_KEY }
```

Without a key the API still accepts uploads; they stay `uploaded` until a provider is configured
(they are re-queued on startup).

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
- Auto-run on startup
- Uses `golang-migrate` library

### Configuration
- **Development:** Uses Docker Compose PostgreSQL
- **Production:** Set `DATABASE_URL` environment variable
- **Testing:** Automatic test database creation

## Development

### VS Code Integration
- **F5 Debugging:** Automatically configured for dev container
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
EXTRACTION_PROVIDER=xai   # xai | anthropic | none
EXTRACTION_MODEL=         # default grok-4.20
XAI_API_KEY=              # never commit
EXTRACTION_SHUTDOWN_TIMEOUT=25  # seconds SIGTERM waits for in-flight extractions
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

1. **Environment Variables:**
   - Set `DATABASE_URL` to your production PostgreSQL
   - Set `ENVIRONMENT=production`
   - Set `LOG_LEVEL=info` or `warn`

2. **Build and Deploy:**
   ```bash
   # Build for Linux
   CGO_ENABLED=0 GOOS=linux go build -o api cmd/api/main.go
   
   # Run migrations (if needed)
   migrate -path migrations -database $DATABASE_URL up
   
   # Start the API
   ./api
   ```

3. **Health Check:**
   ```bash
   curl http://your-api-url/api/v1/health
   ```

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