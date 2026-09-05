# Backend latency and recovery hardening

This local change starts from `f34484434b253f03cd2b50425664c8f59e1dffbf`.
Before PR #267 publication, it was rebased onto release-only
`fb0f0e54e75cb050067e89c90b66725ee7f89a61`.
It follows the September 4 health review: Staff Dial timing mixed several kinds
of waiting, legacy analytics repeatedly parsed raw history, and failed CallLeg
observations could move the evidence window forward.

## Staff Dial and timing

The worker used to wait a full 250 ms after every eight successful claims even
when more work and executor capacity remained. A full batch now yields without
an idle delay. Empty or dependency-blocked scans retain their fallback interval;
executor limits, error backoff, command identity, and same-Call serialization
remain enforced.

The subsequent [worker wakeup exploration](2026-09-04-worker-wakeup-exploration.md)
adds a local coalesced signal after successful receipt processing and command
completion. It wakes the coordinator from idle or blocked waiting while retaining
the timer fallback and error backoff. The original measurements below describe
the initial batch-only patch; the linked comparison measures this follow-up.

A deterministic regression compares the full-batch then blocked-scan waits:
the original Runner emits `[250ms, 250ms]`, while the new Runner emits
`[0s, 250ms]`. Integration timings are logged separately from correctness gates
to avoid a machine-load-dependent latency assertion.

The new test retains inbound answer projection and ringback, runs the real
Runner and PostgreSQL executor with two database connections, and holds ten
provider requests open to prove overlap. It also checks receipt/recovery progress
while those requests are in flight. Before the change, the first-to-last ready
Staff Dial spread was 259 ms. Repeated local runs after the change measured
approximately 6–8 ms. These measure the removed batch delay, not a promise that
all production Calls start ringing in that time. Total dispatch still includes
ringback ordering, dependency waits and the blocked-scan polling interval.

The additive provider-command stage metric separates claim work, creation to
first claim, claim to dispatch, provider execution, and result persistence.
Earlier stages are emitted before later work can fail; retries do not publish
another first-claim sample. Empty successful polls emit nothing. Existing legacy
metrics keep their values, with corrected descriptions. See the
[observability contract](../architecture/call-center-observability.md).

The mixed-workload acceptance scenario uses two independent inbound Calls with
five Staff each, ten in-flight provider Dials with one definitive failure, a
16-receipt burst, and three authorized analytics reads over 300 compact-evidence
rows. The worker retains two database connections, one occupied for 200 ms.
It proves nine SENT and one FAILED Dial, two SENT ringbacks, twelve distinct
provider effects without duplicates, all sixteen receipts durably UNKNOWN,
correct analytics results, and no database failure/acquisition telemetry.
The final race-enabled local run measured 38 ms to dispatch all Dials, 304 ms
for receipts/analytics and 322 ms to complete durable command results. These
are local observations, not a production load envelope or percentile guarantee.

## Analytics

Legacy operator AI analytics now uses the shared admission/deadline guard.
Migrations 0055–0056 derive compact analytical evidence whenever source transcript
or closeout evidence changes, including writes by an overlapping older revision.
A resumable backfill updates at most 100 existing rows per committed batch.
Range summaries and list pages read compact evidence, preserving the current Go
normalization, native/historical tool semantics and global latency samples.
Detail views continue to use original evidence. Missing backfill is a visible
error, never silently omitted Calls or invented zero values.

The synthetic scale case contains 3,000 Calls with 174,254 transcript bytes per
Call. Its compact evidence is 354 bytes per Call (approximately 492 times smaller).
Initial local full-query measurements improved from 1.87 seconds to approximately
42–47 ms; those initial runs differed in toolchain/load and are directional, not
an SLO claim. A subsequent sequential comparison using pinned Go 1.26.7 binaries
and the same fixture under mixed local load observed a 5.226-second statement
timeout before, and a successful 535 ms query after. These are single-run local
observations; the 492-fold payload reduction is independent of host timing.
Tests assert output/pagination parity, corrections, idempotence,
global percentiles, runtime grants and backfill recovery after a locked batch.

## Recovery

A failed or empty provider observation used to update `CallLeg.updated_at` before
reading provider evidence. The next retry then queried a newer window and could
miss a delayed event. Migration 0057 adds separate operational scheduling fields:
last claim time, next attempt, capped consecutive failure/backoff count, and a
bounded error code. `updated_at` remains a domain-state timestamp.

Failed and empty reads preserve the evidence window. Unresolved commands also
anchor it to command creation when that is earlier. Repeated failures back off
1, 2, 4, 8, then at most 15 minutes, without preventing other Calls from being
examined. Fresh durable state can bypass old backoff after the existing stale
threshold. A successful empty read is not proof that all provider history has
arrived. There is no invented provider completeness watermark.

Tests cover delayed hangup convergence, persisted schedules after worker
recreation, fairness, fresh-state eligibility, and stale observer fencing.
Operational scheduling never replays a provider effect or invents success.
`reconciliation_attempts` is capped backoff state, not a lifetime attempt counter.
New scheduling fields are internal database diagnostics, not a new Staff UI.
Existing warnings and unresolved Call state remain visible.

