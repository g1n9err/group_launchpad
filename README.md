# Group Launchpad

Centralized automation platform for launching student groups across **Slack**,
**Google Classroom**, **Google Drive**, and **Google Forms** — one form, one
launch action, every platform synchronized. See `Group_Launchpad_PRD.md` for
the full product requirements.

## Stack

| Plane     | Technology                                                                 |
| --------- | -------------------------------------------------------------------------- |
| Frontend  | React 19 + Vite + Tailwind v4, TanStack Query — deployed to Vercel (static) |
| Backend   | Go (echo), two services: `api` (REST) + `worker` (background jobs)          |
| Data      | PostgreSQL 17, sqlc (type-safe SQL), golang-migrate (versioned migrations)  |
| Jobs      | River (Postgres-backed queue) — wired on Day 2                              |
| Contract  | `openapi.yaml` — both sides generate types from it (`make gen`)             |

## Architecture

```
Browser ── Vercel (static SPA)  ──►  Go API container  ──►  PostgreSQL
   dev: Vite :5173 ──proxy /api──►   :8080 (compose or `make api`)   ▲
                                                                     │
                                              Go worker container ──┘
                                              (River queue + cron, Day 2)
```

```
backend/
├── cmd/{api,worker,migrate}     # entrypoints; cmd/api is the composition root
├── internal/
│   ├── core/                    # pure business logic — no HTTP, no SQL, no SDKs
│   │   └── groups/              # domain service + Repository port
│   ├── store/                   # sqlc-generated queries implementing core ports
│   ├── transport/               # echo handlers + generated types (oapi-codegen)
│   ├── integrations/            # Day 2: slack/, google/ — the ONLY external IO
│   ├── dbmigrate/               # embedded migration runner
│   └── config/                  # env configuration, fail-fast
└── migrations/                  # SQL migrations (embedded into every binary)
```

**Dependency rule** (enforced by `internal/` package visibility):
`transport → core ← store/integrations`; `core` imports nothing internal.
All wiring happens in `cmd/api/main.go`.

## Quickstart (local)

Prerequisites: Docker, Go 1.27+, Node (any recent LTS via fnm/nvm).

```bash
cp .env.example .env
make dev-up        # Postgres 17 in Docker (localhost:5432)
make api           # API on :8080 (migrates on boot)
make fe            # dashboard on :5173 (proxies /api → :8080)
```

Open http://localhost:5173 — create a group; it persists in Postgres and
appears in the list (Day 1 definition of done).

Full prod-parity stack in containers: `make full-up` (api + worker + Postgres).

## API

Interactive contract: see `openapi.yaml` (single source of truth).
Implemented endpoints:

| Method | Path                                   | Purpose                                |
| ------ | -------------------------------------- | -------------------------------------- |
| GET    | `/health`                              | liveness                               |
| GET    | `/readyz`                              | readiness (pings PostgreSQL)           |
| POST   | `/api/groups`                          | create group (→ 201/409/422)           |
| GET    | `/api/groups`                          | list groups (`limit`/`offset`)         |
| GET    | `/api/groups/{id}`                     | get one group (→ 200/404)              |
| GET    | `/api/integrations/google/connect`     | start Google OAuth (302 → consent)     |
| GET    | `/api/integrations/google/callback`    | OAuth callback (Google redirects here) |
| GET    | `/api/integrations/google/status`      | Google connection status (no tokens)   |

Error envelope (every error, every status):

```json
{ "error": { "code": "VALIDATION_ERROR", "message": "…", "fieldErrors": { "name": "is required" } } }
```

## Database

- Migrations live in `backend/migrations/` (golang-migrate layout, embedded in
  binaries). Schema covers the full PRD §7.2 entity set plus the idempotency
  registry (`group_resources`), `automation_jobs.idempotency_key`, and
  `external_operation_logs` — the foundation for safe re-execution (PRD §8).
- Apply manually: `make migrate-up` · roll back: `make migrate-down` ·
  version: `make migrate-version`. The API also migrates on boot when
  `MIGRATE_ON_BOOT=true` (dev default).
- Tests run against an isolated `launchpad_test` database (created
  automatically when the compose Postgres is up).

## Development workflow

```bash
make gen       # after editing openapi.yaml — regenerates Go + TS types
make test      # backend (go test -race, real Postgres) + frontend build
make psql      # quick database shell
```

Env vars: see `.env.example` (backend config, CORS allowlist, Day-2
integration placeholders — empty ⇒ adapter disabled; nothing is simulated).

## Status / roadmap

- **Day 1 (done):** foundation — schema, groups API, dashboard, tests,
  contract-first codegen, docker compose.
- **Day 2:** Slack/Google integrations behind core ports, launch workflow
  state machine, River jobs, idempotency keys.
- **Day 3:** daily sync audit (cron), announcements, notifications, CI/CD,
  production deployment.

Integration prerequisites (user-owned, needed before Day 2 live testing):
Slack workspace + app (bot token with `channels:write`, `chat:write`,
`users:read`, `conversations:*` scopes) and a Google Cloud project with
Classroom/Drive/Forms APIs enabled + OAuth credentials.
