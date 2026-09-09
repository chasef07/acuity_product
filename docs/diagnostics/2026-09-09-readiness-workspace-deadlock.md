# Operator readiness and workspace-version deadlock

## Failure and owner

Production PostgreSQL recorded the same lock cycle at 18:03:31 UTC, before
the September 9 release, and at 18:40:49 and 18:41:04 UTC after it. Readiness
failed with SQLSTATE `40P01` while waiting for either a softphone lease or a
Call. The other transaction was waiting to increment
`access_practices.workspace_version`.

`Access.LockOperationalActor` held `FOR SHARE` on Practice rows for a Platform
Operator before `HumanCalling.SetReadiness` locked its occupied Calls and
lease. Call processing held a Call or lease before
`Access.RecordWorkspaceChange` updated the Practice. Those opposite
dependencies formed a cycle. The normal Membership authorization path locks
Membership rows and is not changed by this correction.

The Practice shared lock dates to August 9 (PR #82), and the current readiness
Call-locking step dates to August 30 (PR #205). PR #295, the SIP handoff
admission repair, did not change these paths.

## Correction

Use `FOR KEY SHARE` for the operator's Practice locks. This still protects
Practice identity against deletion/key changes while allowing the non-key
workspace-version update. Existing Call/lease locking and authorization checks
remain intact. No retry, concurrency, timeout, schema, or provider change is
needed to remove this particular cycle.

## Before and after

`TestOperatorReadinessDoesNotDeadlockWorkspaceChange` exercises the real
readiness and workspace-change methods against PostgreSQL. A second
transaction holds the Call or lease as call processing does. The test waits
until `pg_blocking_pids` confirms readiness is blocked by that exact connection
before asking it to publish its workspace change. It requires both operations
to complete and checks that the returned workspace version was committed.

Before the correction, both cases failed with `deadlock detected (SQLSTATE
40P01)`: `lock calling readiness lease` and `iterate occupied Call locks`.
After the correction, both passed in three consecutive runs. The existing
Platform Operator access scenario now also verifies that workspace updates
can proceed while the operational lock is held and that an exclusive Practice
lock is still rejected with `55P03`.

The focused revoked-Practice capacity regression and two-connection,
ten-Staff fan-out regression passed. The latter observed all ten provider-stub
dials dispatched in about 13 ms; that local observation does not establish a
production latency percentile or a staff-browser ringing time.

## Verification commands

Go 1.26.7 and a private disposable PostgreSQL 16 database named
`acuity_calling_health_test` on localhost port 55462:

```sh
export GOTOOLCHAIN=go1.26.7
export TEST_DATABASE_URL='postgres://127.0.0.1:55462/acuity_calling_health_test?sslmode=disable'

go test ./backend/internal/humancalling \
  -run '^TestOperatorReadinessDoesNotDeadlockWorkspaceChange$' -count=3 -v

go test -p 1 ./backend/internal/access ./backend/internal/humancalling \
  -run 'TestPlatformOperatorHasOperationalAccessWithoutMemberships|TestOperatorReadinessDoesNotDeadlockWorkspaceChange|TestRevokedPracticeCallRemainsGlobalCapacityWithoutLeakingScope|TestInboundStaffDialFanoutProgressesWithTwoDatabaseConnections' \
  -count=1 -v

go test -p 1 ./backend/... ./deploy -count=1
go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./backend/...
git diff --check
```

All commands above passed. The full serial backend/deploy suite ran with the
disposable database configured, and `govulncheck` reported no vulnerabilities.
This is local evidence; hosted CI and browser journeys were not part of this
local verification.

## Production verification and remaining work

This correction is local until separately released. Verify the deployed image
contains it, then observe concurrent readiness updates and real transfers:
no recurrence of this deadlock, no lost authorization protection, continued
staff bridges, and worker acquisition and dial-queue latency under real load.
The observed worker timeouts may share the same blocked transactions, but this
change does not prove every timeout or provider delay is repaired.

The three old provider receipts were waiting for related events under the
existing 15-minute slow-retry policy, with a 24-hour maximum before quarantine.
They are not evidence of stuck PostgreSQL locks. Missing provider facts must
be correlated before recovery or audited terminalization.

An older ambiguous Bridge and pending Hangup command remain associated with
an ended Call. The ambiguous command blocks normal command claiming, and the
terminal-Call observation filter excludes its Bridge action. This needs a
separate provider-evidence/convergence audit; this patch does not label an
uncertain Bridge successful or replay its effect.

No production records, provider settings, deployment, or cloud resources were
changed. Production evidence here contains no patient identifiers or content.
