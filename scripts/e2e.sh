#!/usr/bin/env bash
# Browser E2E orchestrator — DISPOSABLE environment only:
#   * a throwaway PostgreSQL container on a random private port,
#   * the API binary on 127.0.0.1:18090 (migrations run on the throwaway DB),
#   * the production SPA build served by `vite preview` on 127.0.0.1:4199,
#   * Playwright driving real Chromium against the real login/RBAC flow.
# NO real Slack/Google credentials: integrations stay disabled (503), no
# external service is contacted. The developer database (launchpad) is never
# touched. Cleanup always runs (containers, processes, temp dir).
set -euo pipefail

# Repo root captured BEFORE any cd (the trap runs after the script has cd'd).
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

PROJECT="launchpad-e2e-$$"
PG_PORT="55432"
API_ADDR="127.0.0.1:18090"
WEB_PORT="4199"
API_URL="http://${API_ADDR}"
BASE_URL="http://127.0.0.1:${WEB_PORT}"
WORKDIR="$(mktemp -d "${TMPDIR:-/tmp}/launchpad-e2e.XXXXXX")"

cleanup() {
  local code=$?
  set +e
  [ -n "${API_PID:-}" ] && kill "${API_PID}" 2>/dev/null
  [ -n "${WEB_PID:-}" ] && kill "${WEB_PID}" 2>/dev/null
  docker rm -f "$PROJECT" >/dev/null 2>&1
  if [ "$code" -ne 0 ]; then
    # Keep the failure logs for diagnosis (git-ignored path).
    LOGDIR="$REPO_ROOT/frontend/test-results/e2e-logs"
    mkdir -p "$LOGDIR"
    cp "$WORKDIR"/api.log "$WORKDIR"/web.log "$LOGDIR"/ 2>/dev/null
    echo "e2e failed (exit $code); logs kept in $LOGDIR" >&2
  fi
  rm -rf "$WORKDIR"
  exit $code
}
trap cleanup EXIT INT TERM

# Require the newgrp/docker group; the rest of the Makefile assumes it too.
if ! docker info >/dev/null; then
  echo "ERROR: a reachable Docker daemon is required for the disposable E2E database" >&2
  exit 1
fi

command -v nc >/dev/null || { echo "ERROR: nc (netcat) required"; exit 1; }

echo "==> disposable PostgreSQL ($PROJECT on 127.0.0.1:$PG_PORT)"
docker run -d --name "$PROJECT" -e POSTGRES_USER=e2e -e POSTGRES_PASSWORD=e2e \
  -e POSTGRES_DB=e2e -p "127.0.0.1:$PG_PORT:5432" postgres:17-alpine >/dev/null
for _ in $(seq 1 60); do
  nc -z 127.0.0.1 "$PG_PORT" >/dev/null 2>&1 && docker exec "$PROJECT" pg_isready -U e2e >/dev/null 2>&1 && break
  sleep 0.5
done

echo "==> building API + SPA"
(cd backend && go build -o "$WORKDIR/api" ./cmd/api)
(cd frontend && VITE_API_URL="$API_URL" npm run build >/dev/null)

echo "==> starting API on $API_URL"
ADMIN_EMAIL="e2e-admin@launchpad.test"
ADMIN_PASSWORD="e2e-admin-password-123"
(
  cd backend
  # Neutralize any REAL integration credentials exported from the
  # developer's .env by `make` — the E2E stack must run with integrations
  # disabled (503) and bind only its own ports. Prefix assignments override
  # inherited exports.
  DATABASE_URL="postgres://e2e:e2e@127.0.0.1:$PG_PORT/e2e?sslmode=disable" \
  PORT="18090" LOG_LEVEL=warn MIGRATE_ON_BOOT=true \
  COORDINATOR_PASSWORD="e2e-coordinator-basic-0123456789abcdef" \
  CORS_ALLOWED_ORIGINS="$BASE_URL" \
  ADMIN_EMAIL="$ADMIN_EMAIL" ADMIN_INITIAL_PASSWORD="$ADMIN_PASSWORD" \
  AUTH_SESSION_TTL=2h \
  SLACK_BOT_TOKEN="" SLACK_SIGNING_SECRET="" SLACK_EVENTS_ADDR="127.0.0.1:18081" \
  GOOGLE_CLIENT_ID="" GOOGLE_CLIENT_SECRET="" GOOGLE_REDIRECT_URL="" TOKEN_ENCRYPTION_KEY="" \
  SMTP_ENABLED=false GMAIL_ENABLED=false SMTP_HOST="" SMTP_PORT="" SMTP_USERNAME="" SMTP_PASSWORD="" SMTP_FROM="" \
  "$WORKDIR/api" >"$WORKDIR/api.log" 2>&1 &
  echo $! >"$WORKDIR/api.pid"
)
API_PID="$(cat "$WORKDIR/api.pid")"
for _ in $(seq 1 60); do
  nc -z 127.0.0.1 18090 >/dev/null 2>&1 && break
  sleep 0.5
done

echo "==> serving SPA (vite preview :$WEB_PORT)"
(
  cd frontend
  npx vite preview --host 127.0.0.1 --port "$WEB_PORT" --strictPort >"$WORKDIR/web.log" 2>&1 &
  echo $! >"$WORKDIR/web.pid"
)
WEB_PID="$(cat "$WORKDIR/web.pid")"
for _ in $(seq 1 60); do
  nc -z 127.0.0.1 "$WEB_PORT" >/dev/null 2>&1 && break
  sleep 0.5
done

echo "==> Playwright"
cd frontend
E2E_API_URL="$API_URL" E2E_BASE_URL="$BASE_URL" \
E2E_ADMIN_EMAIL="$ADMIN_EMAIL" E2E_ADMIN_PASSWORD="$ADMIN_PASSWORD" \
E2E_COORDINATOR_EMAIL="e2e-coordinator@launchpad.test" E2E_COORDINATOR_PASSWORD="e2e-coord-password-123" \
E2E_LECTURER_EMAIL="e2e-lecturer@launchpad.test" E2E_LECTURER_PASSWORD="e2e-lecturer-password-1" \
E2E_STUDENT_EMAIL="e2e-student@launchpad.test" E2E_STUDENT_PASSWORD="e2e-student-password-1" \
  npx playwright test
