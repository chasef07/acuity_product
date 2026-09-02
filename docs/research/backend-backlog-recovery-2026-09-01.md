# Production backlog recovery — September 1, 2026

The three explicitly requested production counts are zero. Final read-only,
repeatable-read database verification completed at **2026-09-02 03:27:06 UTC**
(September 1, 8:27 p.m. PDT) in `acuity-health-prod` / `acuity-production`.

| Target | Before | After | Disposition |
| --- | ---: | ---: | --- |
| Pending acknowledgement texts | 5 | 0 | Five unsent, week-old attempts retired; all five patient Tasks remain OPEN |
| Calling receipt quarantine | 22 | 0 | Exact terminal ringtone evidence applied individually; all Calls remain UNANSWERED |
| AI receipt quarantine | 133 | 0 | 96 supported receipts recovered; 37 obsolete SUMMARY receipts explicitly retired |

## Durable proof

- 5 `task_acknowledgement.retired` operator audits; every corresponding row is
  `NOT_NEEDED`, has `HISTORICAL_ACKNOWLEDGEMENT_SUPPRESSED`, and has neither a
  Message nor a next retry.
- 22 `provider_receipt.recovered` audits; each receipt is `APPLIED`, retains its
  10 original attempts, and belongs to an `UNANSWERED` Call.
- 96 `ai_interaction.source_clock_recovered` audits. The recovered closeouts
  establish 39 Interaction outcomes: 31 COMPLETED, 7 ESCALATED, and 1 FAILED.
  Only the known source-clock drift was corrected during projection; canonical
  source identity, original receipt payloads and fingerprints were preserved.
- 37 `ai_interaction.legacy_receipt_retired` audits. Each original SUMMARY row is
  `RETIRED`, preserves its `SOURCE_CONFLICT` error and original evidence, links
  its now-terminal Interaction, and has no projection timestamp. It is not
  presented as successfully projected runtime work.
- Calling active receipts, active commands and nonterminal CallLegs: all **0**.
- Calling receipt rows: **71,513**, unchanged from the initial audit.
- AI receipt rows: **17,016**, unchanged from the initial audit.

No provider command or SMS was sent by the recovery tool. Each item committed
with its operator audit and was verified before advancing to the next item.

Forward migration `0045_retired_legacy_interaction_receipts.sql` was applied at
03:23:48 UTC. It adds the explicit retired disposition only for historical
SUMMARY evidence. Prior runtimes already reject that message kind and exclude
it from worker selection, so the migration is compatible with the serving
runtime. No runtime grants were widened.

## Cause and prevention

Terminal ringtone callbacks used the exact hangup command's client state
(`cleanup` or `staff_hangup`), but the projector accepted only the preceding
`outbound_media` state. The local fix accepts that terminal callback only when
its provider leg/session, original ringtone bridge, and exact hangup command
all agree. It does not advance Call outcomes or request another provider effect.

Historical Agent retries regenerated `startedAt` while reusing the same source
Call identity. The operator recovery method permits only that clock correction,
retains normal outcome conflict checks, and leaves ordinary ingestion guards
unchanged.

The five acknowledgement attempts were explicitly retired as obsolete. The two
Locations still lack messaging sender configuration; this operation does not
enable SMS there. The backend prevention change limits configuration retries
to once per minute inside a five-minute send window. Unsent attempts then leave
the executable queue with their failure reason visible and the Task unchanged.
The same deadline is checked before a queued acknowledgement starts its provider
write, preventing a delayed worker from sending an outdated acknowledgement.

The requested PR scope is Product only: the terminal ringtone fix, bounded
acknowledgement attempts, audited historical recovery methods/tooling, and the
already-applied forward migration. No new alerts or Agent changes are included.

## Validation and release state

All commands ran in the isolated `codex/backend-backlog-recovery` worktree with
a disposable local database named `acuity_backlog_test`:

```sh
GOTOOLCHAIN=go1.26.7 TEST_DATABASE_URL='postgres://postgres@127.0.0.1:55434/acuity_backlog_test?sslmode=disable' \
  go test -p 1 ./backend/... ./deploy -count=1
GOTOOLCHAIN=go1.26.7 go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./backend/...
GOTOOLCHAIN=go1.26.7 go vet ./backend/cmd/backlog-recovery ./backend/internal/interaction ./backend/internal/humancalling
git diff --check
```

All passed. The first full run correctly caught the migration-count assertion
and CI shard inventory needing updates for the new migration and command; both
were corrected and the entire suite passed on the second run. Recovery tests
cover both hangup paths, wrong provider identity, ordinary-Staff denial,
immutable evidence, idempotency, clock-only correction, conflicting outcomes,
and refusal to retire SUMMARY before a supported terminal CLOSEOUT exists.

Production data recovery and the forward migration are complete. The runtime
prevention changes require a separately verified release. Local and CI proof
belong to the PR; neither proves the runtime has been deployed. The applied
migration is included here; do not create a competing 0045 migration.

## Separate evidence gaps

34 other AI Interactions have no closeout receipt and remain unresolved. The
legacy portal database has no records after August 10 and could not supply their
missing reports. No outcome was invented to clear those records.

The three unavailable recording placeholders belong to calls with no bridged
leg (one ABANDONED, two UNANSWERED). Exact Telnyx recording and recording-error
lookups returned no recordings or errors for their relevant legs. No missing
audio was falsely marked recovered.

Operational procedure: [audited backlog recovery](../runbooks/backend-backlog-recovery.md).
