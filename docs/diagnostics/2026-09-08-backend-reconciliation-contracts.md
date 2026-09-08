# Backend reconciliation contracts and monitoring repair

## Failure and ownership

The September 7–8 production review found 284 local Telnyx call-event identity
errors, 46 duplicate-active-call errors, and 22 worker database acquisition
errors following provider transport failures. The serving backend image matched
commit `fee6f1b55ad4592c4843dc394002e82d9f226bf9`.

Read-only provider inspection showed that call events use `leg_id`,
`application_session_id`, `occurred_at`, and a nested webhook `payload`, while the
pinned SDK decodes legacy fields. The canonical event time lives inside the
nested webhook; the outer read timestamp is different and lacks a timezone.
Active-call responses use cursor metadata, while the SDK's numbered iterator
could reread page one. The iterator also replaced the caller context on later
pages, allowing reads to outlive the worker budget. The subsequent scheduling
write used that expired context, producing a misleading pool-timeout diagnostic.

Synthetic regression tests against the original code reproduced these failures.
The repair stays in the existing provider adapter, PostgreSQL executor,
reconciliation scheduling, and observability owners.

## Changes

- Adapter-owned DTOs normalize current call-event payloads and retain the
  explicitly documented legacy representation. Canonical webhook IDs, times,
  and payload fields survive normalization. Missing or conflicting identities
  remain errors. Recording-failure lookup uses the same corrected read path.
- Contextual cursor/numbered pagination has page bounds, repeated-cursor
  detection, and exact identity deduplication. Provider-supplied URLs are not
  followed with credentials. Provider effect execution remains unchanged.
- Observation reserves one second of the existing worker deadline for the
  fenced result write. Timeout records durable backoff; cancellation preserves
  the existing one-minute claim lease. No detached context, extended deadline,
  or automatic effect replay was added.
- Database diagnostics distinguish parent operation expiry from actual
  connection-acquisition expiry. Typed connection failures get the bounded
  `connection` cause. A disposable-database test terminates a connection and
  verifies that the failed transaction stays failed and a subsequent operation
  obtains a fresh connection.
- Admission failures emit bounded reasons: provider identity, wrong connection,
  invalid address, missing handoff, or ambiguous handoff. Admission checks and
  durable receipt outcomes are unchanged.
- Separate diagnostic availability and latency metrics cover messaging reads,
  AI outcome reads, Calling readiness, and SSE establishment. SSE success
  requires a successful first-ready flush; stream lifetime is not used as
  establishment latency. The original two-route availability SLO is unchanged.

## Production monitoring

The existing email notification channel was enabled. The checked log metrics
and diagnostic policies were reconciled using:

```sh
GCP_PROJECT=acuity-health-prod \
MONITORING_NOTIFICATION_CHANNELS=projects/acuity-health-prod/notificationChannels/4108333038446484446 \
  node deploy/observability/apply.mjs --apply
```

Live readback found all 33 checked log metrics and all 10 checked diagnostic
policies. The separate two SLO-burn policies remain present. The terminal Staff
occupancy policy now uses the exact nonzero counter rather than the old
histogram. The new command-stage metric is registered.

Application changes remain local until release. Registration of new metrics
and policies does not prove application ingestion or a patient outcome.

## Verification

The before-fix and repaired provider regression tests cover payload preservation,
identity conflicts, recording failure, cursor termination, repeated cursors,
page bounds, identical duplicates, and deadlines on both later-page paths.
A read-only smoke check through the repaired adapter accepted one live Portal
leg and returned one `call.initiated` fact. There were no active calls during the
probe; live nonempty cursor traversal remains unverified.

Passed with Go 1.26.7 and an isolated disposable local PostgreSQL database:

```sh
TEST_DATABASE_URL=postgres://postgres@127.0.0.1:55443/acuity_backend_fix_test?sslmode=disable \
  go test -p 1 ./backend/... ./deploy -count=1
node --test deploy/observability/*.test.mjs
go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./backend/...
```

The Node apply/policy suite passed six tests. Vulnerability scanning found none.

The release-container check was attempted with
`bash ./scripts/test-release-container.sh` but could not execute because the
local Docker daemon was unavailable. This remains a CI verification requirement.

The final custom-parent-deadline edge case was followed by a complete rerun of
`backend/internal/postgres` and `backend/internal/observability` against the
isolated disposable database; both passed.

Notification delivery was verified by three real Google Cloud Alerting
resolution emails received after restoration at 10:28–10:30 a.m. Pacific.
These prove delivery through the configured channel, not backend health. A
separate synthetic delivery check was created while waiting; its isolated gauge
was set back to zero and its temporary policy and metric were removed once real
delivery was confirmed. No synthetic signal was written to application metrics.

The complete browser journey passed **31 tests** (2.4 minutes), including
calling, messaging, analytics, and realtime paths:

```sh
GOTOOLCHAIN=go1.26.7 \
E2E_DATABASE_URL=postgres://postgres@127.0.0.1:55443/acuity_backend_fix_e2e?sslmode=disable \
  ./scripts/run-e2e.sh
```

The final PostgreSQL/observability rerun used `acuity_deadline_fix_test`, a
separate disposable database. The temporary local PostgreSQL server was stopped
after verification. Production readback confirmed 12 remaining alert policies
(10 diagnostic plus two SLO-burn) and no temporary notification-test policy.

## Evidence limits

The recovered HTTP 503 burst coincided with four lost Portal database
connections. The initiating cause remains unproven; no blanket retry or capacity
change was made. The new classification improves the next investigation.

The diagnostic database snapshot contained 158 initial handoff rejections and
349 related rejected-leg events. Raw receipt access was denied to the inspection
role, so expected unrelated traffic versus actual transfer-admission failure was
not established. New reason metrics make that distinction observable without
exposing raw inputs. Retained errors on 39 ended CallLegs were not counted as
active work: the complete worker candidate predicate returned no due candidates.

No application release, provider effect, receipt replay, or production database
write was performed. Production monitoring configuration changed as described;
local tests used synthetic data and disposable databases.
