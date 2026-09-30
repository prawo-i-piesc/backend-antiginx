# 🔍 Scans and results API

All paths are relative to the backend's `:4000` HTTP server. See [Authentication](./Auth.md) for bearer tokens and [Configuration](./Configuration.md) for the queue dependencies. Send JSON with `Content-Type: application/json`.

## 📡 Endpoints

| Method | Path | Access | Purpose |
| --- | --- | --- | --- |
| `GET` | `/api/health` | Public | `200` with `{"message":"Running..."}`. |
| `POST` | `/api/freescans` | Public | Start a scan with all configured test IDs. |
| `GET` | `/api/freescans/:id` | Public | Fetch a free scan with results by UUID. |
| `POST` | `/api/scans` | Bearer access JWT | Start an account scan with selected tests. |
| `GET` | `/api/scans/:id` | Bearer access JWT | Fetch the current user's account scan. |
| `GET` | `/api/users/scans` | Bearer access JWT | List the current user's premium scans with results. |
| `GET` | `/api/users/widgets` | Bearer access JWT | Counts (`total_scans`, `detected_threats`, `safe_sites`) and up to four `recent_scans`. |
| `GET` | `/api/utils/tests` | Bearer access JWT | Grouped test IDs available to premium scans. |
| `POST` | `/api/results` | **Public** (intended for worker) | Store an engine result, execution message, or scan completion. |

`/api/admin/health`, `/api/admin/database?table=users|scans|premium_scans` and `/api/admin/widgets` require an **admin** JWT; see [Authentication](./Auth.md).

## ⚡ Submit a free scan

```http
POST /api/freescans
Content-Type: application/json

{"target_url":"https://example.com"}
```

The request requires a nonempty `target_url`. The backend persists a free scan as `PENDING` and publishes a task for **all** IDs in its available tests list. Successful response: `202 Accepted`.

```json
{"scanId":"<uuid>","status":"PENDING"}
```

Poll `GET /api/freescans/<uuid>`; this route is public. It returns `id`, `target_url`, `status`, `created_at`, `started_at`, `completed_at`, and `results`. Timestamps are null until set. `results` entries contain `id`, `scan_id`, `test_name`, `severity`, `passed`, `message` and `metadata`.

## 🔐 Submit an account scan

```http
POST /api/scans
Authorization: Bearer <access_token>
Content-Type: application/json

{"target_url":"https://example.com","tests":["https","hsts"],"anti_bot_detection":true}
```

`target_url` and a nonempty `tests` array are required. Optional `anti_bot_detection` appends the `--antiBotDetection` engine parameter. The optional `authorized_tester` flag is accepted by the request struct but **is not used** by this handler; it does not grant access or confirm target authorization. Unknown test IDs are silently discarded if at least one valid ID remains; if none remain, the API returns `400` (`No valid tests provided`). Success returns the same `202` `scanId`/`status` shape as a free scan.

Supported test IDs (also returned, grouped, by `GET /api/utils/tests`):

| Category | IDs |
| --- | --- |
| SSL/TLS & Encryption | `https`, `hsts`, `ssl-cert` |
| Security Headers | `csp`, `xframe`, `permissions-policy`, `x-content-type-options`, `referrer-policy`, `cross-origin-x` |
| Privacy & Session Management | `cookie-sec` |
| Reconnaissance & Server Information | `serv-h-a`, `sitemap` |
| Vulnerabilities & Code Analysis | `js-obf`, `phishing-url` |

Poll `GET /api/scans/<uuid>` with the **same user's** bearer token. A different user receives `404`. `GET /api/users/scans` returns that user's scan list, while `GET /api/users/widgets` returns dashboard aggregates.

## 🐰 Worker task

The backend publishes persistent JSON to `scan_queue`. Free scans route through `main_exchange` using `scan_key`; premium scans publish through the default exchange to `scan_queue`. Example task:

```json
{
  "Target": "https://example.com",
  "Parameters": [
    {"Name": "--tests", "Arguments": ["https", "hsts"]},
    {"Name": "--taskId", "Arguments": ["<scanId>"]}
  ]
}
```

Premium tasks can additionally contain `{"Name":"--antiBotDetection","Arguments":[]}`. No engine is started by this repository. Deploy [engine-antiginx](https://github.com/prawo-i-piesc/engine-antiginx) separately with access to RabbitMQ and a backend callback URL.

## 🔄 Result callback contract

`POST /api/results` binds `AsyncResultRequest`, **not** the separate `ResultSubmissionRequest` type declared in the same Go file. `testId` is the scan UUID received as `scanId`; `result` uses the engine's capitalized property names:

```json
{
  "target": "https://example.com",
  "testId": "<scanId>",
  "result": {
    "Name": "https",
    "Certainty": 90,
    "ThreatLevel": "Info",
    "Metadata": {"source": "worker"},
    "Description": "HTTPS is enabled"
  },
  "endFlag": false,
  "resultType": 1,
  "message": {"Message": "", "Code": 0}
}
```

A named result is saved and changes `PENDING` to `RUNNING`. `ThreatLevel` values `None` or `Info` produce `passed: true`; others produce `false`. A callback with `result.Name` empty normally marks the scan `COMPLETED`, regardless of `endFlag`. If `endFlag` is false and `resultType` is `0` (`Message`), it instead records an execution/crash message and moves the scan to `RUNNING`. `resultType` `1` represents `Success`. Do not assume the callback validates worker identity or target ownership: **there is no auth middleware on `/api/results`**.

## ⚠️ Errors and limitations

- Malformed scan UUIDs produce `400`; missing scans produce `404`; malformed request JSON or absent required scan fields produce `400`.
- The scan handlers only require a nonempty target string; they do **not** verify target ownership or URL format. Restrict submissions to authorized testing in production.
- Queue publishing failures return `500` after the scan record was created; status may remain `PENDING`. `202` means accepted/queued, **not** completed.
- Free scan data is publicly retrievable by its UUID. Keep sensitive targets out of public scans.
