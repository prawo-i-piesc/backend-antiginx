# 🔐 Authentication and Account API

Backend-Antiginx serves authentication routes under `/api/auth`. This reference follows `internal/api/router.go` and the corresponding Go handlers. JSON examples use placeholder values; they are not live credentials.

## 🧭 Access rules

| Route group | Requirement | Meaning |
| --- | --- | --- |
| Public | None | Registration, password login, and OAuth start/callback. |
| Origin-restricted | `RequireOrigin(PUBLIC_BASE_URL)` | If an `Origin` header is present, it must match the configured frontend origin; a missing `Origin` is allowed. This is **not** authentication. |
| JWT-protected | `Authorization: Bearer <access_token>` | `RequireAuth` validates an **access** JWT. The refresh cookie alone cannot access these routes. |
| Origin + JWT | Both checks above | Used for session changes, MFA setup, OAuth account linking, WebAuthn credential management, and verification-email requests. |
| Admin | JWT + `RequireAdmin` | `/api/admin/*` checks the user's **current database role** is `admin`; a regular authenticated user is not an admin. No origin middleware is attached to this group. |

CORS allows credentials only from `PUBLIC_BASE_URL`. Set this to the frontend origin (for example, `http://localhost:3000`), not the API address. Browser clients using cookie-backed routes must include credentials. An absent `Origin` passes `RequireOrigin`, so this check is not a substitute for a valid JWT or other authorization.

## 🔑 Registration, login, and sessions

| Method | Route | Access | Request / behavior |
| --- | --- | --- | --- |
| `POST` | `/api/auth/register` | Public | JSON `full_name`, `email`, `password`; creates a `user`, sends a verification email through the configured mailer, then issues a session (`201`). |
| `POST` | `/api/auth/login` | Public | JSON `email`, `password`; returns a session (`200`) or an MFA challenge (`200`) when a second factor is required. |
| `POST` | `/api/auth/refresh` | Origin-restricted | Reads the `ag_session` refresh cookie, rotates it, and returns a new access token (`200`). No bearer token required. |
| `POST` | `/api/auth/logout` | Origin + JWT | Optional JSON `{"all_devices":true}` revokes all refresh sessions; otherwise revokes the session identified by the cookie, if supplied. Clears the cookie (`204`). |
| `DELETE` | `/api/auth/account` | Origin + JWT | Deletes the account (`204`); JSON may contain `password`, `method`, `code`. Password is checked if the account has one; TOTP-enabled accounts also need a TOTP or recovery code. May first return `mfa_required`. |
| `GET` | `/api/auth/me` | JWT | Returns profile and auth state (`id`, `full_name`, `email`, `role`, `created_at`, plus `auth.password_set`, `email_verified`, `providers`, `passkey_mode`, and MFA status). |

Passwords must contain at least **12 Unicode characters** and must not appear on the built-in common-password list. Emails are normalized before lookup/storage; passwords are stored as bcrypt hashes. Registration does **not** wait for email verification before issuing a session.

Successful password login (without a required second factor) and registration return JSON with `token` and `access_token` (the same access JWT), `token_type: "Bearer"`, `expires_in` (seconds), and `user` (`id`, `email`, `full_name`, `role`). Refresh and completed MFA/WebAuthn login return the same shape. The access JWT is HS256-signed using `JWT_SECRET`; its default lifetime is **15 minutes** (`ACCESS_TOKEN_TTL`). Use it in the `Authorization` header, not as a refresh cookie:

```bash
curl -X POST http://localhost:4000/api/auth/register \
  -H 'Content-Type: application/json' \
  -d '{"full_name":"Example User","email":"user@example.com","password":"a-unique-long-passphrase"}'

curl -X POST http://localhost:4000/api/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"user@example.com","password":"a-unique-long-passphrase"}'

curl http://localhost:4000/api/auth/me \
  -H 'Authorization: Bearer <access_token>'
```

The refresh credential is an **opaque server-side session token**, not a JWT. It is delivered in the `ag_session` cookie with `HttpOnly`, `SameSite=Lax`, path `/`, and `Secure` controlled by `COOKIE_SECURE` (default `true`). Its default lifetime is **720 hours / 30 days** (`REFRESH_TOKEN_TTL`). Refresh rotates the cookie and detects reuse; detected reuse revokes sessions. For a local plain-HTTP example, set `COOKIE_SECURE=false` **only in development**, then use a cookie jar:

```bash
curl -c cookies.txt -X POST http://localhost:4000/api/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"user@example.com","password":"a-unique-long-passphrase"}'

curl -b cookies.txt -c cookies.txt -X POST http://localhost:4000/api/auth/refresh
```

