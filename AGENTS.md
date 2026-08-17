# Acuity Product Agent Contract

Acuity Portal is the shared operating workspace where medical-practice teams
turn patient communication into accountable work with durable evidence.

## Start with the product

Before planning, reviewing, or editing this repository, read `VISION.md`. Treat
its North Star, product principles, boundaries, and non-negotiables as acceptance
criteria, not background reading.

Then read only the context the task needs:

- `CONTEXT.md` defines the product's canonical vocabulary. Use those terms in
  code, tests, issues, and user-facing copy.
- `README.md` owns the current architecture.
- `docs/acuity-portal-product-technical-spec.md` owns detailed product behavior
  and the production release bar.
- `docs/architecture/overview.md` owns module boundaries, dependency direction,
  state lifecycles, and architectural invariants.
- GitHub Issues own committed product work. Read the issue and all comments
  before implementing it.

If these sources disagree with each other or with the implementation, surface
the conflict. Do not silently choose a new product rule.

## Working rules

- Treat provider evidence and committed product state as distinct. A click,
  HTTP response, provider request, or green readiness check does not by itself
  prove a completed patient outcome.
- Use synthetic, PHI-free data in tests, fixtures, screenshots, logs, and docs.
  Contact Context is not verified patient identity or a medical record.
- Preserve unrelated working-tree changes. Do not commit, push, deploy, alter
  cloud resources, or write production data unless the user explicitly asks.
- Distinguish local, CI, deployed application-path, provider, and durable
  database evidence in every handoff. State what remains unverified.

## Repository boundaries

- The Go backend is a modular monolith under `backend/`; keep business rules in
  the owning module and adapters at explicit seams.
- `api/openapi.yaml` is the browser/backend contract. Do not edit generated
  files by hand: `backend/internal/api/openapi.gen.go` and
  `web/src/lib/api/generated/`.
- Changes under `web/` must also follow `web/AGENTS.md`. The installed Next.js
  version may differ from training data; consult its local docs before coding.
- Telnyx work must follow `.agents/skills/README.md` and the relevant vendored
  Telnyx skill before implementation.
- Database integration tests may reset schemas. `TEST_DATABASE_URL` must name a
  disposable database ending in `_test`; `E2E_DATABASE_URL` must end in `_e2e`.

## Verification

Start with the narrowest relevant test. Before handoff, run the affected checks
from `.github/workflows/ci.yml` and report exact commands and results.

- Run the database-backed Go suite serially with a disposable `_test` database:
  `go test -p 1 ./backend/... ./deploy -count=1`
- Regenerate contracts after editing `api/openapi.yaml`:
  `go generate ./backend/internal/api && pnpm --dir web api:generate`
- Browser journey, with a disposable `_e2e` PostgreSQL database:
  `E2E_DATABASE_URL=... ./scripts/run-e2e.sh`

A skipped integration test is not proof. If a required check cannot run, say
why and describe the remaining risk.

## Agent skills

### Issue tracker

Issues and PRDs are tracked in GitHub Issues. See
`docs/agents/issue-tracker.md`.

### Triage labels

This repo uses the five default triage labels. See
`docs/agents/triage-labels.md`.

### Domain docs

This is a single-context repo. See `docs/agents/domain.md`.
