# Frontend Task: Implement the New Pill4rs APIs

You are the frontend engineer for Pill4rs (React/Next.js + TypeScript). The backend
just shipped a large batch of new endpoints: prepaid wallet + billing, creative media,
a platform capability matrix, Zernio ad-network connections, Oma's workspace-wide review,
and new campaign fields (bidding/pacing/rationale) with forecasting and health actions.

Implement the UI and API client for all of it. Work through the sections below in order.
Do not invent response shapes — every contract here is authoritative and mirrors the Go
handlers.

## 1. Conventions (read first)

- **Base URL**: `NEXT_PUBLIC_API_URL` (e.g. `http://localhost:8080`). All routes are under `/api`.
- **Auth**: the backend sets an **httpOnly cookie** named `access_token` at login/signup.
  Every request must use `credentials: "include"` (fetch) or `withCredentials: true` (axios).
  There is no `Authorization` header. A `401` means the cookie is missing/expired — call
  `POST /api/auth/refresh`, then retry once; if it still 401s, redirect to login.
- **Errors**: every error is `{ "error": "human readable message" }` with a non-2xx status.
  Surface `error` in a toast/inline message. Some endpoints also return extra fields on error
  (see campaign create `warnings`, action approve `action`).
- **IDs**: all ids are UUID strings.
- **Money**: amounts are **minor units** (cents/kobo) as integers, plus a `currency`
  (`"USD"` or `"NGN"`). Format for display with the currency's exponent (100). Never send
  floats for money.
- **Dates**: calendar dates are `YYYY-MM-DD`; timestamps are RFC3339 UTC.
- **OAuth flows**: `connect` endpoints return a URL; you **must redirect the browser** to it
  (do not fetch it). The provider then redirects back to the backend, which redirects the
  browser to the frontend with query params like `?connected=meta` or `?error=...`.
- Public/unauthenticated: `/api/auth/*`, `/api/webhooks/*`, and `/media/*` (uploaded files).
- The server body limit is 66 MB (media uploads).

Suggested files: `lib/api.ts` (typed client), `lib/money.ts`, `hooks/useWallet.ts`, etc.

## 2. Wallet & billing

### GET `/api/wallet`
Returns the workspace wallet and every currency balance.
```json
{
  "wallet_id": "uuid",
  "status": "active",
  "balances": [
    { "currency": "USD", "balance_minor": 125000 },
    { "currency": "NGN", "balance_minor": 0 }
  ]
}
```
UI: balance cards per currency, a "low balance" warning, and a top-up CTA.

### GET `/api/wallet/transactions?limit=50&offset=0`
Newest-first ledger. `limit` defaults to 50, max 200.
```json
[
  {
    "id": "uuid",
    "currency": "USD",
    "kind": "topup",
    "amount_minor": 50000,
    "balance_after_minor": 125000,
    "source_type": "topup",
    "source_id": "flw-tx-123",
    "created_at": "2026-01-01T12:00:00Z"
  }
]
```
`kind` ∈ `topup | media_spend | commission | adjustment | refund | fx`.
`amount_minor` is **signed**: positive = credit, negative = debit. Render credits green,
debits red. Build a paginated ledger table with a filter by kind and currency.
`source_type` / `source_id` are optional.

### POST `/api/wallet/topup`
Provider-confirmed/manual credit (idempotent by `provider` + `reference`).
```json
{ "currency": "USD", "amount_minor": 50000, "provider": "manual", "reference": "inv-001" }
```
Returns the updated wallet summary (same shape as `GET /api/wallet`), or
`{ "status": "credited" }` in a rare fallback. `provider` defaults to `"manual"`.
Errors: 400 `amount must be positive`, `payment reference is required`,
`currency must be one of: USD, NGN`.

