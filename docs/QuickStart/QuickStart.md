# 🚀 Quick Start
Welcome to **Backend-Antiginx** — an API that queues security scan tasks in RabbitMQ and stores scan data/results in PostgreSQL.
Get up and running in minutes. This guide takes you from zero to a working API.


<br>


## 🧭 Choose Your Setup
| Scenario | Best Path | Best For |
|---|---|---|
| Quick scan from terminal | [CLI](./CLI.md) | Developers, Pentesters |
| Scan via pre-built image | [Docker](./Docker.md) | DevOps, CI/CD |
| Backend with external network/services | [Docker Compose](./DockerCompose.md) | Backend / Queue-based setups |


<br>


## ✅ Requirements
- **Go 1.25+** (if running without containers)
- **Docker 24+** (for containers) 
- **Docker Compose** (for orchestration)
- **Available services:** PostgreSQL 14+ and RabbitMQ 3.12+ (configured via environment variables)
- **Optional:** `jq` — useful for CLI JSON output parsing


<br>


## 🔐 Environment Variables
| Variable | Description | Example |
| --- | --- | --- |
| `DATABASE_URL` | PostgreSQL connection string | `postgres://user:pass@localhost:5432/antiginx` |
| `RABBITMQ_URL` | RabbitMQ connection string | `amqp://user:pass@localhost:5672/` |
| `JWT_SECRET` | Secret key for JWT signing (required for auth endpoints) | `super-secret-key` |
| `BACKEND_PORT` | Host port mapping in compose | `4000` |

**Save to `.env` in your project root:**
```env
DATABASE_URL=postgres://user:pass@localhost:5432/antiginx
RABBITMQ_URL=amqp://user:pass@localhost:5672/
JWT_SECRET=super-secret-key
BACKEND_PORT=4000
```


<br>


## 🛠️ Quick Start Locally (Go)

### Clone the repo:
```bash
git clone https://github.com/prawo-i-piesc/backend-antiginx.git
```

### Navigate to the backend directory:
```bash
cd backend-antiginx
```

### Set environment variables:
```bash
cp .env.example .env
```

### Run the application:
```bash
go run main.go
```

### Test the health endpoint:
```bash
curl http://localhost:4000/api/health
```


<br>


## 📡 API Overview
| Method | Path | Description | Requires JWT |
| --- | --- | --- | --- |
| GET | `/api/health` | Service status | No |
| POST | `/api/auth/register` | Register a user | No |
| POST | `/api/auth/login` | Get authentication token | No |
| GET | `/api/auth/me` | Get current user profile | Yes |
| POST | `/api/scans` | Submit a new scan | No |
| GET | `/api/scans/{id}` | Retrieve scan and results | No |
| POST | `/api/results` | Submit results from workers | No |

**Auth flow:**

- Use `POST /api/auth/login` to obtain a token.
- Use that token in `Authorization: Bearer <token>` for `GET /api/auth/me`.


<br>


## 🔧 Troubleshooting
- **No connection to PostgreSQL or RabbitMQ** — verify `DATABASE_URL` and `RABBITMQ_URL`; test port connectivity from host
- **401 on `GET /api/auth/me`** — ensure header `Authorization: Bearer <token>` comes from `POST /api/auth/login`
- **500 on login/token generation** — check that `JWT_SECRET` is set in environment
- **Port already in use** — change `BACKEND_PORT` in `.env` and re-map when running Docker/Compose
- **Migration errors** — drop old test tables or verify database user permissions


<br>


## 🎯 What's Next?
- Want full parameter docs and all available tests? → [CLI Guide](./CLI.md)
- Want to run via container image? → [Docker Guide](./Docker.md)
- Want a worker + queue setup? → [Docker Compose Guide](./DockerCompose.md)