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
| `REDIS_URL` | no | Redis connection string. |
| `GOOGLE_CLIENT_ID` | no | Google OAuth login client ID. |
| `GOOGLE_CLIENT_SECRET` | no | Google OAuth login client secret. |
| `GOOGLE_REDIRECT_URI` | no | Google OAuth callback (defaults to `http://localhost:<PORT>/api/auth/google/callback`). |
| `TIKTOK_APP_ID` | no | TikTok Business app id (enables the TikTok integration). |
| `TIKTOK_APP_SECRET` | no | TikTok Business app secret. |
| `TIKTOK_REDIRECT_URI` | no | TikTok OAuth callback, e.g. `http://localhost:8080/api/integrations/tiktok/callback`. |
| `GOOGLE_ADS_DEVELOPER_TOKEN` | no | Google Ads API developer token (required to enable the Google Ads integration; reuses `GOOGLE_CLIENT_ID`/`SECRET` for OAuth). |
| `GOOGLE_ADS_LOGIN_CUSTOMER_ID` | no | Google Ads manager (MCC) customer id, if applicable. |
| `GOOGLE_ADS_REDIRECT_URI` | no | Google Ads OAuth callback, e.g. `http://localhost:8080/api/integrations/google/callback`. |
| `STORAGE_DRIVER` | no | Media storage driver. `local` (default) writes to `MEDIA_DIR`; an S3/R2 adapter plugs in behind the same interface. |
| `MEDIA_DIR` | no | Local directory for uploaded creative media (default `media`). |
| `MEDIA_PUBLIC_BASE_URL` | no | Public base URL used to build media URLs for ad platforms (default `http://localhost:<PORT>`). |
| `COMMISSION_RATE_BPS` | no | Commission charged on tracked ad spend, in basis points (default `1000` = 10%). |
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
| `POST` | `/api/ai/chat` | SSE chat with Oma (`mode`: `copilot` (default) or `onboarding`). |
| `GET` | `/api/integrations` | List connected ad accounts (a business can connect many, including several per platform). |
| `GET` | `/api/integrations/meta/connect` · `/callback` | Meta OAuth connect flow. |
| `GET` | `/api/integrations/tiktok/connect` · `/callback` | TikTok OAuth connect flow. |
| `GET` | `/api/integrations/google/connect` · `/callback` | Google Ads OAuth connect flow. |
| `GET` | `/api/integrations/zernio/connect?platform=linkedin` | Start Zernio ads OAuth for a network; returns the auth URL. |
| `GET` | `/api/integrations/zernio/callback` · `/callback/:profile_id` | Zernio OAuth callback; syncs the connected ad accounts. |
| `POST` | `/api/integrations/zernio/sync` | Re-sync Zernio accounts into the workspace. |
| `GET` | `/api/integrations/accounts/:id/options` | A Meta account's pages + pixels (for ad delivery). |
| `PATCH` | `/api/integrations/accounts/:id` | Set a Meta account's `page_id` / `pixel_id`. |
| `DELETE` | `/api/integrations/accounts/:id` | Disconnect a single connected ad account. |
| `POST` | `/api/sync/trigger` | Sync campaigns + last-30-day insights (stands in for the 6h cron). |
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
| `GET` | `/api/wallet` | Wallet status and USD/NGN balances. |
| `GET` | `/api/wallet/transactions` | Wallet ledger (top-ups, spend, commission), newest first. |
| `POST` | `/api/wallet/topup` | Credit the wallet from a provider reference (idempotent). |
| `POST` | `/api/wallet/checkout` | Create a Flutterwave payment link for a wallet top-up. |
| `POST` | `/api/webhooks/flutterwave` | Flutterwave webhook; verifies and credits the wallet on successful payment (unauthenticated, `verif-hash` verified). |
| `GET` / `POST` | `/api/media` | List / upload creative images and videos (multipart `file`). |
| `DELETE` | `/api/media/:id` | Delete a media asset. |
| `PATCH` | `/api/integrations/accounts/:id/billing` | Set an account's billing mode (`byob`\|`managed`), payer, currency, and spend limit. |
| `GET` | `/api/platforms` | Ad-platform matrix (availability, capabilities) plus this workspace's connection state. |
| `GET` | `/api/platforms/capabilities` | Static per-platform capability matrix. |
