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

## September 2 recovery guard review

PR #254 was refreshed against `main` at `765b160`, preserving migration 0045
alongside the new analytics migrations (51 migrations in total). This review
changed local code and tests; it performed no new production recovery.

Real PostgreSQL regressions reproduced three unsafe successes in historical
ringtone recovery: an unconfirmed `SENT` command, bridge-only `outbound_media`
evidence with no hangup command, and another receipt quarantined after timeline
inspection. Each incorrectly changed the selected receipt to `APPLIED`. A
separate CLI regression reproduced success with a `SENT` command or a terminal
outcome lacking an end time.

Recovery now rejects all three cases without changing the receipt, its raw
evidence or attempt count, or writing a projected fact or success audit. Its
allowed client states are only `cleanup` and `staff_hangup`. CLI verification
requires the end time and includes `SENT` among unresolved commands. Its SQL is
expanded into explicit checks for easier review. The two valid hangup journeys
still recover successfully after provider bridge/hangup facts reconcile their
commands; ordinary runtime `outbound_media` handling remains intact.

Focused verification (5 module scenarios and 6 CLI scenarios) passed using
`acuity_pr254_review_test`, a disposable local PostgreSQL database:

```sh
GOTOOLCHAIN=go1.26.7 TEST_DATABASE_URL='postgres://postgres@127.0.0.1:55434/acuity_pr254_review_test?sslmode=disable' \
  go test -p 1 ./backend/internal/humancalling ./backend/cmd/backlog-recovery \
  -run 'TestOutboundRingtoneEndedUsesExactHangupCommand|TestCallingVerificationRequiresCommittedTerminalEvidence' \
  -count=1 -v
```

The full serial `go test -p 1 ./backend/... ./deploy -count=1` suite passed
against the same disposable database with Go 1.26.7. Affected-package `go vet`
and `govulncheck@v1.7.0 ./backend/...` passed. With pnpm 10.34.5, frontend lint,
typecheck, all 243 unit/render tests, the production build, and the production
dependency audit passed. `git diff --check` passed. The updated PR reruns CI,
including browser journeys, generated contracts, and release-container checks.

### Standards review

Both safety findings (ignored `SENT` work and bridge-only historical recovery)
are resolved. No blocking standards or simplicity finding remains. Keeping the
existing shared ringtone validator avoids a second validation implementation.
Moving the separate acknowledgement retirement policy from the CLI into Work
remains a nonblocking ownership cleanup, outside this quarantine correction.

### Spec review

The command, hangup-evidence, and sibling-quarantine guards now match the
runbook's existing contract. No additional blocking correctness or scope gap
was found. The independent reviewers inspected code without sharing the test
database. Local tests and review do not prove deployment or clear production
quarantines; historical items still require individual evidence checks.
