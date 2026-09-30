# 🚀 Quick Start

Backend-Antiginx is an HTTP API, not a scanner CLI. It stores scan requests in PostgreSQL, queues work in RabbitMQ, and accepts results from an engine worker.

## 🧭 Choose your setup

| Scenario | Guide | Requirements |
| --- | --- | --- |
| Run the API from source | [Local Go setup](./CLI.md) | Go, PostgreSQL, RabbitMQ |
| Run a single container | [Docker](./Docker.md) | Docker, reachable PostgreSQL and RabbitMQ |
| Use the repository deployment file | [Docker Compose](./DockerCompose.md) | Docker Compose, external `vpn-net` and running dependencies |

## ✅ Before you start

- Install the Go version declared in [`go.mod`](https://github.com/prawo-i-piesc/backend-antiginx/blob/main/go.mod) for local development (currently Go **1.26.8**), or use Docker.
- Provide PostgreSQL and RabbitMQ; this repository's Compose file starts **only the backend**, not the database, broker or engine worker.
- Set `DATABASE_URL`, `RABBITMQ_URL`, `JWT_SECRET`, `PUBLIC_BASE_URL`, and `TOTP_ENCRYPTION_KEY`. The last value must be base64-encoded **32 random bytes**. See [Configuration](../Backend/Configuration.md) for examples and optional settings.
- Only scan targets you own or are authorized to test. A successful submission means the task was queued, not that a worker is running.

## ⚡ Minimal local run

From the repository root, create `.env` (it is ignored by Git):

```sh
python3 -c 'import base64, secrets; print(base64.b64encode(secrets.token_bytes(32)).decode())'
```

Place the generated value in `TOTP_ENCRYPTION_KEY` and supply your own database/broker credentials:

```dotenv
DATABASE_URL=postgres://user:password@localhost:5432/antiginx?sslmode=disable
RABBITMQ_URL=amqp://user:password@localhost:5672/
JWT_SECRET=replace-with-a-long-random-secret-at-least-32-chars
PUBLIC_BASE_URL=http://localhost:3000
TOTP_ENCRYPTION_KEY=<base64-encoded-32-byte-key>
COOKIE_SECURE=false
MAIL_TRANSPORT=none
```

`PUBLIC_BASE_URL` is the **frontend origin** used for CORS, not the API address. `COOKIE_SECURE=false` is only for HTTP local development; use HTTPS and the default `true` in production.

```sh
go run .
curl http://localhost:4000/api/health
```

Expected response after dependencies connect and database migrations complete:

```json
{"message":"Running..."}
```

## 📡 First scan

A free scan does not require an account:

```sh
curl -X POST http://localhost:4000/api/freescans \
  -H 'Content-Type: application/json' \
  -d '{"target_url":"https://example.com"}'
```

The API responds with `202 Accepted` and a `scanId`; query `GET /api/freescans/{scanId}` to see the status and results. The scan needs an **external engine worker** consuming `scan_queue` and reporting to `/api/results`. Account scans instead require a bearer JWT and a nonempty `tests` array; see [Scans and results](../Backend/Scans.md).

## 🎯 What's next?

- [Local API and curl workflow](./CLI.md)
- [Docker image](./Docker.md) and [Docker Compose](./DockerCompose.md)
- [Backend architecture](../Backend/README.md), [Authentication](../Backend/Auth.md), and [configuration](../Backend/Configuration.md)
