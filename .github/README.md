# 🛡️ Backend-AntiGinx
Production-ready REST API for orchestration of web security scans.
Backend-AntiGinx receives scan requests, stores scan state/results in PostgreSQL, and pushes scan tasks to RabbitMQ for asynchronous worker processing.


<br>


## 🌟 About the Project
Backend-AntiGinx is the API layer of the AntiGinx platform, built for reliability and integration.

- **Queue-first workflow** — scan tasks are published to RabbitMQ queue `scan_queue`
- **Stateful scan lifecycle** — `PENDING` → `RUNNING` → `COMPLETED`
- **JWT-based authentication** — register/login/me flow for protected endpoints
- **Structured JSON API** — easy integration with workers, dashboards, and CI/CD pipelines
- **Container-ready delivery** — prebuilt image on GHCR + Docker Compose support


<br>


## 💻 Technologies
| Technology | Purpose | Details |
|---|---|---|
| 🎯 **Go 1.25** | Core language | Fast, compiled, production-oriented |
| 🌐 **Gin** | HTTP framework | Routing + middleware |
| 🔷 **GORM** | ORM | Model mapping and migrations |
| 🗄️ **PostgreSQL** | Persistence | Scan metadata and results storage |
| 🐰 **RabbitMQ** | Task queue | Async scan dispatch to workers |
| 🐳 **Docker** | Containers | Multi-stage image build |
| 🧩 **Docker Compose** | Service run mode | Simple deployment with env vars |
| 📦 **GHCR** | Image registry | Hosted backend images |
| 📚 **MkDocs** | Documentation | GitHub Pages publishing |


<br>


## 📁 Project Structure
```text
backend-antiginx/
├── internal/
│   ├── api/             # Gin router and route groups
│   ├── handlers/        # Auth and scan handlers
│   └── models/          # GORM models (Scan, ScanResult, User)
├── middleware/          # JWT auth middleware
├── docs/                # MkDocs documentation pages
├── main.go              # Application entry point
├── Dockerfile           # Multi-stage image build
├── docker-compose.yml   # Compose run config
└── mkdocs.yml           # Documentation config
```


<br>


## 🔌 API Surface
| Method | Endpoint | Description | Auth |
|---|---|---|---|
| GET | `/api/health` | Service health check | Public |
| POST | `/api/auth/register` | Register user | Public |
| POST | `/api/auth/login` | Login and get JWT | Public |
| GET | `/api/auth/me` | Current user profile | Bearer JWT |
| POST | `/api/scans` | Submit a new scan request | Public |
| GET | `/api/scans/:id` | Retrieve scan with results | Public |
| POST | `/api/results` | Submit worker result callback | Public |


<br>


## 📋 Prerequisites
| Component | Version | Purpose |
|---|---|---|
| Go | 1.25+ | Build & run locally |
| PostgreSQL | 14+ | Scan metadata and results storage |
| RabbitMQ | 3.12+ | Task queue (optional) |
| Docker | 24+ | Containerization |
| Docker Compose | 2.0+ | Orchestration |


<br>


## 📚 Documentation
Our documentation is comprehensive and organized into logical sections:

- **[Backend-AntiGinx Documentation](https://prawo-i-piesc.github.io/backend-antiginx/)** — full documentation with API reference, architecture overview, and setup guides.
- **[Quick Start](https://prawo-i-piesc.github.io/backend-antiginx/QuickStart/QuickStart/)** — step-by-step guides for local CLI, Docker, and Docker Compose setups.
    - [CLI Guide](https://prawo-i-piesc.github.io/backend-antiginx/QuickStart/CLI/) — detailed API usage examples with `curl`.
    - [Docker Guide](https://prawo-i-piesc.github.io/backend-antiginx/QuickStart/Docker/) — how to run the backend using Docker.
    - [Docker Compose Guide](https://prawo-i-piesc.github.io/backend-antiginx/QuickStart/DockerCompose/) — orchestrate backend with PostgreSQL and RabbitMQ using Compose.


<br>


## 🤝 Contributing
We welcome contributions! Please:

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/my-feature`)
3. Commit changes with clear messages
4. Push to the branch and create a Pull Request


<br>


## 📞 Support & Community
- 🐛 **Found a bug?** → [Open an Issue](https://github.com/prawo-i-piesc/backend-antiginx/issues)
- 📧 **Commercial support** → Contact the Antiginx team


<br>


## 📄 Links
- 📦 [GitHub Repository](https://github.com/prawo-i-piesc/backend-antiginx)
- 🐳 [Container Images (GHCR)](https://github.com/prawo-i-piesc/backend-antiginx/pkgs/container/backend-antiginx)
- 📚 [Full Documentation (GitHub Pages)](https://prawo-i-piesc.github.io/backend-antiginx/)
- 🚀 [GitHub Actions](https://github.com/prawo-i-piesc/backend-antiginx/actions)
- 📝 [License](https://github.com/prawo-i-piesc/backend-antiginx/blob/main/LICENSE)
- 👥 [GitHub Team](https://github.com/prawo-i-piesc)