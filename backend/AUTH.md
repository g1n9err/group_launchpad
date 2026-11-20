# Authentication & Authorization (PRD §3, §7.4)

Group Launchpad authenticates two ways; both resolve to the same role model.

## 1. Authentication flows

### Multi-role bearer sessions (primary)

```
POST /api/auth/login      {email, password}        → {token, expiresAt, user{role}}
GET  /api/auth/me         Authorization: Bearer …   → {userId?, email?, role}
POST /api/auth/logout     Authorization: Bearer …   → revokes the session
POST /api/auth/refresh    Authorization: Bearer …   → rotates the token
```

- Credentials are **bcrypt hashes** in `user_credentials` (migration `0016`);
  plaintext passwords are never stored or logged.
- The bearer token is 256 bits of CSPRNG output, returned **once** at
  login/refresh, and stored server-side **only as its SHA-256 hash**
  (`user_sessions.token_hash`). Tokens travel exclusively in the
  `Authorization` header — never in URLs.
- Sessions expire (`AUTH_SESSION_TTL`, default 12h), record `last_used_at`,
  and can be revoked. **Refresh rotates atomically**: verify-revoke-insert
  happen in one transaction with a row lock on the old session, so two
  concurrent refreshes with the same token cannot both succeed — exactly one
  successor is minted, the replay gets 401; expired/revoked tokens cannot be
  refreshed (fail closed → 401).
- **Password reset (`PUT /api/admin/users/{id}/password`) replaces the hash
  and revokes every active session of that user in one transaction** — tokens
  issued before the reset stop working immediately.
- Failed logins are indistinguishable (`ErrInvalidCredentials`) — no user
  enumeration, with timing equalization. Database/infrastructure failures
  surface as 5xx service errors and are never masked as invalid credentials.
- **Brute-force throttling**: failed logins are counted in three fixed
  windows — per IP+email pair (`LOGIN_RATE_MAX`, default 10), per account
  across all IPs (3×), and per client IP across all accounts (10×), each per
  `LOGIN_RATE_WINDOW` (default 5m). Any window over budget yields
  `429 RATE_LIMITED` with a delta-seconds `Retry-After` — BEFORE any bcrypt
  work, and identically for existing and non-existing accounts (the throttle
  is not an enumeration oracle). A single attacker address cannot lock out a
  victim (success clears the pair/account windows; only the generous account
  cap briefly blocks, and only for one window). Memory is hard-bounded
  (50,000 records per key space; expired entries pruned, soonest-to-expire
  evicted under pressure).
- **Per-process limits**: the counters live in API-process memory. This is
  NOT a distributed rate limiter — multiple API replicas behind a load
  balancer each apply their own budgets. Moving the counters to the shared
  PostgreSQL store is a drop-in extension of the same key spaces.
- **Behind a reverse proxy**, set `TRUST_PROXY=true` so client IPs are taken
  from `X-Forwarded-For` for throttling — but ONLY if the proxy OVERWRITES
  (not appends to) the header; otherwise clients can forge addresses. With
  the flag unset (default), the direct socket peer is used, which merges all
  users behind a proxy into one IP bucket.
- All `/api` responses carry `Cache-Control: no-store` (rosters, emails and
  notifications are PII that must not persist in browser/proxy caches).
- Accounts are created by an administrator:
  `POST /api/admin/users`, `PUT /api/admin/users/{id}/password`,
  `DELETE /api/admin/users/{id}`. Deletion enforces, inside one
  lock-serialized transaction, that an admin never deletes their own account
  and the last administrator always survives — even under concurrent
  deletion attempts.

### Legacy shared coordinator (Basic) — compatibility

`Authorization: Basic coordinator:<COORDINATOR_PASSWORD>` still works and maps
to the **coordinator** role with full operational access. This preserves the
deployed frontend flow and existing operators. Password checks are
constant-time; POST/PUT/PATCH/DELETE requests additionally pass the
cross-origin (CSRF) guard. The account is shared and has no per-user identity,
so it cannot be scoped per group member — new deployments should prefer
per-user accounts.

### Bootstrap administrator

On API boot, if `ADMIN_EMAIL` and `ADMIN_INITIAL_PASSWORD` are set **and no
admin exists**, exactly one admin account is created (`auth.Service.
BootstrapAdmin`). The values are idempotent: a second boot with different
values never overwrites or creates another admin. Remove both variables after
first login.

## 2. Roles (PRD §3)

| Role | Source | PRD duties |
|---|---|---|
| `admin` | users table | configure integrations, manage predefined staff, manage settings/permissions, monitor system health and credentials |
| `coordinator` | users table or the shared Basic account | create/configure/launch groups, monitor progress, review onboarding, manage announcements, resolve integration errors |
| `lecturer` | users table | participate in the assigned group; access its Slack/Classroom/Drive resources |
| `ta` | users table | assist lecturers; access assigned resources |
| `student` | users table | submit the intake form and join Slack **through the external platforms** — the PRD grants students no API access |

