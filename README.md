<p align="center">
  <img src="frontend/public/favicon.png" alt="" width="64" height="64">
</p>

<h1 align="center">Group Launchpad</h1>

<p align="center">
  Create student cohorts, provision their Slack and Google resources, and track onboarding from one dashboard.
</p>

<p align="center">
  <a href="https://g-launchpad.duckdns.org/">Open Launchpad</a> ·
  <a href="#quickstart">Run locally</a> ·
  <a href="#using-launchpad">Usage</a> ·
  <a href="openapi.yaml">API reference</a>
</p>

<p align="center">
  <a href="https://github.com/g1n9err/group_launchpad/actions/workflows/ci.yml">
    <img src="https://github.com/g1n9err/group_launchpad/actions/workflows/ci.yml/badge.svg" alt="Backend tests and frontend build">
  </a>
</p>

![Launchpad overview showing cohort status, connected services, and student onboarding](docs/images/overview.png)

*Screenshots show the application with demonstration data.*

## What you can do

| Workflow | Capabilities |
| --- | --- |
| Cohort setup | Create groups, assign lecturers and teaching assistants, and maintain student rosters. |
| Slack | Connect multiple workspaces, create group channels, invite participants, and publish scheduled announcements. |
| Google Classroom | Provision courses and manage student and teaching staff access. |
| Google Drive | Create a shared folder structure and apply group permissions. |
| Intake forms | Create Google Forms, publish and distribute them, and import student responses. |
| Operations | Review launch outcomes, track Slack onboarding, run resource audits, and inspect notifications. |

Each coordinator has a private workspace. Groups use their assigned Slack installation and the Google account associated with it. The interface supports Armenian, English, and Russian, light and dark themes, and mobile navigation.

<details>
<summary>Explore the group workflow and mobile interface</summary>

**Group launch**

The launch view reports the outcome for each connected service and provides controls for provisioning and retries.

![Group launch showing Slack, Classroom, Drive, and Forms outcomes](docs/images/group-launch.png)

**Mobile navigation**

The menu opens as a side drawer with grouped destinations, account access, and language and theme controls.

<p align="center">
  <img src="docs/images/mobile-navigation.png" alt="Mobile side drawer with grouped navigation and account controls" width="300">
</p>

</details>

## Quickstart

### Requirements

| Tool | Version | Used for |
| --- | --- | --- |
| Docker with Docker Compose | Compose v2 or later | PostgreSQL, API, and background worker |
| Node.js and npm | Node.js 22.12 or later | Frontend dependencies and development server |
| Go | 1.27.4 | Running the backend directly on the host |

The Docker workflow below builds the Go services inside containers.

### Start the application

```bash
git clone https://github.com/g1n9err/group_launchpad.git
cd group_launchpad
cp .env.example .env

docker compose --profile full up --build -d
npm --prefix frontend ci
npm --prefix frontend run dev
```

Open **http://localhost:5173** and choose **Create account**. Registration creates a coordinator account with its own workspace.

The default configuration supports account registration, group creation, and roster management. Configure the integrations below to create resources in Slack and Google.

| Endpoint | Purpose |
| --- | --- |
| `http://localhost:5173` | Web application |
| `http://localhost:8080/api` | HTTP API |
| `http://localhost:8080/readyz` | API readiness and database connectivity |

Migrations run automatically when the backend starts. The background worker executes queued launches, audits, announcements, and email delivery.

### Run the backend on your host

Start PostgreSQL, then run each service in its own terminal from the repository root.

```bash
make dev-up
```

```bash
make api
```

```bash
make worker
```

```bash
npm --prefix frontend ci
make fe
```

The Makefile loads the repository's `.env` file for these commands. Vite forwards `/api` requests to the local API.

## Using Launchpad

1. **Create your account.** Sign up on the local application or the hosted app. An email already present in a student roster can register a site account while retaining its roster memberships.
2. **Connect your services.** Open **Integrations**, connect a Slack workspace, and connect a Google account through the authorization popup. Associate the Google account with the Slack workspace.
3. **Create a group.** Open **Groups → New group**. Enter the group name, course, start date, teaching staff, and lecturers. Add the student roster and teaching assistants as needed.
4. **Choose the workspace.** On the group page, select its Slack workspace before provisioning channels. An existing channel assignment keeps the group tied to that workspace.
5. **Provision the resources.** Use the group's **Group launch** view to start the launch and inspect the Slack, Classroom, Drive, and Forms results. Individual service controls support follow-up provisioning.
6. **Collect intake responses.** Open **Intake forms**, select the group, review the questions, publish the form, and distribute it. Email delivery uses the configured Gmail or SMTP sender.
7. **Manage communication and onboarding.** Schedule a message in **Announcements** and review delivery history. A general announcement targets the selected group's available Slack channels. Use **Students** or the group's member view to track onboarding.
8. **Check resource health.** Open **Automation**, select a group, and run an audit. Review the findings and notifications as the result refreshes.