These cookie-jar commands assume login succeeded without MFA. Refresh and logout have different requirements: refresh needs the cookie (and passes the origin check), whereas logout requires a bearer JWT, even if the cookie is present. Clearing a cookie or revoking refresh sessions does not itself invalidate an already issued access JWT before expiry.

## 🛡️ MFA / TOTP

| Method | Route | Access | Request / result |
| --- | --- | --- | --- |
| `POST` | `/api/auth/mfa/totp/enroll` | Origin + JWT | JSON `{"password":"<current password>"}`; returns `secret` and `otpauth_uri`. Requires a password-bearing account and password confirmation. |
| `POST` | `/api/auth/mfa/totp/activate` | Origin + JWT | JSON `{"code":"<TOTP code>"}`; activates pending enrollment and returns `recovery_codes`, `generated_at`. |
| `DELETE` | `/api/auth/mfa/totp` | Origin + JWT | JSON `{"password":"<current password>"}`; disables TOTP and removes recovery codes (`204`). |
| `POST` | `/api/auth/mfa/recovery-codes/regenerate` | Origin + JWT | JSON `{"password":"<current password>"}`; replaces recovery codes and returns the new set. Requires active TOTP. |
| `POST` | `/api/auth/mfa/verify` | Origin-restricted | JSON `mfa_token`, `method` (`totp` or `recovery_code`), `code`; completes the challenge and issues a session. |
| `POST` | `/api/auth/mfa/webauthn/options` | Origin-restricted | JSON `{"mfa_token":"<challenge token>"}`; returns `publicKey` and `webauthn_session` for passkey-as-second-factor. |
| `POST` | `/api/auth/mfa/webauthn/verify` | Origin-restricted | JSON `mfa_token`, `webauthn_session`, `credential`; verifies the WebAuthn assertion and issues a session. |

When password login requires MFA, it returns `{"mfa_required":true,"mfa_token":"...","methods":[...],"expires_in":300}` instead of access credentials. The methods list may include `totp`, `recovery_code`, and/or `webauthn`. The MFA token is a **short-lived challenge JWT** (five minutes), not a bearer access JWT. For a code-based challenge:

```bash
curl -X POST http://localhost:4000/api/auth/mfa/verify \
  -H 'Content-Type: application/json' \
  -d '{"mfa_token":"<mfa_token>","method":"totp","code":"<current code>"}'
```

Recovery codes are one-use; store the codes returned on activation/regeneration securely. The WebAuthn MFA pair uses a passkey **after** password login, not the passwordless login routes below. OAuth login can also redirect to an MFA challenge when TOTP is enabled.

## 🌐 OAuth providers and account linking

Configured providers are `google` and/or `github` (each requires its client ID and secret). The router accepts `:provider`, but unconfigured/unknown providers cannot complete a flow.

| Method | Route | Access | Behavior |
| --- | --- | --- | --- |
| `GET` | `/api/auth/oauth/:provider/start` | Public | Starts provider authorization; optional `next` query; sets an OAuth state cookie and redirects (`302`). |
| `GET` | `/api/auth/oauth/:provider/callback` | Public | Provider callback using `code` and `state`; validates state, exchanges code, and redirects to the frontend. Successful login sets the session cookie; TOTP may instead redirect to `/login/mfa`. |
| `POST` | `/api/auth/oauth/:provider/link` | Origin + JWT | Begins linking to the signed-in account; optional `next` query; returns `redirect_url` to visit. |
| `DELETE` | `/api/auth/oauth/:provider` | Origin + JWT | Unlinks that provider (`204`), unless it is the last login method. |
| `GET` | `/api/auth/oauth-link/pending` | Origin-restricted | Reads pending link from the OAuth state cookie; returns provider, email, `password_set`, `totp_required`, `expires_in`. |
| `POST` | `/api/auth/oauth-link/confirm` | Origin-restricted | JSON `password`, optional `method` and `code` for TOTP-enabled accounts; confirms a pending link and issues a session, or responds with `mfa_required`. |

For example, navigate the browser to `/api/auth/oauth/google/start` when Google is configured. OAuth requires a **provider-verified email**. An existing unverified password account with the same email needs explicit password confirmation via the pending-link flow; do not assume a callback automatically links it. OAuth callback is a redirect flow, **not** a JSON access-token response. A new OAuth account has no password by default. The provider redirect URI is `PUBLIC_BASE_URL/api/auth/oauth/:provider/callback`.

## 🔐 WebAuthn / passkeys

