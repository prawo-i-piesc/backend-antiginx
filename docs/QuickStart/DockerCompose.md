# 🧩 Quick Start — Docker Compose
This variant runs **backend-antiginx** as a container service connected to PostgreSQL and RabbitMQ.


<br>


## ✅ Requirements
- Docker + Docker Compose (v2)
- Running PostgreSQL and RabbitMQ instances
- Existing Docker network `antiginx` (defined as `external`)

**Create the network (one-time setup) if you don't have it:**
```bash
docker network create antiginx
```


<br>


## 1️⃣ Prepare `.env` File
Create a `.env` file in the project root directory:
```dotenv
DATABASE_URL=postgres://user:pass@dbhost:5432/antiginx
RABBITMQ_URL=amqp://user:pass@mqhost:5672/
JWT_SECRET=super-secret-key
BACKEND_PORT=4000
```

**📋 About Environment Variables:**

- `DATABASE_URL` connects backend to PostgreSQL.
- `RABBITMQ_URL` connects backend to RabbitMQ and publishes tasks to `scan_queue`.
- `JWT_SECRET` is required for JWT token signing (`/api/auth/login`, `/api/auth/me`).
- `BACKEND_PORT` is used for Compose mapping (`${BACKEND_PORT}:4000`).


<br>


## 2️⃣ Create `docker-compose.yml`
Create or update the `docker-compose.yml` file in your project root:
```yaml
services:
  backend-antiginx:
    image: ghcr.io/prawo-i-piesc/backend-antiginx:latest
    container_name: backend
    restart: unless-stopped

    ports:
      - "${BACKEND_PORT}:4000"

    environment:
      - DATABASE_URL=${DATABASE_URL}
      - RABBITMQ_URL=${RABBITMQ_URL}
      - JWT_SECRET=${JWT_SECRET}

    mem_limit: 2048m

    networks:
      - antiginx

networks:
  antiginx:
    external: true
```

**💡 Customization:**

- Change `ghcr.io/prawo-i-piesc/backend-antiginx:latest` to your own image/tag if needed.
- Adjust `mem_limit` based on available server resources.
- If you do not use an external network, replace `external: true` with an internal network setup.


<br>


## 3️⃣ Start Services
Start the container in detached mode:
```bash
docker compose up -d
```


<br>


## ✅ Quick Validation
Check status:
```bash
docker compose ps
```

Verify health endpoint:
```bash
curl http://localhost:${BACKEND_PORT:-4000}/api/health
```

View logs:
```bash
docker compose logs -f backend-antiginx
```


<br>


## 4️⃣ Stop Services
Stop and remove containers:
```bash
docker compose down
```


<br>


## 🔄 How It Works
- Backend starts on port `4000` inside the container.
- On `POST /api/scans`, a scan record is created in PostgreSQL with `PENDING` status.
- Backend publishes a task message to RabbitMQ queue `scan_queue`.
- Workers send results to `POST /api/results`; backend stores them and updates scan status (`RUNNING`/`COMPLETED`).


<br>


## 🔧 Troubleshooting
- **Error: `network antiginx declared as external, but could not be found`** → Create the network: `docker network create antiginx`.
- **Auth endpoints return 500/401** → Verify `JWT_SECRET` is set and non-empty in `.env`.
- **No DB/RabbitMQ connection** → Check `DATABASE_URL` and `RABBITMQ_URL`, then restart with `docker compose up -d`.
- **`localhost` in DB/MQ URLs does not work** → If services run in other containers/hosts, use reachable hostnames (e.g., service name, container DNS, or external host IP).
- **Container keeps restarting** → Check logs with `docker compose logs -f backend-antiginx` and validate external service reachability.