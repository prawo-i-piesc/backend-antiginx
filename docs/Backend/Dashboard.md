# 📊 Dashboard statistics and preferences

The data behind the dashboard charts and widgets, and the per-account settings the dashboard saves, such as its widget layout. Everything here is computed by the database with aggregate queries over one account's scans (or, for administrators, the whole platform) inside a time window, so no view downloads scan results to count them itself.

## 📡 Endpoints

All routes require a bearer access JWT and read the current user's **premium** scans; free scans have no owner. `PUT` also passes the origin check.

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/api/users/stats/summary?days=` | Scans by status (all time), and for the window: scanned domains, tests run and failed, average and latest risk. |
| `GET` | `/api/users/stats/risk?days=&domain=` | Completed scans oldest first, each with its risk score, test count and failures. |
| `GET` | `/api/users/stats/severity?days=&domain=` | Failed results per severity and the number of passed results. |
| `GET` | `/api/users/stats/tests?days=&domain=&limit=` | Tests that failed at least once, most failures first. |
| `GET` | `/api/users/stats/domains` | Every target the account completed a scan of, most recently scanned first. |
| `GET` | `/api/users/preferences/:key` | The saved value, or `"value": null` when nothing was saved. |
| `PUT` | `/api/users/preferences/:key` | JSON `{"value": ...}`; replaces the saved value. |
| `GET` | `/api/admin/stats/tests?days=&limit=` | Admin only: most failed tests across free and premium scans. |

- `days` is the window, 1–365, default 90. `domain` matches `target_url` exactly. `limit` is 1–50, default 10.
- Only `COMPLETED` scans count towards risk, severity and tests; `summary.scans` counts every status.
- When the window holds no completed scan, `risk.average` and `risk.latest` are `null`, not `0`: zero risk is a result, no scans is none.
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

The risk of a scan is the average weight of its failed results, rounded and capped at 100; a scan where everything passed scores 0.

| Severity | Weight |
| --- | --- |
| `CRITICAL` | 100 |
| `HIGH` | 75 |
| `MEDIUM` | 50 |
| `LOW` | 25 |
| `INFO`, `NONE` and anything else | 5 |

This is the model the dashboard used to compute in the browser; it now lives in one place, `internal/stats`, so that a better model (issue #133) changes it there only.

## ⚙️ Preferences

Values are stored as JSON in `user_preferences`, one row per account and key. Only these keys are accepted; anything else answers `400 VALIDATION_FAILED` with `fields.key = "unknown"`:

| Key | Holds |
| --- | --- |
| `dashboard.widgets` | The user dashboard's widget layout. |
| `admin.widgets` | The admin overview's widget layout. |

A value is at most 16 KiB and may not be `null` (`400 VALIDATION_FAILED`). Deleting an account deletes its preferences with it.
