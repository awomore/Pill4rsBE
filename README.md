# Pill4rs Backend

Go API for Pill4rs — a marketing analytics platform with an AI copilot ("Oma"),
Meta (Facebook) Ads integration, performance sync, and a dashboard.

## Tech stack

- **Go 1.25.3**, [Echo](https://echo.labstack.com/) HTTP framework
- **PostgreSQL** via [pgx](https://github.com/jackc/pgx) + [sqlc](https://sqlc.dev/) (typed query code)
- **golang-migrate** for schema migrations (auto-applied on boot)
- **Redis** (rate limiting / caching)
- **Anthropic Claude** for the Oma copilot
- **Meta Graph API** for ad-account OAuth, campaigns and insights

## Prerequisites

- [Docker](https://www.docker.com/) + Docker Compose (for the quick start)
- [Go 1.25.3+](https://go.dev/dl/) (for local development)
- [sqlc](https://docs.sqlc.dev/en/latest/overview/install.html) (only if you change SQL queries or migrations)
- [golang-migrate CLI](https://github.com/golang-migrate/migrate) (optional — migrations also run automatically on startup)

## Environment variables

Copy the template and fill it in:

```bash
cp .env.example .env
```

| Variable | Required | Description |
| --- | --- | --- |
| `PORT` | yes | Port the API listens on (e.g. `8080`). |
| `FRONTEND_ORIGIN` | yes | Browser origin of the frontend (e.g. `http://localhost:3000`). Used for **CORS** and as the post-OAuth redirect target. |
| `DATABASE_URL` | yes | PostgreSQL connection string. |
| `JWT_SECRET` | yes | Long random secret for signing JWT access tokens. |
| `ANTHROPIC_API_KEY` | yes | Anthropic API key powering the Oma copilot. |
| `TOKEN_ENCRYPTION_KEY` | yes | Key used to AES-256-GCM encrypt stored ad-platform tokens. A 32-byte value is used as-is; anything else is SHA-256 derived to 32 bytes. |
| `META_APP_ID` | yes | Meta (Facebook) app ID. |
| `META_APP_SECRET` | yes | Meta app secret. |
| `META_REDIRECT_URI` | yes | OAuth callback. **Must point at this backend**, e.g. `http://localhost:8080/api/integrations/meta/callback` — not the frontend. |
| `META_BUSINESS_ID` | no | Pill4rs Business Manager ID. Enables managed Meta ad-account creation. |
| `META_MANAGED_ACCESS_TOKEN` | no | System-user token for the Pill4rs Business Manager. |
| `REDIS_URL` | no | Redis connection string. |
| `GOOGLE_CLIENT_ID` | no | Google OAuth login client ID. |
| `GOOGLE_CLIENT_SECRET` | no | Google OAuth login client secret. |
| `GOOGLE_REDIRECT_URI` | no | Google OAuth callback (defaults to `http://localhost:<PORT>/api/auth/google/callback`). |
| `TIKTOK_APP_ID` | no | TikTok Business app id (enables the TikTok integration). |
| `TIKTOK_APP_SECRET` | no | TikTok Business app secret. |
| `TIKTOK_REDIRECT_URI` | no | TikTok OAuth callback, e.g. `http://localhost:8080/api/integrations/tiktok/callback`. |
| `TIKTOK_BUSINESS_CENTER_ID` | no | Pill4rs Business Center ID. Enables managed TikTok advertiser creation. |
| `TIKTOK_MANAGED_ACCESS_TOKEN` | no | Access token for the Pill4rs Business Center. |
| `GOOGLE_ADS_DEVELOPER_TOKEN` | no | Google Ads API developer token (required to enable the Google Ads integration; reuses `GOOGLE_CLIENT_ID`/`SECRET` for OAuth). |
| `GOOGLE_ADS_LOGIN_CUSTOMER_ID` | no | Google Ads manager (MCC) customer id, if applicable. |
| `GOOGLE_ADS_MCC_REFRESH_TOKEN` | no | Pill4rs-owned MCC refresh token. Enables managed Google Ads customer creation. |
| `GOOGLE_ADS_REDIRECT_URI` | no | Google Ads OAuth callback, e.g. `http://localhost:8080/api/integrations/google/callback`. |
| `DV360_PARTNER_ID` | no | Pill4rs DSP partner ID. Enables managed programmatic advertiser creation. |
| `DV360_BILLING_PROFILE_ID` | no | Billing profile under the Pill4rs DSP partner. |
| `DV360_MANAGED_REFRESH_TOKEN` | no | Pill4rs-owned DSP partner refresh token. |
| `PROGRAMMATIC_REDIRECT_URI` | no | Programmatic DSP OAuth callback, e.g. `http://localhost:8080/api/integrations/programmatic/callback`. Reuses `GOOGLE_CLIENT_ID`/`GOOGLE_CLIENT_SECRET`; scopes are configured on the OAuth consent screen. |
| `STORAGE_DRIVER` | no | Media storage driver. `local` (default) writes to `MEDIA_DIR`; an S3/R2 adapter plugs in behind the same interface. |
| `MEDIA_DIR` | no | Local directory for uploaded creative media (default `media`). |
| `MEDIA_PUBLIC_BASE_URL` | no | Public base URL used to build media URLs for ad platforms (default `http://localhost:<PORT>`). |
| `COMMISSION_RATE_BPS` | no | Commission charged on tracked ad spend, in basis points (default `1000` = 10%). |
| `PILL4RS_ADMIN_EMAILS` | no | Comma-separated emails bootstrapped as Pill4rs platform admins. |
| `FX_NGN_PER_USD` | no | NGN per USD used to convert wallet top-ups for managed-platform caps/budgets. `0` disables conversion. |
| `FLUTTERWAVE_SECRET_KEY` | no | Flutterwave secret key. Without it, `POST /api/wallet/checkout` is disabled. |
| `FLUTTERWAVE_WEBHOOK_SECRET_HASH` | no | Secret hash configured for the Flutterwave webhook (`verif-hash`). |
| `FLUTTERWAVE_BASE_URL` | no | Flutterwave API base URL (default `https://api.flutterwave.com/v3`). |
| `ZERNIO_API_KEY` | no | Zernio API key. Enables ads on LinkedIn, Pinterest, X and OpenAI Ads (and Zernio-managed Meta/Google/TikTok). |
| `ZERNIO_BASE_URL` | no | Zernio API base URL (default `https://zernio.com/api`). |

The server refuses to start if any required variable is missing.

## Quick start (Docker Compose)

`docker-compose up` builds the API image and starts Postgres, Redis, and the API.
Compose reads secrets from your `.env` (created above) and overrides
`DATABASE_URL` / `REDIS_URL` to the in-network service hostnames.

```bash
cp .env.example .env   # then edit .env with your real keys
docker-compose up --build
```

The API is then available at `http://localhost:${PORT}` (health check at `/health`).
Migrations are applied automatically on startup.

To run only the infrastructure (and run the server yourself, see below):

```bash
docker-compose up postgres redis
```

## Database migrations

Migrations live in `migrations/` (`golang-migrate` format) and are **applied
automatically every time the server boots**, so you normally don't run them by hand.

To apply them manually with the CLI:

```bash
migrate -path migrations -database "$DATABASE_URL" up
# roll back one step:
migrate -path migrations -database "$DATABASE_URL" down 1
```

## Generating query code (sqlc)

Typed DB code in `internal/db/` is generated by sqlc from `internal/queries/*.sql`
and the migration schema (config in `sqlc.yaml`). `internal/db/` is git-ignored, so
**after cloning or after changing any query/migration you must regenerate it**:

```bash
sqlc generate
```

## Running the server (local development)

```bash
cp .env.example .env            # configure DATABASE_URL=localhost, keys, etc.
docker-compose up postgres redis  # start infra
sqlc generate                   # if internal/db/ is missing
go run ./cmd/api
```

Other useful commands:

```bash
go build ./...     # compile
go test ./...      # run tests
go vet ./...       # static checks
```

The process handles `SIGINT`/`SIGTERM` and shuts the HTTP server down gracefully
(10s drain window).

## Meta (Facebook) app setup

1. Go to <https://developers.facebook.com/> → **My Apps** → **Create App** (type: *Business*).
2. Add the **Marketing API** product (and **Facebook Login** for the OAuth dialog).
3. From **App settings → Basic**, copy the **App ID** → `META_APP_ID` and **App Secret** → `META_APP_SECRET`.
4. Under **Facebook Login → Settings**, add your **Valid OAuth Redirect URI**. It must
   exactly match `META_REDIRECT_URI` (this backend's callback,
   e.g. `http://localhost:8080/api/integrations/meta/callback`).
5. The connect flow requests the `ads_read` and `ads_management` scopes. While the app
   is in **Development mode**, add yourself (and any testers) under **App Roles**
   (Admin/Developer/Tester) so those scopes work without App Review.
6. The Meta user you connect with must have access to **at least one ad account** with
   an active campaign for sync and the dashboard to return data.

Connect flow: the frontend calls `GET /api/integrations/meta/connect` to get the OAuth
URL, the user authorizes, Meta redirects to `META_REDIRECT_URI`
(`/api/integrations/meta/callback`), and the backend then redirects the browser to
`${FRONTEND_ORIGIN}/settings/integrations?connected=meta`.

## Programmatic DSP

The programmatic DSP is the buying backbone for display and video. It reuses the
shared OAuth client (`GOOGLE_CLIENT_ID`/`SECRET`), with scopes configured on the
OAuth consent screen; register `PROGRAMMATIC_REDIRECT_URI` on that client.

Agencies can provision a managed advertiser under the Pill4rs DSP partner with
`POST /api/integrations/programmatic/managed-advertiser` (or the v1 equivalent).
Each connected DSP **advertiser** becomes an `ad_account` (`platform: "programmatic"`).
Campaigns are pushed as the native hierarchy:

| Pill4rs field | DSP object |
| --- | --- |
| `external_campaign_id` | campaign |
| `external_ad_id` | insertion order |
| `external_adset_id` | line item (budget/bid/pacing) |
| `external_creative_id` | creative |

Creation always builds the tree paused, and is idempotent so a failed launch can
resume. Image and video creatives are uploaded as DSP assets, created as hosted
creatives, and attached to the line item. Line-item targeting supports geo, age,
gender, device, dayparting, first-party/partner audiences, interest audiences,
combined audiences, custom lists, and lookalikes; assignment is idempotent.
Pass `custom_bidding_algorithm_id` in targeting to assign an existing DSP
custom bidding algorithm. The `/api/v1/programmatic` endpoints can also list audiences,
create lookalikes, create script/rule-based custom bidding algorithms, and upload
their scripts/rules. Reach forecasting is available through
`POST /api/campaigns/forecast` when the advertiser is allowlisted for the DSP
forecast beta. Report media downloads fall back to the report's storage object
when the media endpoint is unavailable. The DSP is **track-only**:
the DSP invoices the advertiser directly, so the prepaid wallet guard does not
block or hard-pause programmatic campaigns, and sync does not debit wallet commission
for them. Reporting runs through the DSP's reporting API. Sync also imports each
campaign's first line item + insertion order IDs, so campaigns created directly
in the DSP can be paused/resumed and budget-managed from Pill4rs. This DSP has no
status webhooks for this scope; the regular sync poll reflects status changes.

## Agencies and clients

Every workspace belongs to an agency. Existing single-user workspaces are
backfilled into a personal agency; new signups create one automatically. Agency
members can switch between client workspaces without re-authenticating.

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/api/agency` | Current agency + the caller's role. |
| `GET` | `/api/agency/summary?range=7d|30d|90d` | Consolidated spend, campaigns, wallet balances, commissions, and per-client breakdown. |
| `GET` / `POST` | `/api/agency/clients` | List / create client workspaces (owner/admin only for create). |
| `POST` | `/api/agency/clients/:id/switch` | Issue a new access cookie scoped to that client. |
| `GET` / `POST` | `/api/agency/members` | List / add members by email (owner/admin only for add). |
| `PATCH` / `DELETE` | `/api/agency/members/:user_id` | Change role / remove a member. |

Roles: `owner`, `admin`, `manager`, `viewer`. Agency management endpoints
require a user session, not an API key. The summary rolls up performance and
wallet/commission totals across every client workspace, including available and
held balances.

## Managed Google Ads accounts

When `GOOGLE_ADS_MCC_REFRESH_TOKEN`, `GOOGLE_ADS_LOGIN_CUSTOMER_ID`, and
the developer token are configured, agencies can create customer accounts under
the Pill4rs MCC with `POST /api/integrations/google/managed-account` (or the
v1 equivalent). The new account is stored as `billing_mode: "managed"`,
`payer: "pill4rs"`, and funded by the workspace wallet. Google must enable
customer creation for the MCC (accounts must have sufficient spend history).
Managed accounts also get an `AccountBudgetProposal` hard cap from lifetime
top-ups; Google reviews proposals asynchronously, so the response reports
`spend_cap_proposed` and the budget may not be active immediately. Wallet
credits refresh the proposal.

Meta managed accounts are created with `POST /api/integrations/meta/managed-account`
when `META_BUSINESS_ID` and `META_MANAGED_ACCESS_TOKEN` are configured. The
account starts with `billing_mode: "managed"` and a `spend_cap` equal to lifetime
wallet top-ups; top-ups automatically refresh the cap. Meta requires a numeric
`timezone_id` and a Business Manager with ad-account creation enabled.

TikTok managed advertisers are created with
`POST /api/integrations/tiktok/managed-account` when
`TIKTOK_BUSINESS_CENTER_ID` and `TIKTOK_MANAGED_ACCESS_TOKEN` are configured.
The account-level budget is set from lifetime top-ups and refreshed on wallet
credits.

## Prepaid wallet and holds

Wallets fund **managed** ad accounts; account owners who bring their own billing
(`byob`) are tracked but not gated by the wallet. When a managed campaign is
activated, Pill4rs reserves `daily_budget x 2` in the wallet (`hold` ledger
entry). Pausing releases the remainder (`release`). During sync, held funds are
consumed against actual spend, then commission and media spend are recorded.
Holds are idempotent per campaign and are stored in `wallet_holds`. Wallet
credits also refresh managed Meta spend caps, TikTok advertiser budgets, and
Google account-budget proposals. When
a wallet and managed account use different currencies, `FX_NGN_PER_USD` converts
lifetime top-ups; leave it at `0` to only count same-currency top-ups.

## Pill4rs platform admin

Platform admins sit above agencies and have full end-to-end control. They are
bootstrapped from `PILL4RS_ADMIN_EMAILS` and can:

- See global users, agencies, workspaces, campaigns, ad accounts, and wallet totals.
- Switch into any workspace; the new session keeps that workspace on refresh.
- Manage any agency (platform admins bypass agency membership checks).
- Grant/revoke platform admin access.
- Manually credit any wallet.
- Review an immutable audit trail of admin actions.

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/api/admin/overview` | Global counts and wallet totals. |
| `GET` | `/api/admin/agencies` | Every agency with owner, clients, and members. |
| `GET` | `/api/admin/users` | Every user and their agency/workspace footprint. |
| `GET` / `POST` | `/api/admin/admins` | List / grant platform admins. |
| `DELETE` | `/api/admin/admins/:user_id` | Revoke platform admin. |
| `POST` | `/api/admin/workspaces/:id/switch` | Issue an access cookie for any workspace. |
| `POST` | `/api/admin/workspaces/:id/topup` | Manually credit a workspace wallet. |
| `GET` | `/api/admin/audit-logs` | Admin action audit trail. |

Admin endpoints require a user session, not an API key.

## Support tickets

Clients and agency members can open support tickets from their active client
workspace. Pill4rs admins get a support console with assignment, status/priority
management, public replies, and internal notes.

| Method | Path | Description |
| --- | --- | --- |
| `POST` / `GET` | `/api/support/tickets` | Create / list the active workspace's tickets. |
| `GET` | `/api/support/tickets/:id` | Ticket + visible messages. |
| `POST` | `/api/support/tickets/:id/messages` | Reply to a ticket. |
| `GET` | `/api/admin/support/tickets` | Admin console list (`status`, `priority` filters). |
| `GET` | `/api/admin/support/tickets/:id` | Admin ticket + all messages, including internal notes. |
| `POST` | `/api/admin/support/tickets/:id/messages` | Admin reply or internal note. |
| `PATCH` | `/api/admin/support/tickets/:id` | Update status/priority and assign to self. |
| `GET` | `/api/admin/support/summary` | Counts by ticket status. |

Support endpoints require a user session, not an API key. Admin replies and
status changes are written to the admin audit log.

## Agency DSP API access

Pill4rs is the DSP partner of record. Marketing agencies use the Pill4rs API to
run programmatic campaigns for their clients end to end:

1. Create or select a client workspace.
2. Provision an advertiser under the Pill4rs partner.
3. Fund the client wallet (or connect BYOB accounts).
4. Use `/api/v1/programmatic` and `/api/v1/campaigns` with an agency API key and
   `X-Client-Id` to automate audiences, bidding, creatives, line items, and
   reporting.

This keeps agencies out of the underlying DSP account setup and billing while
giving them a single API surface across social and programmatic.

## Public API for marketing brands

External brands can automate campaigns with workspace API keys. Manage keys with
a UI session:

| Method | Path | Description |
| --- | --- | --- |
| `GET` / `POST` | `/api/api-keys` | List / create keys. Create returns the plaintext key once. |
| `DELETE` | `/api/api-keys/:id` | Revoke a key. |

Send the key as `Authorization: Bearer p4_live_...` or `X-API-Key`. New keys
created inside an agency client are agency-scoped: add
`X-Client-Id: <client_workspace_id>` to act on another client, or omit it to act
on the client where the key was created. The stable automation surface is under
`/api/v1`:

| Method | Path | Scope |
| --- | --- | --- |
| `GET` | `/api/v1/accounts` | `accounts:read` |
| `POST` | `/api/v1/google/managed-accounts` | `accounts:write` |
| `POST` | `/api/v1/meta/managed-accounts` | `accounts:write` |
| `POST` | `/api/v1/tiktok/managed-accounts` | `accounts:write` |
| `POST` | `/api/v1/programmatic/managed-advertisers` | `accounts:write` |
| `GET` | `/api/v1/campaigns` | `campaigns:read` |
| `POST` | `/api/v1/campaigns` | `campaigns:write` |
| `POST` | `/api/v1/campaigns/forecast` | `campaigns:read` |
| `POST` | `/api/v1/campaigns/:id/launch` · `/pause` · `/resume` | `campaigns:write` |
| `PATCH` | `/api/v1/campaigns/:id` | `campaigns:write` |
| `GET` | `/api/v1/campaigns/:id/health` | `campaigns:read` |
| `POST` | `/api/v1/sync` | `campaigns:write` |
| `GET` | `/api/v1/reports/summary` | `reports:read` |
| `GET` | `/api/v1/programmatic/audiences?advertiser_id=...&type=...` | `audiences:read` |
| `POST` | `/api/v1/programmatic/lookalike-audiences` | `audiences:write` |
| `GET` | `/api/v1/programmatic/custom-bidding-algorithms?advertiser_id=...` | `bidding:read` |
| `POST` | `/api/v1/programmatic/custom-bidding-algorithms` | `bidding:write` |
| `GET` / `POST` | `/api/v1/programmatic/custom-bidding-algorithms/:id/scripts` | `bidding:read` / `bidding:write` |
| `GET` / `POST` | `/api/v1/programmatic/custom-bidding-algorithms/:id/rules` | `bidding:read` / `bidding:write` |

Scopes: `campaigns:read`, `campaigns:write`, `accounts:read`, `reports:read`,
or `*`. Default new keys get the first four.

### Idempotent retries

Send `Idempotency-Key: <unique-value>` on any mutating `/api/v1` request. The
first response is stored for 24h and replayed for retries with the same method,
path, and body. Reusing a key with a different body returns `422`; a concurrent
request with the same key returns `409`. Replayed responses include
`Idempotency-Replayed: true`.

## Anthropic API key setup

1. Go to <https://console.anthropic.com/> → **API Keys** → **Create Key**.
2. Set the value as `ANTHROPIC_API_KEY` in your `.env`.
3. The copilot streams responses from the Claude model configured in
   `internal/ai/client.go`.

## API overview

All errors are returned as `{ "error": "message" }`. Authenticated routes require the
`access_token` cookie (set by the auth endpoints).

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/health` | Liveness check. |
| `POST` | `/api/auth/signup` · `/login` · `/refresh` · `/logout` | Email/password auth. |
| `GET` | `/api/auth/google` · `/api/auth/google/callback` | Google OAuth login. |
| `GET` / `PATCH` | `/api/workspace` | Read / update the workspace profile. |
| `GET` | `/api/user` | Signed-in user profile + platform-admin flag. |
| `POST` | `/api/user/password` | Change password with current-password verification. |
| `GET` | `/api/user/sessions` | List refresh sessions. |
| `DELETE` | `/api/user/sessions/:id` | Revoke a refresh session. |
| `GET` | `/api/user/workspaces` | Every workspace accessible to the user. |
| `POST` | `/api/ai/chat` | SSE chat with Oma (`mode`: `copilot` (default) or `onboarding`). |
| `GET` | `/api/integrations` | List connected ad accounts (a business can connect many, including several per platform). |
| `GET` | `/api/integrations/meta/connect` · `/callback` | Meta OAuth connect flow. |
| `POST` | `/api/integrations/meta/managed-account` | Create a managed Meta ad account under the Pill4rs Business Manager. |
| `GET` | `/api/integrations/tiktok/connect` · `/callback` | TikTok OAuth connect flow. |
| `POST` | `/api/integrations/tiktok/managed-account` | Create a managed TikTok advertiser under the Pill4rs Business Center. |
| `GET` | `/api/integrations/google/connect` · `/callback` | Google Ads OAuth connect flow. |
| `POST` | `/api/integrations/google/managed-account` | Create a managed Google Ads customer under the Pill4rs MCC and attach it to the workspace. |
| `GET` | `/api/integrations/programmatic/connect` · `/start` · `/callback` | DSP OAuth connect flow; `connect` returns a same-origin start URL, `start` redirects to the provider, and the callback discovers every accessible advertiser. |
| `POST` | `/api/integrations/programmatic/sync` | Re-sync programmatic advertisers into the workspace. |
| `POST` | `/api/integrations/programmatic/managed-advertiser` | Create a DSP advertiser under the Pill4rs partner and attach it to the workspace. |
| `GET` | `/api/integrations/zernio/connect?platform=linkedin` | Start Zernio ads OAuth for a network; returns the auth URL. |
| `GET` | `/api/integrations/zernio/callback` · `/callback/:profile_id` | Zernio OAuth callback; syncs the connected ad accounts. |
| `POST` | `/api/integrations/zernio/sync` | Re-sync Zernio accounts into the workspace. |
| `GET` | `/api/integrations/accounts/:id/options` | A Meta account's pages + pixels (for ad delivery). |
| `PATCH` | `/api/integrations/accounts/:id` | Set a Meta account's `page_id` / `pixel_id`. |
| `DELETE` | `/api/integrations/accounts/:id` | Disconnect a single connected ad account. |
| `POST` | `/api/sync/trigger?since=YYYY-MM-DD&until=YYYY-MM-DD` | Sync campaigns + insights; defaults to the last 30 days, supports up to 366-day backfills (stands in for the 6h cron). |
| `GET` | `/api/campaigns` | Campaigns with their latest snapshot. |
| `POST` | `/api/campaigns` | Create a campaign on the selected accounts (`ad_account_ids`); builds the full tree (campaign → ad set → creative → ad, all `PAUSED`); unreachable accounts are saved as local drafts. |
| `POST` | `/api/campaigns/:id/launch` | Resume building a draft's full delivery tree and flip it to `PAUSED`. |
| `POST` | `/api/campaigns/:id/pause` · `/resume` | Pause or resume a live campaign (platform + local). |
| `PATCH` | `/api/campaigns/:id` | Change a campaign's daily budget. |
| `GET` | `/api/campaigns/:id/health` | Oma's health card: what / why / recommendation (+ a one-click fix if any). |
| `POST` | `/api/campaigns/:id/health/apply` | Apply the recommended fix (audited Oma action via the approval engine). |
| `POST` | `/api/ai/campaign/propose` | Oma turns a brief + the picked `ad_account_ids` into a campaign **proposal** (pending action) to review. |
| `POST` | `/api/ai/review` | Oma reviews every campaign across all platforms; queues recommended actions (`propose:false` for read-only, `auto_apply:true` to execute). |
| `GET` | `/api/actions` | The propose → review → approve queue (with outcomes). |
| `POST` | `/api/actions/:id/approve` · `/reject` | Approve (executes via the shared campaign engine) or reject a proposal. |
| `GET` | `/api/dashboard/summary?range=7d\|30d\|90d` | Aggregated performance, daily series, per-campaign breakdown. |
| `GET` | `/api/wallet` | Wallet status and USD/NGN balances (`balance_minor` = available, `held_minor` = reserved, `cash_minor` = available + held). |
| `GET` | `/api/wallet/transactions` | Wallet ledger (top-ups, holds, releases, spend, commission), newest first. |
| `GET` | `/api/wallet/holds` | Active campaign budget holds. |
| `POST` | `/api/wallet/topup` | Credit the wallet from a provider reference (idempotent). |
| `POST` | `/api/wallet/checkout` | Create a Flutterwave payment link for a wallet top-up. |
| `POST` | `/api/webhooks/flutterwave` | Flutterwave webhook; verifies and credits the wallet on successful payment (unauthenticated, `verif-hash` verified). |
| `GET` / `POST` | `/api/media` | List / upload creative images and videos (multipart `file`). |
| `DELETE` | `/api/media/:id` | Delete a media asset. |
| `PATCH` | `/api/integrations/accounts/:id/billing` | Set an account's billing mode (`byob`\|`managed`), payer, currency, and spend limit. |
| `GET` | `/api/platforms` | Ad-platform matrix (availability, capabilities) plus this workspace's connection state. |
| `GET` | `/api/platforms/capabilities` | Static per-platform capability matrix. |
