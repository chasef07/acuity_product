# Worker wakeup exploration before merging PR #267

This exploration starts from PR head
`a959b7c1223afbb01c6fbed1e16e605313f2a278`. The PR remains unmerged.
The existing signed-answer fixture established successive waits between receipt
arrival, committed fanout, and provider dispatch. The goal is to remove avoidable
waiting while preserving durable ownership, bounded concurrency, and recovery.

## Alternatives

| Approach | Waiting addressed | Added cost |
| --- | --- | --- |
| Local coalesced worker hint after receipt processing and successful command completion | The command coordinator waiting after its own runtime has committed work or released a dependency | One private buffered signal; existing database claims and fallback polling |
| Commit-coupled PostgreSQL notification from receipt ingress | The separate worker waiting to discover a newly committed receipt | Dedicated listener, reconnection lifecycle, migration, capacity-contract changes |
| Relay through realtime or add a broker | Cross-process discovery | Another scheduling dependency or infrastructure without an established need |

The first candidate is the local hint. `provider-ingress` and `worker` are
separate runtime roles; a local signal cannot remove ingress-to-receipt polling.
Receipt eligibility and future retries therefore retain the 250 ms fallback.
The realtime service is a separate process, and its workspace hints are emitted
after domain changes. They do not cover raw receipt ingress.

## Candidate contract

- Only the existing provider coordinator claims commands. Hints contain no work
  identity, provider facts, or product state.
- Successful receipt processing and successful command execution can request
  another scan. The module must return after persistence before signaling.
- A capacity-one channel coalesces hints without blocking producers. The next
  authoritative scan covers stale hints; a hint arriving during a scan can
  request a follow-up scan.
- Idle and dependency-blocked waits may wake early. Claim-error and executor-error
  backoff retain their existing timers. Executor capacity, command eligibility,
  same-Call ordering, and idempotency remain in their current owners.
- Polling repairs a dropped hint, process restart, work committed by another
  process, or a future `next_attempt_at`. A hint never makes work eligible.

## Cross-process option if still warranted

A fixed empty-payload calling-receipt channel, notified by a narrow transaction-
coupled INSERT trigger, could cover overlapping older writers. The worker would
own a direct connection, complete LISTEN before requesting a scan, and repeat
that sequence after reconnect. It would continue polling while disconnected.
Notification loss, duplicate hints, startup races, rollback, shutdown, and two
overlapping workers would require explicit integration proof.

PostgreSQL describes the listen-then-scan startup order in its
[LISTEN documentation](https://www.postgresql.org/docs/16/sql-listen.html).
Notifications are delivered after commit; a notifying transaction can itself
fail at commit if the notification queue is exhausted. This is an additional
ingress acknowledgement-path dependency, described in the
[NOTIFY documentation](https://www.postgresql.org/docs/16/sql-notify.html).

The current checked reservation is 36 usable database connections. A listener
per worker revision would raise that to 38 during the allowed two-revision
overlap. It must not consume either of the worker's two query connections.
Relevant owners are `deploy/production-runtime-contract.json`, its renderer and
contract tests, and `docs/architecture/overview.md`.

The cross-process listener is a separate design decision. It is not needed to
measure the smaller local candidate and is not implemented by this exploration.

## Matched local comparison

The unchanged seven-case signed-answer fixture ran against two binaries built
with Go 1.26.7: the original PR head and the local worker candidate. They used
the same disposable PostgreSQL 16 database sequentially. Each scenario submits
ingress just after an empty receipt scan; this intentionally includes nearly a
full receipt poll and is not representative percentile sampling.

Accepted receipt to first provider invocation, milliseconds rounded:

| Staff | Ringback stub | Original PR | Local wakeups |
| --- | --- | --- | --- |
| 1 | immediate | 757 | 267 |
| 5 | immediate | 756 | 262 |
| 10 | immediate | 758 | 270 |
| 1 | 200 ms | 757 | 468 |
| 5 | 200 ms | 761 | 469 |
| 10 | 200 ms | 764 | 470 |
| 10, one query connection reserved for 600 ms | immediate | 760 | 271 |

Both variants passed all APPLIED receipt, SENT command, and unique-provider-effect
assertions. The connection reservation overlapped fanout in both measured runs.
The immediate-ringback scenarios removed approximately 487–493 ms of waiting;
the 200 ms ringback scenarios removed 289–293 ms. Ringback itself remains ordered
and takes its actual duration. The candidate's ringback-return-to-first-Dial
interval was 3–7 ms, versus 55–255 ms in the original.

One original ten-Staff case took 1,037 ms to reach the last Dial (279 ms spread),
despite reaching the first at 758 ms. The candidate reached the last at 281 ms
(10 ms spread). That single original tail observation is retained, not treated
as a percentile or independently attributed to a particular lock wait.

The remaining approximately 250 ms receipt poll crosses runtime processes and
is outside this candidate. Provider invocation is not evidence of Staff browser
ringing or a completed patient outcome. No production improvement is claimed.

## Verification and decision

Two virtual-clock regressions first failed on the original Runner with zero
effects before the polling timer advanced. They pass with the candidate without
advancing that timer. Additional cases cover stale/coalesced/false hints,
in-flight claim races, fallback polling, cancellation, and preserved claim and
executor error backoff. The original idle-poll and executor-limit tests remain.

The complete worker suite passed with the race detector using
`GOTOOLCHAIN=go1.26.7 go test -race ./backend/internal/worker -count=1 -v`
(`wakeup-green-race.log`, 1.386 seconds).

The same real-Runner receipt fixture, mixed analytics/receipt/provider workload,
bounded-pool fanout, and provider-command tests also passed with the race detector:

```sh
GOTOOLCHAIN=go1.26.7 \
TEST_DATABASE_URL='postgres://chasefagen@127.0.0.1:55439/acuity_integrated_test?sslmode=disable' \
go test -race -p 1 ./backend/internal/humancalling \
  -run 'TestStaffDialLatencyFromSignedAnswerReceipt|TestMixedInboundCallsReceiptsAndAnalyticsProgressWithBoundedDatabaseCapacity|TestInboundStaffDialFanoutProgressesWithTwoDatabaseConnections|TestProviderCommand' \
  -count=1
```

Evidence lives in `/tmp/acuity-worker-wakeup-20260904/`: `baseline.log`,
`candidate.log`, `wakeup-red.log`, and `integration-race.log`.
An independent source review found no actionable issue in the candidate.

The final complete backend/deployment check also passed, with no skipped database
suite (`backend-final.log`):

```sh
GOTOOLCHAIN=go1.26.7 \
TEST_DATABASE_URL='postgres://chasefagen@127.0.0.1:55439/acuity_integrated_test?sslmode=disable' \
go test -p 1 ./backend/... ./deploy -count=1
```

The evidence supports including local wakeups before merging. This adds no
database connections, migrations, dependencies, public configuration, or new
command owner. Idle query frequency is unchanged without hints. During activity,
successful receipt processing (including receipts that create no commands) and
command completion may cause extra empty scans; the hint buffer coalesces bursts,
and failed or empty scans do not create further hints. Production query volume
and stage latency still need observation after an authorized deployment.