| Method | Route | Access | Request / result |
| --- | --- | --- | --- |
| `POST` | `/api/auth/webauthn/register/options` | Origin + JWT | Starts credential registration; returns WebAuthn creation options. |
| `POST` | `/api/auth/webauthn/register/verify` | Origin + JWT | JSON `credential` (browser WebAuthn result), optional `name`; stores the credential (`201`). |
| `POST` | `/api/auth/webauthn/login/options` | Origin-restricted | Optional JSON `email` (or empty body); returns `publicKey` assertion options and `webauthn_session`. |
| `POST` | `/api/auth/webauthn/login/verify` | Origin-restricted | JSON `webauthn_session`, `credential`; issues a session **only** for accounts in `passwordless` passkey mode. |
| `GET` | `/api/auth/webauthn/credentials` | Origin + JWT | Lists credential `id`, `name`, `created_at`, `last_used_at`. |
| `DELETE` | `/api/auth/webauthn/credentials/:id` | Origin + JWT | Removes a credential (`204`), unless it is the last login method. |
| `PUT` | `/api/auth/webauthn/mode` | Origin + JWT | JSON `{"mode":"second_factor"}` or `{"mode":"passwordless"}`; returns `passkey_mode`. Passwordless requires an existing passkey. |

The browser must complete each options/verify ceremony with WebAuthn APIs; the `credential` is the browser-produced JSON object, not a password or code. In `second_factor` mode a passkey completes the `/mfa/webauthn/*` challenge after password login. The standalone `/webauthn/login/*` flow is for `passwordless` mode only. Do not remove the last remaining password/provider/passkey login method.

## ✉️ Email and profile

| Method | Route | Access | Request / behavior |
| --- | --- | --- | --- |
| `POST` | `/api/auth/email/verify/request` | Origin + JWT | Requests another verification email for the current unverified account (`204`); rate-limited. |
| `POST` | `/api/auth/email/verify` | Origin-restricted | JSON `{"token":"<email verification token>"}`; verifies the address (`204`). |
| `POST` | `/api/auth/password/forgot` | Origin-restricted | JSON `{"email":"user@example.com"}`; replies `202` with the same generic message even if the account is absent. Rate-limited. |
| `POST` | `/api/auth/password/reset` | Origin-restricted | JSON `token`, `new_password`; updates the password, revokes refresh sessions, clears the cookie. May return new recovery codes if TOTP is enabled. |
| `PATCH` | `/api/utils/profile/name` | JWT | JSON `{"full_name":"Example User"}`; minimum six characters. |
| `PATCH` | `/api/utils/profile/email` | JWT | JSON `{"email":"new@example.com"}`; changing it clears `email_verified` and sends verification mail. |
| `PATCH` | `/api/utils/profile/password` | JWT | JSON `old_password`, `new_password`; checks the old password and the new password policy. |

Email verification links target the frontend `/verify-email?token=...` and expire after **24 hours**; password reset links target `/reset-password?token=...` and expire after **30 minutes**. Tokens are single-use and stored hashed. Email delivery depends on `MAIL_TRANSPORT` (`resend`, `log`, or `none`; default is `none` without a Resend key), so a successful API response does not by itself guarantee delivery. Password reset revokes refresh sessions; do not assume already-issued access JWTs are revoked immediately.

## 👑 Admin is separate

| Method | Route | Access | Purpose |
| --- | --- | --- | --- |
| `GET` | `/api/admin/health` | Admin JWT | Admin health check. |
| `GET` | `/api/admin/database` | Admin JWT | Database information. |
| `GET` | `/api/admin/widgets` | Admin JWT | Admin dashboard widgets. |

These routes require an access JWT **and** `RequireAdmin`. Registration assigns the ordinary `user` role; these endpoints do not grant admin privileges. The middleware checks the role from the database, rather than trusting only the role claim in the JWT.

## ⚠️ Security and errors

- Use HTTPS in production and keep `COOKIE_SECURE=true`. Keep `JWT_SECRET` and `TOTP_ENCRYPTION_KEY` private; `TOTP_ENCRYPTION_KEY` must decode to 32 bytes. Never expose access tokens, session cookies, TOTP secrets, recovery codes, or email tokens in logs or client-side storage unnecessarily.
- Cookie-based refresh is origin-restricted when `Origin` is supplied, but requests without `Origin` are accepted. Do not describe `RequireOrigin` as a blanket CSRF defense; `SameSite=Lax` and appropriate deployment controls matter too.
- API failures are JSON with `error` and `code` (and sometimes `fields`). For example, invalid login returns `INVALID_CREDENTIALS`; missing/invalid access credentials return `SESSION_EXPIRED`; invalid MFA codes return `MFA_INVALID_CODE`; a forbidden admin or mismatched origin returns `FORBIDDEN`. OAuth callback failures generally **redirect** with an `error` query parameter instead of returning JSON.
