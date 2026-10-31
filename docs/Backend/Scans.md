# 🔍 Scans and results API

All paths are relative to the backend's `:4000` HTTP server. See [Authentication](./Auth.md) for bearer tokens and [Configuration](./Configuration.md) for the queue dependencies. Send JSON with `Content-Type: application/json`.

## 📡 Endpoints

| Method | Path | Access | Purpose |
| --- | --- | --- | --- |
| `GET` | `/healthz` | Public | Liveness probe, `200` with `{"status":"ok"}`. No dependencies are checked. |
| `GET` | `/readyz` | Public | Readiness probe, `200` with `{"db":"up","rabbitmq":"up"}` or `503` when a dependency is down. |
| `GET` | `/api/health` | Public | Kept for compatibility: `200` with `{"message":"Running..."}`, no dependencies checked. |
| `POST` | `/api/freescans` | Public | Start a scan with all configured test IDs. |
| `GET` | `/api/freescans/:id` | Public | Fetch a free scan with results by UUID. |
| `GET` | `/api/freescans/:id/report` | Public | Download a free scan report (PDF, HTML, Markdown or SARIF). |
| `POST` | `/api/scans` | Bearer access JWT | Start an account scan with selected tests. |
| `GET` | `/api/scans/:id` | Bearer access JWT | Fetch the current user's account scan. |
| `GET` | `/api/scans/:id/report` | Bearer access JWT | Download a report of the current user's account scan. |
| `GET` | `/api/users/scans` | Bearer access JWT | List the current user's premium scans with results. |
| `GET` | `/api/users/widgets` | Bearer access JWT | Counts (`total_scans`, `detected_threats`, `safe_sites`) and up to four `recent_scans`. |
| `GET` | `/api/utils/tests` | Bearer access JWT | Grouped test IDs available to premium scans. |

