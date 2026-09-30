# 🐳 Quick Start — Docker

Run the API from the published container image or build it locally. **The image contains only the backend**; PostgreSQL, RabbitMQ, and an engine worker must be provided separately.

## ✅ Requirements

Docker, database and broker endpoints reachable **from inside the container**, and the [required configuration](../Backend/Configuration.md). Docker `localhost` points to the container itself; on macOS a service on your host may be reachable as `host.docker.internal`. For containers on a shared network, use container DNS names instead.

## 📦 Pull or build

```sh
docker pull ghcr.io/prawo-i-piesc/backend-antiginx:latest
```

Or build from the repository root:

```sh
docker build -t backend-antiginx:local .
```

In the following example use `backend-antiginx:local` instead of the GHCR tag if you built locally. Create `.env` containing `DATABASE_URL`, `RABBITMQ_URL`, `JWT_SECRET`, `PUBLIC_BASE_URL`, and `TOTP_ENCRYPTION_KEY` (see [Configuration](../Backend/Configuration.md)). Never commit `.env` or put real secrets in a shell history command.

## 🚀 Run

```sh
docker run -d --name backend-antiginx \
  --env-file .env \
  -p 4000:4000 \
  ghcr.io/prawo-i-piesc/backend-antiginx:latest

curl http://localhost:4000/api/health
```

Expected: `{"message":"Running..."}`. The application listens on port `4000` inside the container. If your DB/MQ is on a Docker network, add `--network <network-name>` and set URLs to the appropriate DNS names; when using HTTP localhost and refresh cookies, set `COOKIE_SECURE=false` **only for development**.

```sh
docker logs backend-antiginx
docker stop backend-antiginx
docker rm backend-antiginx
```

## 🔧 Troubleshooting

- **Container stops during startup:** inspect `docker logs backend-antiginx`; configuration, PostgreSQL connection, database migrations, and RabbitMQ connection are required before the API starts.
- **Connection refused to DB/MQ:** check credentials, networking, and hostnames from the container's point of view. `localhost` usually is not the dependency.
- **No scan results:** an external [engine worker](https://github.com/prawo-i-piesc/engine-antiginx) must consume the queue and call the backend with results.

Next: [Docker Compose](./DockerCompose.md) or [API usage](./CLI.md).