The workspace selector on **Overview** filters group-related views. On phones, the menu button opens navigation and account controls in a side drawer.

| Account role | Access |
| --- | --- |
| Administrator | Platform-wide operations and administrator account-management APIs |
| Coordinator | Groups and integrations in the coordinator's private workspace |
| Lecturer or teaching assistant | Read access to assigned groups and their resources |
| Student | Participation through Slack, Classroom, and intake forms |

## Integration prerequisites

Keep your credentials in `.env`. The complete configuration template is available in [.env.example](.env.example).

Generate the credential-encryption key once.

```bash
openssl rand -base64 32
```

Set the result as `TOKEN_ENCRYPTION_KEY`.

> [!IMPORTANT]
> The API and worker must use the same encryption key and integration configuration. Keep the encryption key stable across redeployments so saved credentials remain decryptable.

### Slack

Create a Slack app with OAuth v2 and configure its bot permissions using the project's [requested scopes](backend/internal/core/integrations/slack_oauth.go). Add the exact callback URL to the app's OAuth redirect URLs.

```env
SLACK_OAUTH_CLIENT_ID=your-client-id
SLACK_OAUTH_CLIENT_SECRET=your-client-secret
PUBLIC_API_BASE_URL=https://your-launchpad-domain.example
SLACK_OAUTH_REDIRECT_URI=https://your-launchpad-domain.example/api/integrations/slack/callback
TOKEN_ENCRYPTION_KEY=your-generated-key
```

Use **Integrations → Connect a Slack workspace** to authorize each installation. For onboarding events, configure `SLACK_SIGNING_SECRET` and point the Slack Events API at `/api/integrations/slack/events`, with the `member_joined_channel` bot event enabled.

`SLACK_BOT_TOKEN` supports an existing server-token installation. Coordinator-owned groups use explicit workspace assignments for connected OAuth installations.

