# 🛡️ Backend-AntiGinx
REST API for orchestration of web security scans. Backend-AntiGinx accepts scan requests, stores scan state/results in PostgreSQL, and pushes scan tasks to RabbitMQ for asynchronous engine workers.


<br>


## 🌟 About the Project
Backend-AntiGinx is the API layer of the AntiGinx platform, built for reliability and integration.

- **Queue-first workflow** — scan tasks are published to RabbitMQ queue `scan_queue`
- **Stateful scan lifecycle** — `PENDING` → `RUNNING` → `COMPLETED`
- **Account security** — access JWTs, rotating refresh sessions, MFA, OAuth and passkeys
- **Structured JSON API** — easy integration with workers, dashboards, and CI/CD pipelines
- **Container-ready delivery** — prebuilt image on GHCR + Docker Compose support


<br>


## 💻 Technologies
| Technology | Purpose | Details |
|---|---|---|
| 🎯 **Go 1.26.8** | Core language | Version specified in `go.mod` |
| 🌐 **Gin** | HTTP framework | Routing + middleware |
| 🔷 **GORM** | ORM | Model mapping and migrations |
| 🗄️ **PostgreSQL** | Persistence | Scan metadata and results storage |
| 🐰 **RabbitMQ** | Task queue | Async scan dispatch to workers |
| 🐳 **Docker** | Containers | Multi-stage image build |
| 🧩 **Docker Compose** | Service run mode | Backend container on external `vpn-net`; dependencies run separately |
| 📦 **GHCR** | Image registry | Hosted backend images |
| 📚 **MkDocs** | Documentation | GitHub Pages publishing |


<br>


## 📁 Project Structure
```text
backend-antiginx/
├── internal/
│   ├── api/             # Gin router and route groups
│   ├── auth/            # JWT, sessions, MFA, OAuth and passkeys
│   ├── config/          # Environment validation
│   ├── handlers/        # Auth, scan and admin handlers
│   └── models/          # GORM models
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
| POST | `/api/freescans` | Queue a free scan | Public |
| GET | `/api/freescans/:id` | Retrieve free scan and results | Public |
| POST | `/api/scans` | Queue an account scan with selected tests | Bearer JWT |
| GET | `/api/scans/:id` | Retrieve your account scan and results | Bearer JWT |
| GET | `/api/utils/tests` | List available test IDs | Bearer JWT |
| POST | `/api/results` | Receive engine callback (protect externally) | Public |

!!! warning "Deployment security"
    `/api/results` is unauthenticated in the current router. Restrict it to trusted workers at the network or gateway layer. Free scan details are public by ID; accept only authorized scan targets.


<br>


## 📋 Prerequisites
| Component | Version | Purpose |
|---|---|---|
| Go | Version in `go.mod` (currently 1.26.8) | Build & run locally |
| PostgreSQL | Running instance | Scan metadata and results storage |
| RabbitMQ | Running instance | Required task queue, even for API startup |
| Docker / Docker Compose | Optional | Containerized deployment |
| Engine worker | Separate deployment | Executes scans and posts results |


<br>


## 📚 Documentation
Read the [published documentation](https://prawo-i-piesc.github.io/backend-antiginx/) or go straight to:

- [Quick Start](https://prawo-i-piesc.github.io/backend-antiginx/QuickStart/QuickStart/) — choose local Go, Docker, or Docker Compose.
- [Backend architecture](https://prawo-i-piesc.github.io/backend-antiginx/Backend/) — startup and scan lifecycle.
- [Configuration](https://prawo-i-piesc.github.io/backend-antiginx/Backend/Configuration/) — required settings and deployments.
- [Scans and results API](https://prawo-i-piesc.github.io/backend-antiginx/Backend/Scans/) — worker contracts and scan endpoints.
- [Authentication](https://prawo-i-piesc.github.io/backend-antiginx/Backend/Auth/) — JWT, MFA, OAuth and passkeys.


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