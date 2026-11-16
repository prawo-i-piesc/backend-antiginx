# 🗓️ Scheduled scans

A schedule runs an account scan of one target, with chosen tests, daily, weekly or monthly at a set time. Schedules are a **Pro** feature: an account without Pro gets `403 PRO_REQUIRED` when it tries to add, edit or resume one. It can still pause and delete its schedules, and those it already has stop running until it has Pro again.

A scan started by a schedule is an ordinary account scan. It appears in `GET /api/users/scans` with `schedule_id` set, and has results, reports and statistics like any other.

## 📡 Endpoints

| Method | Path | Access | Purpose |
| --- | --- | --- | --- |
| `GET` | `/api/schedules` | Bearer access JWT | The current user's schedules, newest first, with the account's limit. |
| `GET` | `/api/schedules/calendar?from=&to=` | Bearer access JWT | Planned terms and past runs in a time range; see [Calendar](#-calendar). |
| `POST` | `/api/schedules` | Bearer access JWT, `Origin` checked, Pro | Add a schedule. It is enabled at once. |
| `PUT` | `/api/schedules/:id` | Bearer access JWT, `Origin` checked, Pro | Replace the target, tests and cadence. Pause and block stay as they are. |
| `PATCH` | `/api/schedules/:id` | Bearer access JWT, `Origin` checked | `{"enabled":false}` pauses; `{"enabled":true}` resumes (Pro, not while blocked). |
| `DELETE` | `/api/schedules/:id` | Bearer access JWT, `Origin` checked | Delete a schedule. Its scans stay, with their `schedule_id`. |
| `GET` | `/api/admin/schedules?q=&state=&user_id=&limit=&offset=` | Admin JWT | A page of every account's schedules; see [Admin](#-admin). |
| `PUT` | `/api/admin/schedules/:id/block` | Admin JWT | Block a schedule, with an optional reason. |
| `DELETE` | `/api/admin/schedules/:id/block` | Admin JWT | Lift the block. |

A schedule of another account answers `404 SCHEDULE_NOT_FOUND`, as if it did not exist.

## ➕ Add or edit a schedule

```http
POST /api/schedules
Authorization: Bearer <access_token>
Content-Type: application/json

{
  "target_url": "https://shop.example.com",
  "tests": ["https", "hsts", "csp"],
  "frequency": "weekly",
  "time": "08:00",
  "weekday": 1,
  "timezone": "Europe/Warsaw"
}
```

| Field | Meaning |
| --- | --- |
| `target_url` | Validated like an account scan's target: a public `http(s)` host. |
| `tests` | Test IDs from `GET /api/utils/tests`; unknown IDs and repeats are dropped, `400 NO_VALID_TESTS` when none is left. |
| `frequency` | `daily`, `weekly` or `monthly`. Nothing runs more often than once a day. |
| `time` | `HH:MM`, 24-hour, in `timezone`. |
| `weekday` | Weekly only: `0` (Sunday) to `6` (Saturday), as in `Date.getDay()`. Ignored otherwise. |
| `month_day` | Monthly only: `1` to `28`, a day every month has. Ignored otherwise. |
| `timezone` | IANA zone of the user, e.g. `Intl.DateTimeFormat().resolvedOptions().timeZone`. The time stays the same on the user's clock across daylight saving changes. |

A field that fails answers `400 VALIDATION_FAILED` with that field in `fields`. An account has at most `SCHEDULE_LIMIT` schedules (10 by default); the next one answers `409 SCHEDULE_LIMIT_REACHED`. `POST` answers `201` and `PUT` and `PATCH` answer `200`, each with the schedule:

```json
{
  "id": "01a2…",
  "target_url": "https://shop.example.com",
  "tests": ["https", "hsts", "csp"],
  "frequency": "weekly",
  "time": "08:00",
  "weekday": 1,
  "month_day": null,
  "timezone": "Europe/Warsaw",
  "enabled": true,
  "blocked": false,
  "blocked_at": null,
  "block_reason": "",
  "next_run_at": "2026-11-23T07:00:00Z",
  "last_run_at": "2026-11-16T07:00:00Z",
  "last_error": "",
  "last_scan": {"id": "01a1…", "status": "COMPLETED", "created_at": "2026-11-16T07:00:00Z", "risk": 25},
  "runs": 12,
  "created_at": "2026-08-31T12:00:00Z",
  "favicon": "3f2a…"
}
```

`next_run_at` is null while the schedule is paused or blocked. `last_scan` is the newest scan the schedule started and `runs` how many it has started. `last_error` says why the last term passed without a scan:

| `last_error` | Meaning |
| --- | --- |
| `PRO_REQUIRED` | The account had no Pro plan at the time. |
| `QUEUE_UNAVAILABLE` | The scan queue did not accept the task; the scan is recorded as `FAILED`. |
| `INVALID_TARGET` | The target no longer passes validation, e.g. its name now points at a private address. |

Resuming counts the next term from now: terms missed while paused are not made up.

## 📆 Calendar

`GET /api/schedules/calendar?from=2026-10-26T23:00:00Z&to=2026-12-07T23:00:00Z` takes RFC 3339 instants, at most 45 days apart, so a month view in the user's time zone asks for exactly its grid.

```json
{
  "planned": [
    {"schedule_id": "01a2…", "target_url": "https://shop.example.com", "at": "2026-11-23T07:00:00Z"}
  ],
  "runs": [
    {"scan_id": "01a1…", "schedule_id": "01a2…", "target_url": "https://shop.example.com", "status": "COMPLETED", "at": "2026-11-16T07:00:00Z", "risk": 25}
  ]
}
```

`planned` lists the terms of enabled, unblocked schedules from now until `to`; a term that has passed is in `runs` as the scan it started. `runs` lists the account's scans started by any schedule, including deleted ones, in the range.

## ⏱️ How schedules run

Every instance checks for due schedules every `SCHEDULER_INTERVAL` (30 seconds by default), up to 50 at a time. For each one it first moves `next_run_at` to the next term, on the condition that it still holds the value it read. Only one instance wins that update, so several replicas never start the same term twice. The winner then checks the plan and publishes the scan like `POST /api/scans` does, without anti-bot detection.

Terms missed while the backend was down are not made up: after downtime a schedule runs once and moves on.

## 🛡️ Admin

`GET /api/admin/schedules` returns a page of every account's schedules, newest first, in the shape above plus `user_id` and `user_email`.

| Parameter | Meaning |
| --- | --- |
| `q` | A part of the target address or the owner's email, case-insensitive. |
| `state` | `active`, `paused` (by the user) or `blocked` (by an administrator). |
| `user_id` | One account's schedules. |
| `limit`, `offset` | As in the scan lists: 1–100, default 25. |

`PUT /api/admin/schedules/:id/block` with `{"reason":"…"}` (up to 200 characters, optional) blocks a schedule: it stops running, and the user sees the reason and cannot resume it (`409 SCHEDULE_BLOCKED`). Blocking again only changes the reason. `DELETE /api/admin/schedules/:id/block` lifts the block; a schedule the user had enabled continues from its next term. Both answer `200` with the schedule and leave a line in the log, like other admin writes.
