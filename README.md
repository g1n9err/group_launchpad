# Group Launchpad

A multi-platform automation system that launches student groups across Slack, Google Classroom, Google Drive, and Google Forms — one form, one launch action, every platform synchronized. Built with Go (API + background worker), React (dashboard), and PostgreSQL.

## Requirements

| Tool | Version | Notes |
|---|---|---|
| **Go** | 1.27+ | Pinned in `backend/go.mod` (`go 1.27.4`) |
| **Node.js** | 20+ | Tested with v26.13.1; any modern LTS works |
| **npm** | 10+ | Ships with Node |
| **Docker** | recent | For PostgreSQL and prod-parity containers |
| **Docker Compose** | v2+ | Plugin form (`docker compose`, not `docker-compose`) |

PostgreSQL runs in Docker (`postgres:17-alpine`) — no separate install needed.

## Setup

### 1. Clone and configure

```bash
git clone <repository-url>
cd launchpad
cp .env.example .env
```

Edit `.env` — the minimum for the dashboard to boot:

```env
DATABASE_URL=postgres://launchpad:launchpad@localhost:5432/launchpad?sslmode=disable
```

Optional integrations (leave empty to disable — the API boots safely without them):

```env
SLACK_BOT_TOKEN=          # Bot token from your Slack App
SLACK_SIGNING_SECRET=     # Signing secret for Events API verification
GOOGLE_CLIENT_ID=         # OAuth 2.0 Client ID from Google Cloud Console
GOOGLE_CLIENT_SECRET=     # OAuth 2.0 Client Secret
GOOGLE_REDIRECT_URL=      # Must match the registered redirect URI exactly
TOKEN_ENCRYPTION_KEY=     # openssl rand -base64 32
```

Coordinator operations use HTTP Basic authentication with username `coordinator`.
Set `COORDINATOR_PASSWORD` to a random value (for example, generate it with
`openssl rand -hex 32`). This protects Slack launch, launch status, direct
Slack provisioning, Classroom provisioning, and the start of Google OAuth.
Without a password these routes return HTTP 503. For a browser-based OAuth
connection, open the Google Connect URL and enter the coordinator credentials
when prompted. Use HTTPS for coordinator traffic outside local development.

**Never commit `.env`** — it is git-ignored.

### 2. Start PostgreSQL

```bash
make dev-up
```

### 3. Run database migrations

Migrations apply automatically when the API or worker boots (`MIGRATE_ON_BOOT=true`, the default). Migration `0006` owns the River queue schema. Rolling it back removes queued jobs and job history. To apply or inspect migrations manually:

```bash
make migrate-up       # apply pending migrations
make migrate-version  # show current schema version
```

### 4. Start the backend

```bash
make api
```

The API listens on `http://localhost:8080`. Migrations run on boot. The dedicated Slack Events listener starts on `http://127.0.0.1:8081` (loopback only) when `SLACK_SIGNING_SECRET` is configured.

### 5. Start the frontend

```bash
make fe
```

Open `http://localhost:5173` — the Vite dev server proxies `/api` to `:8080`.

### Full stack in Docker (prod-parity)

```bash
make full-up    # PostgreSQL + API container + worker container
```

## Tests

### Backend (Go)

```bash
export TEST_DATABASE_URL='postgres://launchpad:launchpad@localhost:5432/postgres?sslmode=disable'
make test-backend           # all packages, -race enabled
cd backend && go test ./... # without race detector
```

Use a development PostgreSQL instance and a role with permission to create databases. The HTTP suite uses a dedicated `launchpad_test` database and truncates its test tables. Migration and River integration tests require `TEST_DATABASE_URL` and create and remove their own temporary databases. `make dev-up` starts PostgreSQL for local testing.

### Frontend

```bash
cd frontend && npm run build   # TypeScript type-check + Vite production build
```

### Both

```bash
make test
```

## Project structure

