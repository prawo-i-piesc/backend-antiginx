# 🐳 Quick Start — Docker
Run **backend-antiginx** in a container without a local Go setup.


<br>


## ✅ Requirements
- Docker 24+
- Internet access (to pull the image from GHCR)
- Reachable PostgreSQL and RabbitMQ instances
- Prepared environment values: `DATABASE_URL`, `RABBITMQ_URL`, `JWT_SECRET`


<br>


## Option A: Pre-built Image from GHCR
Pull the latest image:
```bash
docker pull ghcr.io/prawo-i-piesc/backend-antiginx:latest
```

Run Backend Container
```bash
docker run -d \
  --name antiginx-backend \
  -p 4000:4000 \
  -e DATABASE_URL="postgres://user:pass@host:5432/antiginx" \
  -e RABBITMQ_URL="amqp://user:pass@host:5672/" \
  -e JWT_SECRET="super-secret-key" \
  ghcr.io/prawo-i-piesc/backend-antiginx:latest
```


<br>


## Option B: Build Image Locally
In the project directory:
```bash
docker build -t backend-antiginx:local .
```

Run backend from local image:
```bash
docker run -d \
  --name antiginx-backend-local \
  -p 4000:4000 \
  -e DATABASE_URL="postgres://user:pass@host:5432/antiginx" \
  -e RABBITMQ_URL="amqp://user:pass@host:5672/" \
  -e JWT_SECRET="super-secret-key" \
  backend-antiginx:local
```


<br>


## ✅ Quick Validation
Check container status:
```bash
docker ps
```

Check API health:
```bash
curl http://localhost:4000/api/health
```

View logs:
```bash
docker logs -f antiginx-backend
```


<br>


## 🔍 Useful Diagnostic Commands
Check if image exists locally:
```bash
docker images | grep backend-antiginx
```

List all containers (running and stopped):
```bash
docker ps -a
```

Inspect logs for a specific container:
```bash
docker logs <container_id>
```


<br>


## ⏹️ Stop Container
Stop and remove container:
```bash
docker stop antiginx-backend && docker rm antiginx-backend
```


<br>


## 🛠️ Notes
- Backend listens on port `4000` inside the container.
- The image runs as non-root user (`appuser`).
- If port `4000` is busy, change mapping (for example: `-p 8080:4000`).


<br>


## 🔧 Troubleshooting
- **Cannot connect to PostgreSQL/RabbitMQ** → Verify host, port, credentials, and network reachability from container.
- **Auth endpoints return 500/401** → Ensure `JWT_SECRET` is set and non-empty.
- **Using `localhost` in URLs fails** → If DB/MQ are outside this container, use reachable hostnames/IPs (on macOS often `host.docker.internal`).
- **Container exits immediately** → Check startup errors with `docker logs antiginx-backend`.