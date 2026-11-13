# 🔍 Scans and results API

All paths are relative to the backend's `:4000` HTTP server. See [Authentication](./Auth.md) for bearer tokens and [Configuration](./Configuration.md) for the queue dependencies. Send JSON with `Content-Type: application/json`.

## 📡 Endpoints

| Method | Path | Access | Purpose |
| --- | --- | --- | --- |
| `GET` | `/healthz` | Public | Liveness probe, `200` with `{"status":"ok"}`. No dependencies are checked. |
| `GET` | `/readyz` | Public | Readiness probe, `200` with `{"db":"up","rabbitmq":"up"}` or `503` when a dependency is down. |
| `GET` | `/api/health` | Public | Kept for compatibility: `200` with `{"message":"Running..."}`, no dependencies checked. |
| `POST` | `/api/freescans` | Public | Start a scan with every test ID except the Pro ones (`dir-listing`, `sensitive-files`). |
| `GET` | `/api/freescans/:id` | Public | Fetch a free scan with results by UUID. |
| `GET` | `/api/freescans/:id/report` | Public | Download a free scan report (PDF, HTML, Markdown or SARIF). |
| `GET` (WebSocket) | `/api/live/freescans/:id` | Public, `Origin` checked | Follow a free scan live; see [Live scan stream](#-live-scan-stream). |
| `POST` | `/api/scans` | Bearer access JWT | Start an account scan with selected tests. |
| `GET` | `/api/scans/:id` | Bearer access JWT | Fetch the current user's account scan. |
| `GET` | `/api/scans/:id/report` | Bearer access JWT | Download a report of the current user's account scan. |
| `GET` (WebSocket) | `/api/live/scans/:id` | `ag_access` cookie, `Origin` checked | Follow the current user's account scan live; see [Live scan stream](#-live-scan-stream). |
| `GET` | `/api/users/scans?q=&status=&limit=&offset=` | Bearer access JWT | A page of the current user's account scans, newest first; see [Account scan list](#-account-scan-list). |
| `GET` | `/api/users/widgets` | Bearer access JWT | Counts (`total_scans`, `detected_threats`, `safe_sites`) and up to four `recent_scans`. |
| `GET` | `/api/utils/tests` | Bearer access JWT | Grouped test IDs available to premium scans. |
| `GET` | `/api/favicons/:hash` | Public | A scanned site's icon by content hash; see [Site icons](#-site-icons). |

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

Poll `GET /api/freescans/<uuid>`; this route is public. It returns `id`, `target_url`, `status`, `created_at`, `started_at`, `completed_at`, `tests` (the test IDs requested, null on scans older than this field), `favicon` (see [Site icons](#-site-icons)) and `results`. Timestamps are null until set. `results` entries contain `id`, `scan_id`, `test_name`, `severity`, `passed`, `message` and `metadata`.

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
| Privacy & Session Management | `cookie-sec`, `cache-control` |
| Reconnaissance & Server Information | `serv-h-a`, `sitemap`, `robots-txt`, `bot-protection` |
| Vulnerabilities & Code Analysis | `js-obf`, `phishing-url`, `dns-reputation`, `urlhaus`, `favicon-origin`, `dir-listing`, `sensitive-files` |

The list has to match the engine's (`--tests` in `App/parser/config/paramregistry.go`): one ID the engine does not know fails the whole scan before any test runs. `urlhaus` reports an `Info` result without checking anything unless the daemon has `ANTIGINX_URLHAUS_AUTH_KEY`.

`dir-listing` and `sensitive-files` request paths the site does not advertise (`.env`, `.git`, backups, directories), so they need the **Pro** plan and never run in anonymous free scans. `GET /api/utils/tests` names them in each group's `pro_tests`. A scan that asks for one on another plan answers `403 PRO_REQUIRED` and nothing is queued, rather than quietly running the rest.

Poll `GET /api/scans/<uuid>` with the **same user's** bearer token. A different user receives `404`. `GET /api/users/scans` returns a page of that user's scans (see [Account scan list](#-account-scan-list)), while `GET /api/users/widgets` returns dashboard aggregates.

## 📋 Account scan list

`GET /api/users/scans` returns one page of the user's account scans, newest first. The database pages and filters it, so no request carries the whole history.

| Parameter | Meaning |
| --- | --- |
| `q` | A part of the target address, case-insensitive; `%` and `_` match themselves. Up to 2048 characters. |
| `status` | `active` (still `PENDING` or `RUNNING`), `COMPLETED` or `FAILED`; every status when left out. |
| `limit` | 1–100, default 25. |
| `offset` | Rows to skip, from 0. |

```json
{
  "scans": [
    {
      "id": "01a1…",
      "target_url": "https://shop.example.com",
      "status": "RUNNING",
      "created_at": "2026-11-13T10:02:11Z",
      "started_at": "2026-11-13T10:02:12Z",
      "completed_at": null,
      "tests": ["https", "hsts", "csp"],
      "answered": 2,
      "risk": 25,
      "favicon": "3f2a…"
    }
  ],
  "total": 42,
  "limit": 25,
  "offset": 0
}
```

- A row has no `results`: they are what weighs the most, and the list does not show them. `GET /api/scans/:id` has the whole scan.
- `tests` lists the requested test IDs, `null` on scans older than that column. `answered` counts the results that name a test, which is the scan's progress, since a test has at most one result. An engine notice about a test counts, because it is that test's answer.
- `risk` is the score from the same model as the charts (see [Dashboard](./Dashboard.md)), computed from the results so far, so a running scan has one too. It is `null` until the scan has any test outcome, and stays `null` for a scan with nothing but engine notices: a scan that checked nothing has no risk, rather than a risk of 0.
- `risk`, `answered` and `favicon` are worked out for the rows on the page only, with one query each.
- There is no risk filter: the score is not stored, so the database cannot filter or count by it.
- Unknown `status` values and out-of-range `limit` or `offset` return `400 VALIDATION_FAILED`.

## 🔴 Live scan stream

`/api/live/*` upgrades to a WebSocket that sends a scan's state and every change to it. It is an accelerator, not a second source of truth: everything it sends can also be read from `GET /api/freescans/:id` or `GET /api/scans/:id`, so a client that cannot connect can poll those instead.

**Routing.** A Next.js route handler cannot pass an upgrade through, so the frontend's `/api` proxy does not carry this. The ingress must send the `/api/live` prefix straight to the backend, as `apps/antiginx/frontend/ingress.yaml` does in the infrastructure repository.

**Access.** A browser cannot set `Authorization` on a WebSocket, so account scans read the access token from the `ag_access` cookie, the one the frontend's proxy keeps it in. Only these routes accept it there, and an `Origin`, which browsers always send here, must match `PUBLIC_BASE_URL`, so another site cannot open a connection with a visitor's cookie. Refusals are ordinary HTTP responses sent before the upgrade: `400 VALIDATION_FAILED` for a malformed ID, `403` for a foreign `Origin`, `401 SESSION_EXPIRED`, `404 SCAN_NOT_FOUND` (also for someone else's scan) and `429 RATE_LIMITED`. A connection is limited to 12 at a time per account or address, 500 per process, and 120 new connections per minute per address.

**Messages** are JSON text frames from the server; the client sends nothing:

| `type` | Fields | Meaning |
| --- | --- | --- |
| `snapshot` | `scan` | The whole scan with the fields of the REST response, without its owner. Always first, and again after every reconnect, so there is no replay to manage. |
| `results` | `results` | Result rows not sent yet on this connection. Rows from two consumers can commit out of `id` order, so `id` says nothing about what has been seen; merge by `id` across reconnects. |
| `status` | `status`, `started_at`, `completed_at` | The scan's state changed. |
| `heartbeat` | | Sent about every 20 seconds, once the client has answered a ping. JavaScript cannot see ping frames, so a silence of 50 seconds means the link is dead. |

**Closing.** The stream ends with a close code that says what to do next:

| Code | Meaning | Client should |
| --- | --- | --- |
| `1000` | The scan finished; the final state has been sent. | Stop. |
| `1001` | The server is restarting. | Reconnect. |
| `4401` | The access token expired. | Refresh the session, then reconnect. |
| `4404` | The scan was deleted. | Stop. |
| `4408` | The connection reached its 15-minute limit, which is what ends a stream for a scan that never finishes. | Reconnect while still interested. |
| Any other, such as `1011` or `1013` | Something went wrong on the way. `1013` means the last read failed, so the client may be missing results. | Reconnect with a backoff. |

A scan's final `status` is held back for a second, because messages are spread over several consumers and the end message can be committed just before the last results. A batch whose save failed can land later still, so a client should read the scan over REST once after `1000`.

**How changes travel.** The transaction that saves results also sends `pg_notify('scan_events', <scanId>)`. Postgres delivers it only after the commit, and to every replica, so it does not matter which one consumed the message. Each connection then reads from the database whatever it has not sent yet. Each one also rereads every 5 seconds, so a lost notification or a dropped `LISTEN` connection only delays a result. No state is buffered per client, so a slow client cannot grow the server's memory: anything it cannot take yet stays in the database, and a write stuck for 10 seconds closes the connection.

## 🖼️ Site icons

Every response that shows a scan's address also carries `favicon`: the SHA-256 of the site's icon, or `null` while it is unknown. That covers `GET /api/freescans/:id`, `GET /api/scans/:id`, `GET /api/users/scans`, `recent_scans` in `GET /api/users/widgets` and `GET /api/admin/widgets`, `GET /api/users/stats/domains` and both admin scan routes. `GET /api/users/stats/risk` has one point per scan, so it carries the hashes once, as `favicons`, an object from `target_url` to hash. The image is at `GET /api/favicons/<hash>`.

- **One icon per site.** A site is the host, plus the port when it is not the scheme's default; `http://` and `https://` share it. Sites with the same icon share one stored row, keyed by the hash of what is served.
- **Fetched in the background.** Submitting a scan queues its site, and so does reading a scan whose site has no row yet, which is how scans older than this feature get icons. A request never waits for a fetch; the hash shows up on a later read. The queue holds 256 sites and two workers serve it; when full, new requests are dropped and come back on the next read.
- **One fetch across replicas.** A worker claims a site with a single `INSERT … ON CONFLICT … RETURNING` on `site_icons`, so only one instance fetches it. A found icon is checked again after 30 days, a missing one after a day. When a site cannot be reached its stored icon stays.
- **Discovery.** The fetcher reads up to 256 KiB of the home page (after up to three redirects), takes the `<link rel="icon">` closest to 32 px, then an SVG, an unsized one, the remaining sizes and `apple-touch-icon`, and finally `/favicon.ico`. Inline `data:` icons are used as they are. At most four candidates are tried per site, each file up to 2 MiB.
- **Normalization.** The file's content decides its type, not the server's `Content-Type`. From an ICO only the entry closest to 32 px is kept. A raster image larger than 64 px is scaled down to 64 px and stored as PNG; dimensions above 2048 px are refused before decoding. SVG up to 64 KiB is stored as is. A larger SVG that only wraps one embedded raster image, the way design tools export icons, is cut to its `viewBox` as a browser would draw it (following `transform`, `<use>` and the image's aspect fit) and stored as a 64 px PNG; any other large SVG is refused, since drawing shapes would need an SVG renderer. Stored icons are at most 64 KiB, typically 0.3–10 KiB.
- **SSRF.** Every connection, including after a redirect, checks the IP it actually dials, so a public name resolving to a private or cluster address is refused, and no proxy from the environment is used. `ANTIGINX_ALLOW_PRIVATE_TARGETS=true` lifts this together with the target check (development only).
- **Serving.** `GET /api/favicons/<hash>` returns the stored bytes with `Cache-Control: public, max-age=31536000, immutable` and `ETag: "<hash>"`. `If-None-Match` gets `304` without a database read. `Content-Security-Policy: default-src 'none'; style-src 'unsafe-inline'; sandbox` keeps an SVG opened directly from running script in the app's origin. Unknown hashes return `404 NOT_FOUND`. An icon no site points at any more is deleted when its last site changes icon.

The route is public because the free scan shows icons without an account. Knowing a site's icon hash lets anyone check whether that site has been scanned on this instance; icons are public data, and the instance is reachable only inside the VPN.

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

The backend publishes persistent JSON through `main_exchange` with routing key `scan_key`, which delivers it to `scan_queue`, and waits for the broker's publisher confirm. `scan_queue` is a quorum queue: after three deliveries the daemon could not finish, the task is dead-lettered through the `scan_dlx` fanout into `scan_dlq` (kept for inspection) and `scan_failed`, which the backend consumes to mark the scan `FAILED` if it is still `PENDING` or `RUNNING`; a scan that already completed is left alone. When the broker nacks the task (for example a quorum without majority, or a length limit hit under `x-overflow: reject-publish`), is unreachable or the confirm does not arrive within 5 seconds, the scan is marked `FAILED` and the API returns `503 QUEUE_UNAVAILABLE`. Example task, in the shape of the daemon's `ScanTask`:

```json
{
  "Target": "example.com",
  "Parameters": [
    {"Name": "--tests", "Arguments": ["https", "hsts"]}
  ],
  "TaskId": "<scanId>"
}
```

`Target` is the validated `target_url` **without its scheme**: `host[:port][/path][?query]`, with the host lowercased and the fragment dropped (`https://Example.com:8443/app?x=1#top` → `example.com:8443/app?x=1`). The engine refuses a target that starts with `http` and adds the scheme itself: `http://` when the `https` or `hsts` test is selected (to check the redirect to HTTPS), `https://` otherwise. The scan record keeps the full `target_url`, which is what the API and reports show.

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

Delivery is at-least-once: a result saved right before the connection dropped can arrive again, and a task the daemon retries sends all its results again. A unique index on (`scan_id`, `test_name`) keeps one row per test, and a result that is already stored is skipped. Messages are spread over several consumers, so the end message can be committed a few milliseconds before the last results of the same scan.

## ⚠️ Errors and limitations

- Every error uses the shared `{"error","code","fields"}` shape. Malformed scan UUIDs produce `400 VALIDATION_FAILED` with `fields.id = "invalid"`; missing scans produce `404 SCAN_NOT_FOUND`; malformed JSON or absent required fields produce `400 VALIDATION_FAILED`.
- `target_url` must use `http` or `https`, carry no credentials and name a public host. Rejected values return `400 VALIDATION_FAILED` with `fields.target_url` set to `invalid` (not a usable URL) or `not_public` (loopback, private, CGNAT, link-local or multicast address, single-label name, or an internal suffix such as `.local`, `.internal`, `.svc`). In development, `ANTIGINX_ALLOW_PRIVATE_TARGETS=true` lifts the `not_public` check (see [Configuration](./Configuration.md)); `invalid` still applies. The check does not resolve DNS, so it cannot stop a public name pointing at a private address; that belongs in the engine's egress policy. Target ownership is not verified: restrict submissions to authorized testing.
- Queue publishing failures return `503 QUEUE_UNAVAILABLE` and mark the scan `FAILED`. `202` means accepted/queued, **not** completed.
- Reports are only generated for finished scans: a `PENDING` or `RUNNING` scan returns `409 SCAN_NOT_FINISHED`, while a `FAILED` scan gets a report with whatever results arrived. An unknown `format` or `lang` returns `400 VALIDATION_FAILED` with `fields.format = "oneof:pdf html md sarif"` and/or `fields.lang = "oneof:en pl"`, both in one response.
- Free scan data is publicly retrievable by its UUID. Keep sensitive targets out of public scans.