Admin routes (`/api/admin/*`) require an **admin** JWT; see [Authentication](./Auth.md#admin-operations).

## ⚡ Submit a free scan

```http
POST /api/freescans
Content-Type: application/json

{"target_url":"https://example.com"}
```

The request requires a `target_url` pointing at a public `http(s)` host (see [Errors and limitations](#errors-and-limitations)); each client IP may start 10 free scans per hour, after which the API returns `429 RATE_LIMITED` with `Retry-After`. The backend persists a free scan as `PENDING` and publishes a task for **all** IDs in its available tests list. Successful response: `202 Accepted`.

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

`target_url` (same rules as free scans, no rate limit) and a nonempty `tests` array are required. Optional `anti_bot_detection` appends the `--antiBotDetection` engine parameter. Unknown test IDs are silently discarded if at least one valid ID remains; if none remain, the API returns `400 NO_VALID_TESTS`. Success returns the same `202` `scanId`/`status` shape as a free scan.

Supported test IDs (also returned, grouped, by `GET /api/utils/tests`):

| Category | IDs |
| --- | --- |
| SSL/TLS & Encryption | `https`, `hsts`, `ssl-cert` |
| Security Headers | `csp`, `xframe`, `permissions-policy`, `x-content-type-options`, `referrer-policy`, `cross-origin-x` |
| Privacy & Session Management | `cookie-sec` |
| Reconnaissance & Server Information | `serv-h-a`, `sitemap`, `bot-protection` |
| Vulnerabilities & Code Analysis | `js-obf`, `phishing-url`, `dns-reputation`, `favicon-origin`, `credential-form` |

Poll `GET /api/scans/<uuid>` with the **same user's** bearer token. A different user receives `404`. `GET /api/users/scans` returns that user's scan list, while `GET /api/users/widgets` returns dashboard aggregates.

## 📄 Scan reports

```http
GET /api/scans/<uuid>/report?format=pdf&lang=pl
Authorization: Bearer <access_token>
```

A report can be generated for any finished scan, whether it was just completed or is taken from the scan history (`GET /api/users/scans`). It is built on request from the stored results; nothing is cached or saved. `GET /api/freescans/<uuid>/report` serves free scans and, like the free scan itself, is public by UUID. The account route only serves the scan's owner: another user's scan, or a free scan's UUID, returns `404 SCAN_NOT_FOUND`.

`format` selects the output; it defaults to `pdf`. `lang` selects the language of the report's labels: `en` (default) or `pl`; a region suffix such as `pl-PL` is accepted, so the frontend can pass its interface language as is. Test names, messages and metadata come from the engine and stay in English in both languages; the Polish report says so above the findings, and the HTML report marks that text `lang="en"` for screen readers. SARIF is meant for tools and is identical in both languages.

| `format` | `Content-Type` | Contents |
| --- | --- | --- |
| `pdf` | `application/pdf` | A4 document for printing and sharing. |
| `html` | `text/html; charset=utf-8` | Self-contained page: inline styles, no scripts, no external resources. |
| `md` (or `markdown`) | `text/markdown; charset=utf-8` | CommonMark with GFM tables, for tickets and repositories. |
| `sarif` | `application/sarif+json` | [SARIF 2.1.0](https://docs.oasis-open.org/sarif/sarif/v2.1.0/sarif-v2.1.0.html) log for security tooling. |

The response is a download: `Content-Disposition: attachment; filename=antiginx-report-<uuid>.<ext>` (exposed to the frontend through CORS) and `Cache-Control: no-store`. Because the account route needs the bearer token, the frontend fetches the file and saves the resulting blob; a plain link cannot carry the header.

Every format renders the same document:

- **Scan**: target, scan ID, type (free or account), status, submission, start and completion times, duration.
- **Summary**: checks run, passed and failed, failed checks per severity (`Critical`, `High`, `Medium`, `Low`) and an overall assessment equal to the most severe failed check.
- **Findings**: failed checks first, most severe first, then passed checks. Each finding has the test name, a rule ID derived from it (`HSTS Header Analysis` → `hsts-header-analysis`), severity, result, message and the formatted `metadata`. The PDF, HTML and Markdown reports cut metadata at 8 KiB per finding; SARIF carries it in full. Results stored twice by an at-least-once redelivery are counted once.

Scan text such as messages and metadata comes from the scanned site, so the HTML report escapes it and the Markdown report backslash-escapes link, image, HTML and block syntax, keeping a hostile page from injecting content into the report.

### SARIF mapping

The SARIF log has one run whose `tool.driver` is `AntiGinx`. Each test becomes a rule and each finding a result:

| Finding | `kind` | `level` | rule `security-severity` |
| --- | --- | --- | --- |
| Failed `Critical` | `fail` | `error` | `9.5` |
| Failed `High` | `fail` | `error` | `8.0` |
| Failed `Medium` | `fail` | `warning` | `5.5` |
| Failed `Low` / `Info` / `None` | `fail` | `note` | `2.0` / none / none |
| Passed | `pass` | `none` | — |

A rule's `defaultConfiguration.level` and `security-severity` come from its most severe failed result. Each result's location is the scanned URL (`physicalLocation.artifactLocation.uri`), and `partialFingerprints["antiginx/v1"]` is a hash of the target and rule ID, so tools can match the same issue across scans. `automationDetails.id` is `antiginx/<free|premium>/<scanId>`; `guid` is left out because SARIF only allows UUID versions 1–5 there and scans use UUIDv7. `invocations[0].executionSuccessful` is true only for `COMPLETED` scans. Result `properties` hold `severity`, `passed`, `testName` and the original `metadata`; run `properties` hold the scan details and summary.

## 🐰 Worker task

The backend publishes persistent JSON through `main_exchange` with routing key `scan_key`, which delivers it to `scan_queue`, and waits for the broker's publisher confirm. `scan_queue` is a quorum queue: after three deliveries the daemon could not finish, the task is dead-lettered through `scan_dlx` into `scan_dlq`. When the broker nacks the task (for example a quorum without majority, or a length limit hit under `x-overflow: reject-publish`), is unreachable or the confirm does not arrive within 5 seconds, the scan is marked `FAILED` and the API returns `503 QUEUE_UNAVAILABLE`. Example task, in the shape of the daemon's `ScanTask`:

```json
{
  "Target": "https://example.com",
  "Parameters": [
    {"Name": "--tests", "Arguments": ["https", "hsts"]}
  ],
  "TaskId": "<scanId>"
}
```

Premium tasks can additionally contain `{"Name":"--antiBotDetection","Arguments":[]}`. No engine is started by this repository. Deploy [daemon-antiginx](https://github.com/prawo-i-piesc/daemon-antiginx) separately with access to RabbitMQ.

## 🔄 Results queue contract

The daemon publishes every engine result to the results queue (`RABBITMQ_RESULTS_QUEUE`, default `results_queue`) through the default exchange. Each message is the daemon's `ScanContext`; `testId` is the scan UUID received as `TaskId`:

```json
{
  "target": "https://example.com",
  "testId": "<scanId>",
  "endFlag": false,
  "result": {
    "testResult": {
      "Name": "https",
      "Certainty": 100,
      "ThreatLevel": "Info",
      "Metadata": {"source": "worker"},
      "Description": "HTTPS is enabled"
    },
    "scanInfo": {"Message": "", "Code": 0},
    "resultType": 1
  }
}
```

`RESULT_CONSUMERS` consumers, each on its own channel, collect messages into batches of `RESULT_BATCH_SIZE` (or whatever arrived within 50 ms) and save each batch in one transaction, acknowledging it with a single multiple ack. A failed save puts the whole batch back on the queue after a backoff of 1 to 30 seconds. A message that is not JSON or has a `testId` that is not a UUID is rejected without requeueing.

Within a batch:

- `endFlag: true` marks the scan `COMPLETED`. The engine's end message carries `resultType` `0` and an empty result, so `endFlag` is checked first and that message never becomes a result row.
- `resultType` `1` (`Success`) saves a result row. `ThreatLevel` values `None` or `Info` produce `passed: true`; others produce `false`. Missing `Metadata` is stored as `{}`.
- `resultType` `0` (`Message`) records the engine's execution message (a test interrupted or blocked) as an `Info` row named after the test, or `Engine/Execution Error` when the name is empty.
- The first saved message moves the scan `PENDING` → `RUNNING`. A `FAILED` scan is never completed. Messages for a scan that no longer exists are logged and dropped.

Delivery is at-least-once: a result saved right before the connection dropped can arrive again and be stored twice. Messages are spread over several consumers, so the end message can be committed a few milliseconds before the last results of the same scan.

## ⚠️ Errors and limitations

- Every error uses the shared `{"error","code","fields"}` shape. Malformed scan UUIDs produce `400 VALIDATION_FAILED` with `fields.id = "invalid"`; missing scans produce `404 SCAN_NOT_FOUND`; malformed JSON or absent required fields produce `400 VALIDATION_FAILED`.
- `target_url` must use `http` or `https`, carry no credentials and name a public host. Rejected values return `400 VALIDATION_FAILED` with `fields.target_url` set to `invalid` (not a usable URL) or `not_public` (loopback, private, CGNAT, link-local or multicast address, single-label name, or an internal suffix such as `.local`, `.internal`, `.svc`). The check does not resolve DNS, so it cannot stop a public name pointing at a private address; that belongs in the engine's egress policy. Target ownership is not verified: restrict submissions to authorized testing.
- Queue publishing failures return `503 QUEUE_UNAVAILABLE` and mark the scan `FAILED`. `202` means accepted/queued, **not** completed.
- Reports are only generated for finished scans: a `PENDING` or `RUNNING` scan returns `409 SCAN_NOT_FINISHED`, while a `FAILED` scan gets a report with whatever results arrived. An unknown `format` or `lang` returns `400 VALIDATION_FAILED` with `fields.format = "oneof:pdf html md sarif"` and/or `fields.lang = "oneof:en pl"`, both in one response.
- Free scan data is publicly retrievable by its UUID. Keep sensitive targets out of public scans.
