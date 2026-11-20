# External integrations

This package contains adapters that call external services. Business rules and
interfaces live in `internal/core/integrations`; the composition roots in
`cmd/api` and `cmd/worker` provide configured adapters to those services.

Current adapters:

- `slack/`: workspace status, channel provisioning, invitations, welcome
  messages, signed Events API verification, member lookup, audit, and scheduled
  announcements.
- `google/`: OAuth token management plus Classroom, Drive, Forms, and resource
  audit operations.
- `mail/`: SMTP relay and Gmail delivery for intake forms and audit notices.

Adapters fail closed when credentials are absent or invalid. Tests replace
external gateways with local fakes or HTTP fixtures; live Slack and Google
resources are never required by the test suite. Stored Google credentials are
encrypted by `internal/secrets` before they reach PostgreSQL.

Provisioning services record external operation outcomes and use the
`group_resources` registry or provider identifiers to make retries idempotent.
Audit jobs report drift for coordinator review; they do not repair external
resources automatically.