Local OAuth and event testing can use a public HTTPS tunnel to the API. Register that tunnel's callback URL in the Slack app. Slack's setup instructions cover the authorization flow. [Slack OAuth documentation](https://docs.slack.dev/authentication/installing-with-oauth/)

### Google

Enable the Google Classroom, Drive, and Forms APIs in your Google Cloud project. Configure an OAuth consent screen and a **Web application** OAuth client, then set its authorized redirect URI to the callback used by this deployment.

```env
GOOGLE_CLIENT_ID=your-client-id
GOOGLE_CLIENT_SECRET=your-client-secret
GOOGLE_REDIRECT_URL=http://localhost:8080/api/integrations/google/callback
TOKEN_ENCRYPTION_KEY=your-generated-key
```

The redirect URI must match the Google Cloud setting exactly. Add the accounts you intend to use as test users while the OAuth consent screen is in testing mode. Review the [requested Google scopes](backend/internal/core/integrations/ports.go), then connect the account from **Integrations**. [Google credential setup](https://developers.google.com/workspace/guides/create-credentials)

### Email delivery

| Provider | Configuration |
| --- | --- |
| Gmail | Enable the Gmail API, set `GMAIL_ENABLED=true`, and reconnect Google to grant the Gmail sending permission. |
| SMTP | Set `SMTP_ENABLED=true`, `SMTP_HOST`, `SMTP_PORT`, and `SMTP_FROM`. Set `SMTP_USERNAME` and `SMTP_PASSWORD` when required by the relay. |

Apply the email configuration to both the API and worker. Classroom join messages and intake-form delivery are processed by the worker. Set `AUDIT_NOTIFICATION_EMAIL` to receive summaries of changed audit findings.

A delivery marked `sent` indicates provider acceptance. An `unknown` outcome requires checking the provider or destination before resending.

### Administrator setup

Public registration creates coordinator accounts. To bootstrap the first administrator, set `ADMIN_EMAIL` and `ADMIN_INITIAL_PASSWORD` before starting the API. Use a freshly generated password.

```bash
openssl rand -hex 24
```

For the reserved `administrator` login, set `ADMIN_EMAIL` to `administrator@launchpad.internal`. Other administrator email addresses sign in using their email. The initial account is created when the database has no administrator.

After the first successful administrator login, remove the bootstrap variables and recreate the API container. Administrator account management is available through the `/api/admin/users` endpoints documented in [openapi.yaml](openapi.yaml).

## Architecture

```mermaid
flowchart LR
    UI[React dashboard] --> API[Go API]
    API <--> DB[(PostgreSQL)]
    DB -->|River jobs| Worker[Go worker]
    Worker -->|Results and job state| DB
    API -->|OAuth and direct operations| Platforms[Slack and Google APIs]
    Worker --> Platforms
    Worker --> Mail[Gmail or SMTP]
```

The API handles accounts, authorization, group management, and job submission. River stores background jobs in PostgreSQL. The worker performs queued operations and persists their outcomes for the dashboard.

| Location | Responsibility |
| --- | --- |
| [`backend/cmd`](backend/cmd) | API, worker, and migration entry points |
| [`backend/internal/core`](backend/internal/core) | Business logic and integration interfaces |
| [`backend/internal/integrations`](backend/internal/integrations) | Slack, Google, and mail adapters |
| [`backend/internal/store`](backend/internal/store) | PostgreSQL persistence and generated queries |
| [`backend/internal/transport`](backend/internal/transport) | HTTP handlers and generated API types |
| [`backend/migrations`](backend/migrations) | Embedded database migrations |
| [`frontend/src`](frontend/src) | React UI, translations, session handling, and API client |
| [`openapi.yaml`](openapi.yaml) | Shared API contract |

The backend uses Go, Echo, pgx, sqlc, and River. The frontend uses React, TypeScript, Vite, Tailwind CSS, and TanStack Query. Integration credentials are encrypted with AES-256-GCM, passwords use bcrypt, and sessions expire and can be revoked.

## Development and verification

Run backend tests against a dedicated development or test PostgreSQL instance. The suites create temporary databases and reset their test tables.

```bash
TEST_DATABASE_URL='postgres://launchpad:launchpad@localhost:5432/postgres?sslmode=disable' make test-backend
```

```bash
npm --prefix frontend run build
npm --prefix frontend run lint
```

Install Chromium once, then run the browser suite against its disposable stack.

```bash
cd frontend
npx playwright install chromium
cd ..
make e2e
```

Browser checks exercise registration, login, role and workspace access, group and roster workflows, Google authorization handoff, and mobile navigation. External services are replaced with fixtures during automated tests.

| Command | Purpose |
| --- | --- |
| `make help` | List available development commands |
| `make migrate-up` | Apply pending migrations |
| `make migrate-version` | Inspect the migration version |
| `make gen` | Regenerate OpenAPI and SQL query bindings |
| `make test` | Run backend race tests and build the frontend |
| `make full-down` | Stop the Docker application stack |

## Deployment

The production Compose file runs PostgreSQL, the API, and the worker. Copy the production environment template, fill in the deployment values, and run from the repository root.

```bash
cp deploy/env.production.example deploy/.env.production
docker compose -f deploy/docker-compose.prod.yml \
  --env-file deploy/.env.production --profile full up --build -d
```

Build the frontend and serve `frontend/dist` behind an HTTPS reverse proxy. For a shared frontend and API origin, use a relative API URL.

```bash
npm --prefix frontend ci
VITE_API_URL= npm --prefix frontend run build
```

Route `/api/*`, `/health`, and `/readyz` to the API, and configure the static host to fall back to `index.html`. Set `CORS_ALLOWED_ORIGINS` to the frontend origin and use matching public Slack and Google callback URLs. Preserve the database volume and encryption key across releases.

The repository includes a [production deployment guide in Armenian](deploy/DEPLOY.md).

## Troubleshooting

| Symptom | Check |
| --- | --- |
| Announcements or audits stay queued | Confirm the worker is running and reads the same environment file as the API. Inspect `docker compose logs --tail 100 worker`. |
| A connected workspace cannot be used | Check the group's Slack assignment and verify that both services have the original `TOKEN_ENCRYPTION_KEY`. |
| Google reports a redirect mismatch | Compare `GOOGLE_REDIRECT_URL` with the OAuth client's authorized redirect URI, including scheme, host, port, and path. |
| Onboarding events are missing | Verify the Events API URL, signing secret, `member_joined_channel` subscription, and the bot's channel membership. |
| Port 8080 is occupied | Stop the existing API process or container before starting another backend instance. |
| The API cannot reach PostgreSQL | Start the database and check `DATABASE_URL` against its host, port, database, and credentials. |
| Docker reports permission denied | Verify access to the Docker daemon for your current user and shell. |

For a reproducible issue, include the affected workflow, application version or commit, and relevant logs with credentials removed. [Report an issue](https://github.com/g1n9err/group_launchpad/issues)
