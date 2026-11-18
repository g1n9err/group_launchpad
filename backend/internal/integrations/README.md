# Integrations (Day 2 — nothing here yet, by design)

This package will contain the ONLY code that talks to external platforms:

```
integrations/
├── slack/       # channels, invites, membership, chat — slack-go/slack
└── google/      # classroom/, drive/, forms/ — google.golang.org/api
```

Rules (PRD §7.4, SYSTEMPROMPT Step 5):

- Each adapter implements a **port interface declared in core** (e.g.
  `core/workflow.SlackGateway`). Core never imports this package.
- Adapters are **credential-gated**: no token configured ⇒ adapter refuses to
  operate. Nothing fakes a successful integration, in prod or in tests — tests
  use recorded HTTP fixtures and are labeled as such.
- Every external call is logged to `external_operation_logs` with duration and
  status, and resource creation goes through the `group_resources` idempotency
  registry (check-before-create, store-external-ID).
