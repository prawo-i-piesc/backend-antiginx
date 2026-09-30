# 🧩 Quick Start — Docker Compose

The repository's `docker-compose.yml` deploys **only** the backend image and connects it to the **external** Docker network `vpn-net`. It does not create PostgreSQL, RabbitMQ, or an engine worker.

## ✅ Requirements

- Docker with Compose v2 and access to `ghcr.io/prawo-i-piesc/backend-antiginx:latest`.
- Reachable PostgreSQL and RabbitMQ services. Their hostnames in `DATABASE_URL` and `RABBITMQ_URL` must be resolvable **inside** `vpn-net`.
- An existing network named `vpn-net`, shared with the services (or routed to them). To create a new one when appropriate:

```sh
docker network create vpn-net
```

## 🔐 Configure

Create `.env` next to `docker-compose.yml`. Replace the placeholders; obtain a valid `TOTP_ENCRYPTION_KEY` using the command in [Quick Start](./QuickStart.md). There is no checked-in `.env.example`.

```dotenv
BACKEND_PORT=4000
DATABASE_URL=postgres://user:password@dbhost:5432/antiginx?sslmode=disable
RABBITMQ_URL=amqp://user:password@mqhost:5672/
JWT_SECRET=replace-with-a-long-random-secret-at-least-32-chars
PUBLIC_BASE_URL=http://localhost:3000
TOTP_ENCRYPTION_KEY=<base64-encoded-32-byte-key>
COOKIE_SECURE=false
MAIL_TRANSPORT=none
```

`PUBLIC_BASE_URL` must be the browser frontend origin. Use `COOKIE_SECURE=true` (the default), TLS and production secrets outside development. For all available variables see [Configuration](../Backend/Configuration.md). Compose maps `${BACKEND_PORT}:4000` and joins `vpn-net`; it will **not** start missing dependencies for you.

## 🚀 Start and check

```sh
docker compose up -d
docker compose ps
curl http://localhost:4000/api/health
docker compose logs backend-antiginx
```

Expect `{"message":"Running..."}` after the API connects to the broker/database and runs migrations. If you choose a different `BACKEND_PORT`, update the `curl` URL accordingly.

```sh
docker compose down
```

## 🔧 Troubleshooting

| Symptom | Check |
| --- | --- |
| `network vpn-net declared as external, but could not be found` | Create or supply `vpn-net` before `docker compose up`. |
| Backend repeatedly restarts | Inspect `docker compose logs backend-antiginx` for missing env values, DB or broker errors. |
| DB/MQ connection fails | `localhost` inside the backend container is **not** the DB/MQ container; use reachable service hostnames. |
| Scan remains `PENDING` | Deploy an engine worker on a network that can access RabbitMQ and the backend callback. |

See [Scans and results](../Backend/Scans.md) for the worker integration.
