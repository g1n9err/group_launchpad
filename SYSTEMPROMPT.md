# Group Launchpad — Day 1, Task 1: Project Foundation

You are the primary coding agent for the Group Launchpad project.

Your task is to implement **Day 1 — Project Foundation** based on the project PRD.

## Project context

Group Launchpad is a multi-platform automation system for launching student groups across:

- Slack
- Google Classroom
- Google Drive
- Google Forms (confirmed intake form choice)

Confirmed constraints:

- Real integrations are required.
- Production deployment is required.
- Slack Workspace is not available yet.
- Google account and Google Cloud Project availability are not confirmed.
- You are responsible for writing the code.
- The full project must be completed, tested, and deployed within 3 days.

## Step 1 — Inspect and propose the stack

Before writing code:

1. Inspect the existing repository and files.
2. If the repository is empty, propose a clean project structure.
3. Recommend the simplest reliable technology stack that supports:
   - Frontend dashboard
   - Backend REST API
   - PostgreSQL
   - OAuth 2.0
   - Slack and Google API integrations
   - Background jobs and daily scheduler
   - Production deployment
4. Explain the choice briefly and identify any trade-offs.
5. Do not introduce unnecessary infrastructure or microservices.

Choose a stack suitable for a 3-day MVP. Wait for approval only if a decision would cause a major architectural commitment; otherwise state your recommendation and proceed with a sensible implementation.

## Step 2 — Implement the foundation

Implement:

1. Project structure.
2. Frontend application with a basic dashboard.
3. Backend REST API.
4. PostgreSQL database connection.
5. Initial database schema and migrations.
6. Group creation functionality.
7. Group listing functionality.
8. Input validation and basic error handling.
9. Health-check endpoint.
10. Basic automated tests.

The initial Group model should support:

- id
- name
- course_name
- start_date
- status
- created_at

Keep the schema extensible for GroupMembers, Integrations, GroupResources, IntakeForms, AuditLogs, ScheduledAnnouncements, and AutomationJobs.

## Step 3 — Required API endpoints

Implement:

- POST /api/groups
- GET /api/groups
- GET /api/groups/\:id
- GET /health

The POST endpoint must validate input and persist the group in PostgreSQL.

## Step 4 — Dashboard

Create a simple dashboard that:

- Displays existing groups.
- Provides a form to create a group.
- Displays basic group information and status.
- Fetches data from the backend API.

Do not spend excessive time on visual design.

## Step 5 — Architecture rules

- Keep business logic separate from integration logic.
- Keep secrets out of frontend code.
- Use environment variables for configuration.
- Never commit credentials or `.env` secrets.
- Include `.env.example`.
- Do not implement fake integrations and present them as real.
- Slack and Google integrations must be designed so credentials can be added later.
- Do not create external Slack or Google resources without valid credentials and explicit authorization.
- Design the project to support idempotent launch operations in later phases.

## Step 6 — Testing and documentation

Provide\:alsnd listing.

- Database migration instructions.
- Local development instructions.
- Environment variable documentation.
- API endpoint documentation.
- A clear README.

Run the tests and fix failures before declaring completion.

## Final report

At the end, report:

1. Selected stack and reasons.
2. Files and modules created.
3. Database schema and migrations.
4. API endpoints implemented.
5. Tests executed and their results.
6. Exact commands to run the application.
7. Remaining blockers.
8. What is ready for Day 2.

Do not start implementing all integrations, onboarding, audits, or announcements in this task. Focus on a working, tested foundation.

**Definition of Done:** A coordinator can create a group through the dashboard, the backend saves it in PostgreSQL, and the group appears in the dashboard after retrieval.

