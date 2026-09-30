# 💻 Quick Start — local API and curl

Run the backend from source and call its HTTP API from a terminal. The scanner itself runs in the separate [engine-antiginx](https://github.com/prawo-i-piesc/engine-antiginx) worker; there is no backend scanner CLI.

## ✅ Requirements

Go matching `go.mod` (currently **1.26.8**), a running PostgreSQL instance, RabbitMQ, `curl`, and optionally `jq`. Configure the [required environment variables](../Backend/Configuration.md) in `.env` in the repository root. There is **no `.env.example` in this repository**; create `.env` yourself. Use `MAIL_TRANSPORT=none` for local development without an email provider and `COOKIE_SECURE=false` only for HTTP localhost.

```sh
go run .
```

The server listens on `:4000`. In another terminal:

```sh
curl http://localhost:4000/api/health
```

Expected: `{"message":"Running..."}`. Startup requires working database and broker connections, even for the health check.

## 🔍 Free scan (no login)

```sh
curl -i -X POST http://localhost:4000/api/freescans \
  -H 'Content-Type: application/json' \
  -d '{"target_url":"https://example.com"}'
```

Expected: `202 Accepted` with `{"scanId":"<uuid>","status":"PENDING"}`. Use the returned UUID:

```sh
curl http://localhost:4000/api/freescans/<scanId>
```

The response includes `id`, `target_url`, `status`, `created_at`, `started_at`, `completed_at`, and `results`. The API cannot finish the scan by itself: the external engine must consume the RabbitMQ task and report results.

## 🔐 Account scan (JWT)

Register an account with a unique password of **at least 12 characters**:

```sh
curl -X POST http://localhost:4000/api/auth/register \
  -H 'Content-Type: application/json' \
  -d '{"full_name":"Example User","email":"user@example.com","password":"a-unique-long-passphrase"}'
```

Registration returns an access `token` and sets a refresh cookie. You can also log in (this example assumes MFA is not enabled):

```sh
curl -X POST http://localhost:4000/api/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"user@example.com","password":"a-unique-long-passphrase"}'
```

Copy the `token` from the JSON response into the bearer header below. If login returns `mfa_required`, finish the [MFA flow](../Backend/Auth.md) before making an authenticated request.

```sh
curl -X POST http://localhost:4000/api/scans \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer <access_token>' \
  -d '{"target_url":"https://example.com","tests":["https","hsts"],"anti_bot_detection":false}'

curl http://localhost:4000/api/scans/<scanId> \
  -H 'Authorization: Bearer <access_token>'
```

`POST /api/scans` returns `202` and a `scanId`; `GET /api/scans/{scanId}` only returns scans owned by the current user. To discover supported test IDs, call `GET /api/utils/tests` with the same bearer header. For all routes and the callback contract, see [Scans and results](../Backend/Scans.md).

## 🔧 Troubleshooting

| Symptom | Check |
| --- | --- |
| Server exits at startup | Verify required variables, PostgreSQL and RabbitMQ connectivity, and database migration permissions. |
| `401` on `/api/scans` | Supply a valid **access** JWT as `Authorization: Bearer <token>`; the refresh cookie is not sufficient. |
| `400` on premium scan | Supply `target_url` and at least one valid test ID in `tests`. |
| Scan stays `PENDING` | Ensure the engine worker consumes `scan_queue` and can reach `/api/results`. |
| Refresh cookie is not retained on local HTTP | Use `COOKIE_SECURE=false` only locally; enable `Secure` with HTTPS in production. |
