# 📊 Dashboard statistics and preferences

The data behind the dashboard charts and widgets, and the per-account settings the dashboard saves, such as its widget layout. Everything here is computed by the database with aggregate queries over one account's scans (or, for administrators, the whole platform) inside a time window, so no view downloads scan results to count them itself.

## 📡 Endpoints

All routes require a bearer access JWT and read the current user's **premium** scans; free scans have no owner. `PUT` and `DELETE` also pass the origin check.

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/api/users/stats/summary?days=&domain=` | Scans by status (all time), and for the window: scanned domains, tests run and failed, average and latest risk. With `domain`, all of it for that target only. |
| `GET` | `/api/users/stats/risk?days=&domain=` | Completed scans oldest first, each with its risk score, test count and failures. |
| `GET` | `/api/users/stats/severity?days=&domain=` | Failed results per severity and the number of passed results. |
| `GET` | `/api/users/stats/tests?days=&domain=&limit=` | Tests that failed at least once, most failures first. |
| `GET` | `/api/users/stats/domains` | Every target the account completed a scan of, most recently scanned first. |
| `GET` | `/api/users/preferences/:key` | The saved value, or `"value": null` when nothing was saved. |
| `PUT` | `/api/users/preferences/:key` | JSON `{"value": [...]}`; replaces the saved value. |
| `DELETE` | `/api/users/preferences/:key` | Forgets the saved value (`204`), so the account follows the defaults again, including defaults that change later. |
| `GET` | `/api/admin/stats/tests?days=&limit=` | Admin only: most failed tests across free and premium scans. |

- `days` is the window, 1–365, default 90. `domain` matches `target_url` exactly. `limit` is 1–50, default 10.
- Only `COMPLETED` scans with at least one test outcome count towards risk, severity, tests and domains; `summary.scans` counts every status.
- Engine notices (a test aborted or blocked by bot protection, stored with `notice = true`) are not outcomes and are left out everywhere. A scan where every test was blocked therefore has no risk at all, instead of a risk of 0 that would read as "all clear".
- When the window holds no such scan, `risk.average` and `risk.latest` are `null`, not `0`: zero risk is a result, no scans is none. `risk.average` is the mean of the scans' unrounded scores.
- `GET /api/admin/widgets` also reports `new_users`, the accounts created in the last 7 days.

`GET /api/users/stats/risk` returns:

```json
{
  "days": 90,
  "points": [
    {
      "scan_id": "0b6f…",
      "target_url": "https://shop.example.com",
      "created_at": "2026-10-28T14:02:11Z",
      "tests": 6,
      "failed": 2,
      "risk": 88
    }
  ]
}
```

## 🎯 Risk score

The risk of one scan is a number from 0 to 100, computed in `internal/stats/risk.go` from its failed results. `None` and `Info` results count as passed, and engine notices are not results at all. The frontend computes the same score for the scan list in `app/lib/risk.ts`, so a change goes into both.

| Band | Score | One finding of this severity scores |
| --- | --- | --- |
| None | 0 | — |
| Low | 1–24 | `LOW` 5 |
| Medium | 25–49 | `MEDIUM` 25 |
| High | 50–74 | `HIGH` 50 |
| Critical | 75–100 | `CRITICAL` 75 |

1. **The worst finding sets the band.** A single finding scores the bottom of its severity's band.
2. **Every further finding raises the score.** Each closes its weight's share of the remaining distance to 100, as independent events would: `100 × (1 − Π(1 − w))` with `w` = 0.05, 0.25, 0.50, 0.75. More problems never lower the risk. The previous model, the average weight of the failed tests, did: one critical finding scored 100, and the same finding plus ten low ones scored 32.
3. **Minor problems do not pass for serious ones.** With only low findings the score stops at 49, with a medium one at worst at 74, with a high one at worst at 94.
4. **One cause counts once.** When `HTTPS Protocol Verification` fails, the TLS failure in `SSL Certificate Security Analysis` and the missing `HSTS Header Analysis` follow from it and are not counted again.
5. A failed result with an unknown severity counts as `LOW`.

| Findings | Score |
| --- | --- |
| Four low, one medium (a site missing a few headers and its CSP) | 39 |
| One critical, ten low | 85 |
| Two high | 75 |
| Site served over plain HTTP (HTTPS, certificate and HSTS all failing) | 25 |

`risk.average` in the summary is the mean of the scans' unrounded scores.

## ⚙️ Preferences

Values are stored as JSON in `user_preferences`, one row per account and key. Only these keys are accepted; anything else answers `400 VALIDATION_FAILED` with `fields.key = "unknown"`:

| Key | Holds |
| --- | --- |
| `dashboard.widgets` | The user dashboard's widget layout. |
| `admin.widgets` | The admin overview's widget layout. |

Every key holds a widget layout, so a value must be a JSON array, at most 16 KiB once normalised; anything else answers `400 VALIDATION_FAILED`. The value is stored as Postgres will keep it: strings may not contain `\u0000`, and lone UTF-16 surrogates are replaced with U+FFFD. A foreign key with `ON DELETE CASCADE` removes an account's preferences with the account, including a write that arrives after the deletion (that request answers `401 SESSION_EXPIRED`).