The accepted recovery policy is to keep capped checks and durable errors for
now. Checks continue at the capped interval while the item remains eligible;
there is no new automatic exhaustion or operator-owned exception workflow.

Read-only aggregate inspection after deployment can use:

```sql
SELECT reconciliation_error_code,
       count(*) AS call_legs,
       min(reconciliation_next_attempt_at) AS next_attempt_at,
       max(reconciliation_attempts) AS maximum_backoff_step
FROM human_calling_call_legs
WHERE reconciliation_error_code IS NOT NULL
GROUP BY reconciliation_error_code;
```

The aggregate contains no patient content or provider identifiers. It establishes
scheduled work, not patient outcome completion. Do not bulk replay.

## Verification boundary

The implementation uses synthetic local PostgreSQL/provider evidence. The final
handoff records exact test commands/results. Required local checks include the
serial backend/deploy suite on a disposable `_test` database, the browser journey
on a disposable `_e2e` database, the production release-container test, and the
pinned vulnerability scan.

Production database migration, metric application, provider requests,
and deployment remain outside this change's verified evidence. Production improvement
requires migration/serving-revision verification, metric ingestion and comparison
of the same workload, plus provider and durable outcome evidence. The existing
single-zone database availability limitation is unchanged.

## Local verification results

All Go checks used `GOTOOLCHAIN=go1.26.7`, matching CI. PostgreSQL 16 listened only
on localhost, with separate disposable databases for concurrent workers. Browser
verification used local Node 24.19.0 and the pinned pnpm 10.34.5.

| Check | Command | Result |
| --- | --- | --- |
| Complete backend and deployment suite | `TEST_DATABASE_URL=postgres://chasefagen@127.0.0.1:55439/acuity_integrated_test?sslmode=disable go test -p 1 ./backend/... ./deploy -count=1` | PASS |
| Focused concurrency/race checks | `TEST_DATABASE_URL=postgres://chasefagen@127.0.0.1:55439/acuity_dial_test?sslmode=disable go test -race -p 1 ./backend/internal/worker ./backend/internal/observability ./backend/internal/humancalling -run 'TestProviderCommand\|TestInboundStaffDialFanoutProgressesWithTwoDatabaseConnections\|TestOutgoingReconciliation' -count=1` | PASS |
| Review-fix telemetry check | `go test ./backend/internal/observability ./deploy -run 'TestProviderCommandStages\|TestCallCenterLogMetricDefinitionsAreBoundedAndComplete' -count=1` | PASS |
| Final browser journey | `E2E_DATABASE_URL=postgres://chasefagen@127.0.0.1:55439/acuity_latency_e2e?sslmode=disable ./scripts/run-e2e.sh` | 29 passed, 1.7 minutes |
| Pinned release-container verification | `bash ./scripts/test-release-container.sh` | PASS, isolated Linux/amd64 deployment image |
| Vulnerability scan | `go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./backend/...` | No vulnerabilities found |
| Whitespace validation | `git diff --check` | PASS |

An initial integration pass caught the four recovery fixture scheduling changes
and the migration-count expectation. Those were corrected without removing the
original outcome assertions. An unchanged 20 ms connection-acquisition test also
failed under simultaneous local test/build load; the final serial suite passed
it without changing its deadlines. The full suite passed before the small Staff
Transfer metric label review fix, which received the focused telemetry recheck
listed above.

### Standards review

Independent review found one actionable issue: the new stage action allowlist
omitted `transfer_staff`. The known action and a positive normalization regression
were added. No other actionable standard, privilege, migration or locking issue
was reported.

### Spec review

Independent review found no confirmed correctness regression. The broader
proposal's recovery exhaustion workflow was resolved by the explicit user choice
to keep capped checks and durable errors. A local mixed-workload acceptance check
supplements the focused tests; production p95/p99 targets still require a defined
capacity envelope and deployed observations.

Final added acceptance command (PASS with race detector):

```sh
GOTOOLCHAIN=go1.26.7 \
TEST_DATABASE_URL='postgres://chasefagen@127.0.0.1:55439/acuity_dial_test?sslmode=disable' \
go test -race ./backend/internal/humancalling \
  -run '^TestMixedInboundCallsReceiptsAndAnalyticsProgressWithBoundedDatabaseCapacity$' \
  -count=1 -v
```

Local raw check logs are retained in
`/tmp/acuity-backend-latency-20260904/` (`backend-final.log`, `race.log`,
`mixed-final.log`, `e2e-final.log`, `release-container.log`,
`govulncheck-final.log`). Detailed before/after worker experiments are in
`/tmp/acuity-health-20260904/`. The temporary PostgreSQL and container runtime
started for this work are stopped after verification; recreate a disposable
local database before rerunning database-backed commands.

After publication, the complete serial backend/deploy suite passed again on
PR commit `10f2b1b67231d545e2b732ff577876b907740bd7`
(`/tmp/acuity-backend-latency-20260904/backend-pr.log`). All required CI checks
passed for that commit in run `33932028197`. The subsequent signed-answer
receipt experiment is recorded in the
[second latency review](2026-09-04-staff-dial-latency-review.md#follow-up-experiment-after-pr-publication),
including seven race-enabled scenarios and the remaining polling delays.
