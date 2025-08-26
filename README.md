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

The template includes a complete Product CRUD API:

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/v1/health` | Health check endpoint |
| GET | `/api/v1/products` | List all products (paginated) |
| GET | `/api/v1/products/{id}` | Get a single product |
| POST | `/api/v1/products` | Create a new product |
| PUT | `/api/v1/products/{id}` | Update an existing product |
| DELETE | `/api/v1/products/{id}` | Delete a product |

### Example Product JSON:
```json
{
  "sku": "1234567",
  "name": "some product",
  "description": "a pretty cool product",
  "quantity": 1,
  "unit_price": 19.99
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
```

### Testing
```bash
# Run tests
go test ./...

# Run with coverage
go test -cover ./...

# Run specific test
go test -v ./internal/repository
```

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