```
launchpad/
├── backend/
│   ├── cmd/
│   │   ├── api/          # API server (composition root — wires all layers)
│   │   ├── migrate/      # standalone migration command
│   │   └── worker/       # River background worker
│   ├── internal/
│   │   ├── core/         # pure business logic — no HTTP, no SQL, no SDKs
│   │   │   ├── groups/       # group CRUD, student ingestion, onboarding
│   │   │   └── integrations/ # Slack/Google ports, identity linker, events
│   │   ├── integrations/ # adapters: the ONLY code talking to Slack/Google
│   │   │   ├── google/       # OAuth flow, token exchange
│   │   │   └── slack/        # channels, invites, events, HMAC verification
│   │   ├── store/        # sqlc-generated queries implementing core ports
│   │   ├── transport/    # echo HTTP handlers (thin: parse → call → map)
│   │   ├── secrets/      # AES-256-GCM encryption for stored tokens
│   │   ├── config/       # env configuration, fail-fast
│   │   └── dbmigrate/    # embedded migration runner
│   └── migrations/       # SQL migrations (golang-migrate format, embedded)
├── frontend/
│   └── src/
│       ├── components/   # layout (sidebar/header), shared UI, icons
│       ├── i18n/         # translations: hy (default), en, ru
│       ├── lib/          # typed API client, TanStack Query hooks, router
│       └── pages/        # Overview, Groups, Students, Integrations, etc.
├── docker-compose.yml    # PostgreSQL (always) + API/worker (profile: full)
├── openapi.yaml          # API contract — both sides generate types from it
├── Group_Launchpad_PRD.md # Product Requirements Document
├── SYSTEMPROMPT.md        # Project instructions for the coding agent
├── .env.example           # Environment variable template (placeholders only)
└── Makefile              # dev commands: dev-up, api, fe, test, gen, migrate-*
```

**Architecture rule:** `transport → core ← store/integrations`. Core declares
interfaces (ports); store and integrations implement them; `cmd/api` wires
everything. Core never imports HTTP frameworks, SQL drivers, or external SDKs.

## Troubleshooting

### Docker permission denied

```text
permission denied while trying to connect to the Docker API
```

Your user isn't in the `docker` group in this shell. Fix: `sudo usermod -aG docker $USER`, then log out/in. Workaround for the current session: `newgrp docker`.

### Port 8080 already in use

```text
failed to bind host port 0.0.0.0:8080/tcp: address already in use
```

Another API instance is running. Stop it first (`Ctrl+C` in the terminal running `make api`, or `docker compose --profile full stop api`). The Makefile's `full-up` target pre-checks this and errors with a clear message.

### Database connection refused

```text
postgres unreachable after retries
```

PostgreSQL isn't running. Start it: `make dev-up`. If the volume is corrupt, reset everything: `docker compose down -v` (⚠️ deletes data), then `make dev-up`.

### Missing environment variables

The API fail-fasts on boot with `fatal: config: DATABASE_URL is required`. Integration warnings (`slack integration disabled`, `google oauth disabled`) are non-fatal — the API runs without them, but integration endpoints return `503 NOT_READY`.

### Google OAuth redirect mismatch

The `GOOGLE_REDIRECT_URL` must match the **Authorized redirect URI** in Google Cloud Console byte-for-byte (scheme, host, port, path). For local development:
`http://localhost:8080/api/integrations/google/callback`

### Slack Events not arriving

1. **Check the tunnel is alive** — quick tunnels (`cloudflared tunnel --url`) die silently after network changes. Restart it.
2. **Check Socket Mode is OFF** in your Slack App settings — it silently redirects all events to a WebSocket instead of your HTTP endpoint.
3. **Check the signing secret** — the events listener won't start without it.
4. **Check the bot is a member** of the channel — Slack only delivers `member_joined_channel` for channels the bot is in.

### Regenerating API types after contract changes

After editing `openapi.yaml`, regenerate types on both sides:

```bash
make gen
```