## 3. Route authorization matrix

`C` = coordinator, `A` = admin, `L` = lecturer (assigned), `T` = TA (assigned),
`S` = student, `—` = denied (403), `anon` = anonymous.

| Route | anon | S | L/T (own group) | L/T (other group) | C | A |
|---|---|---|---|---|---|---|
| `GET /health`, `GET /readyz` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `POST /api/auth/login` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| `GET /api/integrations/google/status` (public projection) | ✓¹ | ✓¹ | ✓¹ | ✓¹ | ✓ | ✓ |
| `GET /api/integrations/slack/status` | ✓¹ | ✓¹ | ✓¹ | ✓¹ | ✓ | ✓ |
| `GET /api/groups` | 401 | 403 | **scoped: own only** | 403 | all | all |
| `GET /api/groups/{id}` | 401 | 403 | ✓ | 403 | ✓ | ✓ |
| `GET /api/groups/{id}/resources` | 401 | 403 | ✓ | 403 | ✓ | ✓ |
| `GET /api/groups/{id}/status`, `/audits`, `/announcements`, `/forms` | 401 | 403 | ✓ | 403 | ✓ | ✓ |
| `GET /api/groups/{id}/onboarding` (roster PII), `/forms/deliveries` (emails) | 401 | 403 | **403** | 403 | ✓ | ✓ |
| `GET /api/notifications`, `/api/audit-notifications`, `/api/announcement-templates*` | 401 | 403 | 403 | 403 | ✓ | ✓ |
| `POST /api/groups`, `POST …/launch`, `…/slack|classroom|drive|forms/provision|publish|distribute|responses/import`, `POST …/audits`, `POST …/announcements`, templates CUD, notification read* | 401 | 403 | 403 | 403 | ✓ | ✓ |
| `GET /api/integrations/google/connect` (configure) | 401 | 403 | 403 | 403 | ✓² | ✓ |
| `GET/POST/DELETE /api/admin/users…`, `/api/admin/staff…` | 401 | 403 | 403 | 403 | 403 | ✓ |
| `POST /api/integrations/slack/events` | HMAC-verified by the Slack signing secret (not role-based) | | | | | |
| `GET /api/integrations/google/callback` | OAuth `state` (not role-based) | | | | | |

¹ Public endpoints return a **stripped projection** (connection state only);
account email/scopes require staff credentials.
² Configuring integrations is an administrator duty (PRD §3.2); the
coordinator retains access for compatibility with the deployed flow.

### Group scoping

A lecturer/TA is "assigned" to a group when either:

- a `group_members` row with `member_role` **lecturer** or **ta** exists for
  their user id, or
- a `staff_assignments` row with role **lecturer** or **ta** matches their
  normalized email (PRD §4.1).

Deliberately excluded from granting scoped access:

- **student memberships** — the PRD grants students no API surface (§3.5); a
  lecturer who is also a rostered student does not inherit access through
  that membership;
- **teaching_staff assignments** — the predefined operational staff (§4.2) is
  a provisioning concept; the PRD authorizes no API access for it.

The check runs on **every** group-scoped request
(`transport.requireGroupRead`); a user assigned to one group receives `403`
for any other group's details, resources, status, audits, announcements, or
forms. If membership cannot be verified (store error or unwired checker), the
request **fails closed** (403/500 — never a silent allow).

### PII non-disclosure

Student names/emails surface only through `GET /api/groups/{id}/onboarding`
and `GET /api/groups/{id}/forms/deliveries` — both coordinator/admin only
(PRD §9.2 "Sensitive student data must have restricted access"). Lecturers and
TAs (even for their own group) get `403`. Notification details are aggregates
("2 of 24 students have not joined Slack") — never email lists.

## 4. Deliberate design decisions (recorded ambiguity)

The PRD leaves open who may launch/modify groups (§15 Q12) and what lecturers
may read. The safest consistent choices made here:

1. **Admin is a superset of coordinator** for reads and group operations, and
   exclusively owns user/staff/integration management. Rationale: §3.2
   ("manage application settings and permissions", "monitor system health")
   requires full visibility; write access to groups stays with coordinators —
   and admins, since an administrator above the coordinator in the same
   organization is standard and the PRD does not forbid it.
2. **Lecturers/TAs are read-only** and never see the student roster via the
   API: the PRD grants them resource access (§3.3, §3.4), and §6.4 assigns
   roster inspection to the coordinator.
3. **Students get no API surface**: the PRD's student duties (§3.5) are all
   fulfilled through Google Forms and Slack. A student token authenticates but
   authorizes nothing (fail closed).
4. **`GET /api/groups` / `GET /api/groups/{id}` became authenticated** with
   this change (previously public, no PII was exposed — only names/courses).
   This is a deliberate compatibility break; the frontend unlock flow already
   sends credentials for every request.
