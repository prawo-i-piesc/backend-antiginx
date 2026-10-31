# 🔧 Configuration and deployment

`main.go` optionally loads `.env` from the working directory and then calls `config.Load()`. If validation, PostgreSQL connection/ping, migrations or RabbitMQ connection fails, the process exits before listening on `:4000`.

## ✅ Required environment

| Variable | Meaning |
| --- | --- |
| `DATABASE_URL` | PostgreSQL connection string for GORM, e.g. `postgres://user:password@localhost:5432/antiginx?sslmode=disable` (local example only). |
| `RABBITMQ_URL` | AMQP URL, e.g. `amqp://user:password@localhost:5672/`. |
| `JWT_SECRET` | HS256 signing secret. Less than 32 characters logs a warning but does not prevent startup; use a long random secret. |
| `PUBLIC_BASE_URL` | Frontend origin, e.g. `http://localhost:3000` or `https://example.com`. Must be bare `http(s)://host[:port]`, with no path, query or fragment. Used for CORS, origin checks and OAuth callbacks. |
| `TOTP_ENCRYPTION_KEY` | Base64 encoding of **exactly 32 random bytes**, used to encrypt TOTP secrets. |

To generate a new development key:

```sh
python3 -c 'import base64, secrets; print(base64.b64encode(secrets.token_bytes(32)).decode())'
```

Keep both secrets out of source control. Back up the TOTP encryption key: changing it prevents decryption of existing TOTP enrollments. `.env` is Git-ignored; `.env.example` lists every variable the backend reads.

## ⚙️ Optional environment

| Variable | Default | Meaning |
| --- | --- | --- |
| `COOKIE_SECURE` | `true` | `Secure` flag on session cookies. Set to `false` only on HTTP localhost; keep `true` with HTTPS in production. |
| `ACCESS_TOKEN_TTL` | `15m` | Access-token lifetime as a Go duration. |
| `REFRESH_TOKEN_TTL` | `720h` | Refresh-session lifetime as a Go duration. |
| `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET` | unset | Enable Google OAuth when **both** are set. |
| `GITHUB_CLIENT_ID`, `GITHUB_CLIENT_SECRET` | unset | Enable GitHub OAuth when **both** are set. |
| `WEBAUTHN_RPID` | hostname of `PUBLIC_BASE_URL` | Relying-party ID for passkeys. |
| `WEBAUTHN_RP_NAME` | `AntiGinx` | Display name for passkey registration. |
| `MAIL_TRANSPORT` | `resend` if `RESEND_API_KEY` exists, otherwise `none` | `resend`, `log` or `none`. `log` is for development and logs mail content; `none` does not send mail. |
| `MAIL_FROM`, `RESEND_API_KEY` | unset | Both required for `MAIL_TRANSPORT=resend`. |
| `TRUSTED_PROXIES` | unset | Comma-separated IPs or CIDRs of reverse proxies allowed to set `X-Forwarded-For`. While unset, the client IP is the connection address, so forwarded headers cannot spoof per-IP rate limits. Set it (for example `10.42.0.0/16`) once an ingress fronts the API, otherwise every request appears to come from the proxy. |
| `RABBITMQ_RESULTS_QUEUE` | `results_queue` | Queue the daemon publishes results to. Must equal the daemon's `RABBITMQ_RESULTS_QUEUE`. |
| `RESULT_CONSUMERS` | `2` | Number of result consumers, each on its own channel. |
| `RESULT_BATCH_SIZE` | `50` | Results saved per transaction. A partial batch is flushed every 50 ms; prefetch is twice the batch size. |
| `GIN_MODE` | `debug` | Read by gin itself. Use `release` on deployments to silence debug output. |
| `BACKEND_PORT` | no default in Compose | **Compose-only** host-side port mapping (`${BACKEND_PORT}:4000`). The Go server always listens on `:4000`. |

For OAuth providers register callback URLs using `PUBLIC_BASE_URL/api/auth/oauth/google/callback` and/or `PUBLIC_BASE_URL/api/auth/oauth/github/callback` as derived by the application. Make sure this origin actually reaches the backend callback path through your reverse proxy; the setting also controls which browser origin is allowed by CORS.

## 🐰 Broker and database

At startup `main.go` migrates users, free and premium scans, scan results, sessions, recovery codes, OAuth credentials, WebAuthn credentials and email/password-reset tokens. It declares `main_exchange`, a quorum `scan_queue` (`x-delivery-limit` 3, dead-lettered at-least-once through the `scan_dlx` fanout exchange into the quorum `scan_dlq`, `x-overflow: reject-publish`) and the durable results queue, then starts the result consumers. The daemon declares nothing, so these arguments are defined only here. Workers must be deployed independently; the backend does not launch a worker or PostgreSQL/RabbitMQ.

RabbitMQ cannot change the type or arguments of an existing queue. On a broker that still has the old classic `scan_queue`, delete it (and the obsolete `wait_queue` and `retry_exchange`) once before deploying, otherwise startup fails with `PRECONDITION_FAILED`.

The provided `docker-compose.yml` runs only `backend-antiginx`, joins an external network named `vpn-net` and maps `${BACKEND_PORT}:4000`. The database and broker URLs must be reachable from that network. See [Docker Compose](../QuickStart/DockerCompose.md).

## 🔒 Deployment notes

- Use HTTPS and a reverse proxy for public deployments; `PUBLIC_BASE_URL` is the **frontend** origin, not the API host unless they are deliberately the same.
- `target_url` must be an `http(s)` URL pointing at a public host. Loopback, private (RFC 1918, ULA), CGNAT, link-local and multicast addresses, single-label names and internal suffixes (`.local`, `.localhost`, `.internal`, `.lan`, `.home.arpa`, `.svc`) are rejected. This does **not** stop a public domain that resolves to a private address — apply an egress `NetworkPolicy` to the engine pods for that. Free scans are additionally limited to 10 per client IP per hour.
- The HTTP server applies read, write and idle timeouts, caps request headers at 64 KiB and bodies at 1 MiB, and shuts down gracefully on `SIGTERM`/`SIGINT` with a 10-second drain, so rolling restarts do not cut live requests.
- `GET /healthz` (liveness, no dependencies) and `GET /readyz` (pings PostgreSQL with a 2-second timeout and checks the RabbitMQ publishing channel; `503` with `{"db":"down"}` or `{"rabbitmq":"down"}` when either fails) serve container probes. They sit outside `/api`, skip CORS and are not logged. See [Kubernetes](../QuickStart/Kubernetes.md).
- The backend does not reconnect to RabbitMQ after the connection drops: `/readyz` then stays `503` and the pod receives no traffic until it is restarted.
- Only authorized security testing is appropriate. Apply network and rate restrictions in your deployment as needed; do not treat the origin check as full authentication (it permits requests without an `Origin` header).