### POST `/api/wallet/checkout`
Creates a Flutterwave hosted payment for a top-up. **This is the primary top-up UX.**
Request:
```json
{ "currency": "USD", "amount_minor": 50000, "email": "user@example.com" }
```
`email` optional (falls back to the logged-in user's email). Response:
```json
{ "checkout_url": "https://checkout.flutterwave.com/...", "tx_ref": "pill4rs-..." }
```
Redirect the browser to `checkout_url`. Flutterwave returns the user to
`/settings/billing?status=complete`. The wallet is credited **only** by the webhook, so on
return poll `GET /api/wallet` a few times (or optimistically show "processing") until the
balance updates.
Errors: 503 `billing is not configured` (hide/disable top-up UI when you receive this),
400 currency/amount validation.

### PATCH `/api/integrations/accounts/:id/billing`
Configure how an ad account is funded.
```json
{
  "billing_mode": "byob",
  "payer": "agency",
  "payment_instrument": "card_123",
  "currency": "USD",
  "spend_limit_minor": 100000
}
```
All fields optional (partial update). `billing_mode` ∈ `byob | managed`;
`payer` ∈ `agency | pill4rs`; `currency` ∈ `USD | NGN`. Response:
```json
{ "id": "uuid", "billing_mode": "byob", "payer": "agency", "currency": "USD", "payment_instrument": "card_123" }
```
UI: per-connected-account billing settings panel. When `billing_mode = managed`, show a note
that spend is debited from the prepaid wallet and surface the account's spend limit.

## 3. Creative media library

Uploaded media is served publicly so ad platforms can fetch it by URL.

### GET `/api/media`
```json
[
  { "id": "uuid", "kind": "image", "url": "http://.../media/abc.png", "mime": "image/png",
    "bytes": 20480, "status": "ready", "created_at": "2026-01-01T12:00:00Z",
    "width": 1080, "height": 1080 }
]
```
Optional fields: `width`, `height`, `duration_ms` (videos). `kind` ∈ `image | video`.

### POST `/api/media`
`multipart/form-data` with a single field named **`file`**. Returns `201` with the created
asset (same shape as list items). Enforce a client-side 66 MB cap and show an upload progress
bar. Accepted types are validated server-side; surface 400 messages.

### DELETE `/api/media/:id`
Returns `204 No Content`. Add a confirm dialog.

UI: a media picker/gallery used by the campaign creative form. When a user picks an asset,
set the campaign creative's `media_asset_id` (and/or `image_url` / `video_url` from `url`).

## 4. Platforms

### GET `/api/platforms/capabilities`
Static matrix, fetch once and cache. Shape:
```json
{
  "capabilities": {
    "meta": {
      "available": true,
      "targeting": { "geo": "supported", "age": "supported", "dayparting": "supported" },
      "bidding": { "lowest_cost": "supported", "bid_cap": "supported" },
      "frequency_cap": "supported",
      "pacing": { "standard": "supported", "accelerated": "deprecated" },
      "creative": { "single_image": "supported", "video": "supported" },
      "objectives": { "awareness": "supported", "sales": "supported" },
      "forecast": "supported"
    }
  }
}
```
Every capability value is `supported | partial | unsupported | deprecated`.
Use this to **dynamically drive the campaign builder**: only show targeting/bidding/pacing/
creative/objective controls the selected platform supports (disable or hide `unsupported` /
`deprecated`, warn on `partial`).

### GET `/api/platforms`
Workspace-aware overview: every known platform plus connection state.
```json
{
  "platforms": [
    {
      "platform": "meta",
      "connected": true,
      "accounts": 2,
      "capabilities": { "...": "same object as above" }
    }
  ]
}
```
UI: a platforms/connections page showing connected vs available platforms with account counts
and a connect CTA.

## 5. Zernio connections (LinkedIn, Pinterest, X, OpenAI Ads, + managed Meta/Google/TikTok)

### GET `/api/integrations/zernio/connect?platform=linkedin&profile_id=optional`
`platform` is required (`facebook`, `instagram`, `linkedin`, `tiktok`, `twitter`,
`pinterest`, `googleads`). Two possible responses:
```json
{ "auth_url": "https://...", "state": "..." }
```
→ redirect the browser to `auth_url`.
```json
{ "already_connected": true, "accounts": 3 }
```
→ treat as success, refresh the account list.
Errors: 503 `zernio is not configured` (hide Zernio networks), 400 missing platform.

### GET `/api/integrations/zernio/callback` and `/callback/:profile_id`
Backend-handled redirect target. On completion the browser lands on
`/settings/integrations?connected=zernio` or `?error=zernio_denied|zernio_sync_failed`.
Handle those query params on the integrations page (toast + refresh).

### POST `/api/integrations/zernio/sync?profile_id=optional`
Re-syncs Zernio accounts. Response `{ "accounts": 3 }`. Add a "Sync" button.

Note: after any successful connection/sync, refetch `GET /api/integrations` and
`GET /api/platforms` — new ad accounts appear there.

## 6. Oma workspace review

### POST `/api/ai/review`
Request (all optional):
```json
{ "propose": true, "auto_apply": false }
```
- `propose` (default `true`): queue recommended actions.
- `auto_apply`: only meaningful when `propose` is true; executes actions immediately.

Response always has `review`. When `propose:false`:
```json
{ "review": { "...": "review payload" } }
```
When proposing without auto-apply, adds `proposed` (array of actions); with auto-apply,
adds `executed` (array of actions, or `{ "id": "...", "error": "..." }` for failures):
```json
{ "review": {}, "proposed": [ { "id": "uuid", "actor": "oma", "type": "...", "status": "pending", "payload": {}, "created_at": "..." } ] }
```
UI: a "Review with Oma" button. Show a results summary; when proposals are returned, link
to the existing `/api/actions` approval queue. Offer an "Auto-apply" toggle behind a
confirmation (it mutates live campaigns). This call can be slow — show a loading state and
disable the button while in flight.

### Action shape (used by review + `/api/actions`)
```json
{
  "id": "uuid",
  "actor": "oma | user",
  "type": "set_status | update_budget | create_campaign | ...",
  "status": "pending | executed | rejected | failed",
  "payload": {},
  "result": {},
  "error": "optional",
  "created_at": "2026-01-01T12:00:00Z",
  "updated_at": "2026-01-01T12:00:00Z"
}
```
`payload` / `result` are free-form JSON. Approve/reject via existing
`POST /api/actions/:id/approve` and `/reject`.

## 7. Campaign builder upgrades

### POST `/api/campaigns` — new request fields
```json
{
  "name": "Spring Sale",
  "objective": "sales",
  "daily_budget": 50,
  "currency": "USD",
  "start_date": "2026-04-01",
  "end_date": "2026-04-30",
  "cta": "shop_now",
  "ad_account_ids": ["uuid"],
  "platforms": ["meta", "tiktok"],
  "bid_strategy": "lowest_cost",
  "bid_cap": 2.5,
  "pacing_type": "standard",
  "frequency_cap": 3,
  "frequency_cap_time_unit": "day",
  "targeting": {},
  "creative": {
    "primary_text": "...", "headline": "...", "description": "...",
    "link_url": "https://...", "image_url": "https://...",
    "video_url": "", "format": "single_image", "media_asset_id": "uuid"
  },
  "variants": [ { "targeting": {}, "creative": { "...": "..." } } ],
  "provenance": { "name": "user" },
  "rationale": { "daily_budget": "keeps CAC under target" }
}
```
`bid_strategy` / `bid_cap` / `pacing_type` / `frequency_cap` / `frequency_cap_time_unit` /
`targeting` / `variants` / `provenance` / `rationale` are all optional. Gate the bidding,
pacing and frequency controls using `GET /api/platforms/capabilities`.
`rationale` is a `field -> explanation` map Oma writes; display it as helper text / tooltips.
`variants` enables per-variant targeting + creative; if present it takes precedence over
top-level `targeting`/`creative`.

Response `201`:
```json
{ "campaigns": [ { "id": "uuid", "platform": "meta", "status": "draft", "is_draft": true, "...": "..." } ], "warnings": ["..."] }
```
If **nothing** could be created you get `400` with `{ "error": "...", "warnings": [...] }`.
Always render `warnings` (per-platform partial failures) — success is not all-or-nothing.

Campaign objects may also include: `external_campaign_id`, `objective`, `daily_budget`,
`start_date`, `end_date`, `cta`, `bid_strategy`, `bid_cap`, `pacing_type`, `frequency_cap`,
`frequency_cap_time_unit`, `external_adset_id`, `external_ad_id`, `external_creative_id`,
`provenance`, `rationale`, and either `variants` or (`targeting` + `creative`).

### POST `/api/campaigns/forecast?ad_account_id=<uuid>`
Estimate delivery for an **unsaved** targeting spec. `platform` is required in the body;
`ad_account_id` is a query param.
```json
{ "platform": "meta", "objective": "sales", "daily_budget": 50, "currency": "USD", "targeting": {}, "bid_strategy": "lowest_cost", "bid_cap": 2.5, "start_date": "2026-04-01", "end_date": "2026-04-30" }
```
Response:
```json
{ "estimated_reach": 12000, "estimated_impressions": 45000, "estimated_spend": 48.5, "estimated_cpm": 1.08, "estimated_clicks": 900, "currency": "USD" }
```
UI: a "Estimate reach & spend" button in the builder, debounced on targeting/budget changes.

### GET `/api/campaigns/:id/health`
```json
{ "status": "healthy | warning | critical", "what": "...", "why": "...", "recommendation": "...",
  "recommended_action": { "type": "update_budget", "summary": "...", "status": "optional", "daily_budget": 60 } }
```
`recommended_action` is optional. If present, show a one-click "Apply" button.

### POST `/api/campaigns/:id/health/apply`
Executes the recommendation through the audited action engine. Returns the executed action
(same shape as the actions above). Errors: 400 `there is no recommended action to apply`.

Existing campaign operations (already live, keep working): `POST /api/campaigns/:id/launch`,
`/pause`, `/resume`, `PATCH /api/campaigns/:id` (`{ "daily_budget": 60 }`).

## 8. Acceptance criteria

- A typed API client covers every endpoint above with correct request/response types.
- Money is always handled in minor units and formatted per currency.
- Wallet page: balances, paginated ledger, Flutterwave top-up with return handling.
- Media library: upload (progress, 66 MB cap), list, pick, delete.
- Campaign builder is capability-driven and supports variants, bidding, pacing, frequency,
  rationale, forecast, and Oma propose/review.
- Zernio + native integrations connect via browser redirect and refresh account lists.
- `warnings` from campaign create and partial failures from review/auto-apply are surfaced.
- No auth header is sent; `credentials: "include"` is used everywhere; 401 triggers a
  single refresh-and-retry.
- TypeScript strict mode passes and there are no `any` types in API boundaries.
