# 🛡️ Backend-AntiGinx

## About the Project

Backend-AntiGinx is the REST API server for the AntiGinx security scanning platform. Built with Go and the Gin framework, it provides endpoints for submitting security scan requests, processing results from worker services, and retrieving scan data. The service uses PostgreSQL for data persistence and RabbitMQ for asynchronous task distribution to scan workers.

## Technologies

| Technology            | Description                                               |
|-----------------------|-----------------------------------------------------------|
| 🎯 **Go 1.25**        | Main programming language                                 |
| 🌐 **Gin**            | High-performance HTTP web framework                       |
| 🗄️ **PostgreSQL**     | Relational database for scan data persistence             |
| 🐰 **RabbitMQ**       | Message broker for async task distribution                |
| 🔷 **GORM**           | ORM library for database operations                       |
| 🐳 **Docker**         | Containerization with multi-stage build                   |
| 🔄 **GitHub Actions** | CI/CD: build, tests, release, auto-labeling               |
| 📦 **GHCR**           | GitHub Container Registry for Docker images               |
| 📚 **GitHub Pages**   | Documentation hosting (MkDocs)                            |

## Project Structure

```
Backend-AntiGinx/
├── internal/
│   ├── api/             # HTTP routing configuration
│   ├── handlers/        # Request handlers (business logic)
│   └── models/          # Database models (GORM)
├── docs/                # MkDocs documentation
├── main.go              # Application entry point
├── go.mod               # Go module dependencies
├── Dockerfile           # Multi-stage Docker build
├── docker-compose.yml   # Container orchestration
└── mkdocs.yml           # Documentation configuration
```

### Core Components

- **internal/api** - HTTP routing using Gin framework with RESTful endpoint definitions
- **internal/handlers** - Request handlers implementing scan submission, result processing, and data retrieval
- **internal/models** - GORM models for `Scan` and `ScanResult` entities with UUID support

## API Endpoints

| Method | Endpoint          | Description                          |
|--------|-------------------|--------------------------------------|
| POST   | `/api/scans`      | Submit a new security scan request   |
| POST   | `/api/results`    | Submit scan results (from workers)   |
| GET    | `/api/scans/:id`  | Retrieve scan details and results    |

## Quick Start

### Prerequisites

- Go 1.25 or higher ([download here](https://go.dev/dl/))
- PostgreSQL 14+
- RabbitMQ 3.x
- Docker & Docker Compose (optional)

### Environment Variables

Create a `.env` file in the project root:

```env
# Server
BACKEND_PORT=8080

# PostgreSQL
DATABASE_URL=postgres://user:password@localhost:5432/antiginx?sslmode=disable

# RabbitMQ
RABBITMQ_URL=amqp://user:password@localhost:5672/
```

### Running Locally

```bash
# Clone the repository
git clone https://github.com/prawo-i-piesc/backend-antiginx.git
cd backend-antiginx

# Install dependencies
go mod download

# Run the server (requires PostgreSQL and RabbitMQ)
go run main.go
```

### Using Docker Compose

```bash
# Build and start all services
docker-compose up -d --build

# View logs
docker-compose logs -f backend-antiginx

# Stop services
docker-compose down
```

### Using Pre-built Docker Image

```bash
# Pull the latest image
docker pull ghcr.io/prawo-i-piesc/backend-antiginx:latest

# Run with environment variables
docker run -d \
  -p 8080:8080 \
  -e DATABASE_URL="postgres://..." \
  -e RABBITMQ_URL="amqp://..." \
  ghcr.io/prawo-i-piesc/backend-antiginx:latest
```

## API Usage Examples

### Submit a new scan

```bash
curl -X POST http://localhost:8080/api/scans \
  -H "Content-Type: application/json" \
  -d '{"target_url": "https://example.com"}'
```

Response:
```json
{
  "scanId": "01234567-89ab-cdef-0123-456789abcdef",
  "status": "PENDING"
}
```

### Get scan results

```bash
curl http://localhost:8080/api/scans/01234567-89ab-cdef-0123-456789abcdef
```

## Links

- 📦 [GitHub Repository](https://github.com/prawo-i-piesc/backend-antiginx)
- 🐳 [Container Images (GHCR)](https://github.com/prawo-i-piesc/backend-antiginx/pkgs/container/backend-antiginx)
- 📚 [Documentation (GitHub Pages)](https://prawo-i-piesc.github.io/backend-antiginx/)
- 🚀 [GitHub Actions](https://github.com/prawo-i-piesc/backend-antiginx/actions)
- 📝 [License](../LICENSE)
