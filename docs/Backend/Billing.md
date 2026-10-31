# 💳 Billing and the Pro plan

The backend sells one paid plan, **Pro**, as a Stripe subscription. Payment pages and card data never touch AntiGinx: users pay on **Stripe Checkout** and manage the subscription (cancel, change card, download invoices) in the **Stripe customer portal**. The backend keeps a copy of each user's subscription state in the `subscriptions` table and reads the plan from there.

Without `STRIPE_SECRET_KEY` and `STRIPE_PRO_PRICE_ID` billing is off: every user is on Free, `GET /api/billing` reports `"enabled": false` and the other billing routes answer `404 BILLING_DISABLED`. See [Configuration](./Configuration.md#billing).

## 📡 Endpoints

All routes except the webhook require a bearer access JWT and pass the origin check.

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/api/billing` | Current plan and subscription state, from the database. |
| `POST` | `/api/billing/checkout` | `{"url": "https://checkout.stripe.com/..."}` to send the user to. `409 ALREADY_SUBSCRIBED` when the user already pays. |
| `POST` | `/api/billing/portal` | `{"url": "https://billing.stripe.com/..."}` for the customer portal. `409 NO_BILLING_ACCOUNT` when the user never started a payment. |
| `POST` | `/api/billing/sync` | Fetches the state from Stripe, saves it and returns it like `GET /api/billing`. Limited to 10 calls a minute per user. |
| `POST` | `/api/billing/webhook` | Stripe events, authenticated by the `Stripe-Signature` header. `404 BILLING_DISABLED` without `STRIPE_WEBHOOK_SECRET`. |

`GET /api/billing` and `POST /api/billing/sync` return:

```json
{
  "enabled": true,
  "plan": "pro",
  "status": "active",
  "current_period_end": "2026-11-30T12:00:00Z",
  "cancel_at": null,
  "manageable": true
}
```

- `plan` is `free` or `pro`. `/api/auth/me` returns the same value as `plan`.
- `status` is the Stripe subscription status (`active`, `trialing`, `past_due`, `unpaid`, `canceled`, `incomplete`, `incomplete_expired`, `paused`), or `none` for a user who opened Checkout without paying.
- `active`, `trialing` and `past_due` give Pro. `past_due` keeps Pro while Stripe retries the payment; after the last retry Stripe moves the subscription to `canceled` or `unpaid`, depending on the account's settings.
- `cancel_at` is set once the user cancels: the subscription stays Pro until then and is not renewed.
- `manageable` says whether the customer portal has anything to show.

Checkout sends the user back to `PUBLIC_BASE_URL/dashboard/profile?billing=success` (or `?billing=cancel`), and the portal to `PUBLIC_BASE_URL/dashboard/profile?billing=portal`.

## 🔄 How the state stays current

Stripe is the source of truth. One function, `billing.Service.Sync`, fetches a customer's subscription from Stripe and overwrites the database row; every path below calls it, so the order or repetition of events does not matter.

1. **Return from Checkout or the portal.** The frontend calls `POST /api/billing/sync`, so the user sees Pro, or a cancellation, straight away.
2. **Webhook.** When Stripe can reach `/api/billing/webhook`, it reports renewals, cancellations and failed payments as they happen. The handler takes only the customer ID from the event and syncs that customer.
3. **Reconciliation.** Every `STRIPE_SYNC_INTERVAL` (one hour by default) and at startup, each instance syncs every account whose state can change on its own: a live subscription, or a Checkout opened in the last 25 hours. This is what keeps the state correct when webhooks cannot arrive, for example while the app is reachable only over WireGuard.

Before Checkout the backend syncs the user once more, so a stale row cannot lead to a second subscription.

## 🗑️ Deleting an account

`DELETE /api/auth/account` and `DELETE /api/admin/users/:id` delete the user's Stripe customer before deleting the account, which ends the subscription immediately, without proration. If Stripe cannot be reached the deletion fails with `502 BILLING_UNAVAILABLE` and nothing is deleted, so no account disappears while Stripe still bills it. With billing switched off, an account that still has a paying subscription cannot be deleted for the same reason.

## 🧾 Setting up Stripe

1. Create a product (for example "AntiGinx Pro") with a recurring price, and copy the price ID (`price_...`) into `STRIPE_PRO_PRICE_ID`.
2. Copy the secret key (`sk_test_...` in test mode) into `STRIPE_SECRET_KEY`. A restricted key (`rk_...`) works if it can write Customers, Checkout Sessions and Customer portal sessions, and read Subscriptions.
3. Configure the customer portal (Settings → Billing → Customer portal) and save it once; Stripe refuses portal sessions until a configuration exists. Allow cancelling, updating payment methods and viewing invoices.
4. Optional: add a webhook endpoint for `PUBLIC_BASE_URL/api/billing/webhook` with the events `checkout.session.completed`, `customer.subscription.*`, `invoice.paid`, `invoice.payment_failed` and `customer.deleted`, and copy its signing secret (`whsec_...`) into `STRIPE_WEBHOOK_SECRET`.

To receive webhooks locally, forward them with the Stripe CLI and use the secret it prints:

```sh
stripe listen --forward-to localhost:3000/api/billing/webhook
```

## 🔐 Gating features

`billing.UserPlan(db, userID)` reads the plan from the database without calling Stripe, so it is cheap enough to check on every request. Paid features (scheduled scans, premium tests) should check it on the backend; the `plan` field in `/api/auth/me` is only for the interface.
