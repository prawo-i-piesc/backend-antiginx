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

Keep both secrets out of source control. Back up the TOTP encryption key: changing it prevents decryption of existing TOTP enrollments. `.env` is Git-ignored; there is no `.env.example` in the repository.

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
| `BACKEND_PORT` | no default in Compose | **Compose-only** host-side port mapping (`${BACKEND_PORT}:4000`). The Go server always listens on `:4000`. |

For OAuth providers register callback URLs using `PUBLIC_BASE_URL/api/auth/oauth/google/callback` and/or `PUBLIC_BASE_URL/api/auth/oauth/github/callback` as derived by the application. Make sure this origin actually reaches the backend callback path through your reverse proxy; the setting also controls which browser origin is allowed by CORS.

## 🐰 Broker and database

At startup `main.go` migrates users, free and premium scans, scan results, sessions, recovery codes, OAuth credentials, WebAuthn credentials and email/password-reset tokens. It declares durable `main_exchange` and `retry_exchange`, a durable `scan_queue`, and a durable retry `wait_queue` (5-second TTL). Workers must be deployed independently; the backend does not launch a worker or PostgreSQL/RabbitMQ.

The provided `docker-compose.yml` runs only `backend-antiginx`, joins an external network named `vpn-net` and maps `${BACKEND_PORT}:4000`. The database and broker URLs must be reachable from that network. See [Docker Compose](../QuickStart/DockerCompose.md).

## 🔒 Deployment notes

- Use HTTPS and a reverse proxy for public deployments; `PUBLIC_BASE_URL` is the **frontend** origin, not the API host unless they are deliberately the same.
- Keep `POST /api/results` behind trusted infrastructure: it has no authentication middleware. The free-scan routes are public and accept target strings without URL or ownership checks beyond a required field.
- Only authorized security testing is appropriate. Apply network and rate restrictions in your deployment as needed; do not treat the origin check as full authentication (it permits requests without an `Origin` header).
