# Terminal-session orphan receipts

## Failing state

A provider can deliver lifecycle events for extra legs sharing a session with a
Product Call. If those exact legs were never attached, an answered or hangup
event continues as `WAITING_FOR_RELATED_FACT_SLOW_RETRY`, even after the Product
Call and all its legs have ended. The existing retry owner tries every 15 minutes
after ten fast attempts, then quarantines unresolved events after 24 hours.

A synthetic database-backed regression reproduced the failure through
`ProcessNextReceipt`: the ended orphan remained PENDING instead of reaching
`TERMINAL_OR_OBSOLETE_PROVIDER_FACT`.

## Change and safety boundary

When normal projection reports a missing related fact, answered/hangup receipts
without client state can be classified obsolete only if:

- Exactly one Product Call owns the provider session, and that Call has a
  terminal outcome and an end time.
- Every leg of that Call has ended or failed with an end time, and every provider
  command is reconciled or failed.
- No Product leg owns either the incoming control ID or leg ID.
- A read-only Telnyx status response matches the complete control/leg/session
  identity, explicitly reports `is_alive=false`, and supplies a valid end time.
- The Product conditions still hold after the provider read.

The receipt becomes FAILED with the existing obsolete-fact code. The original
provider body remains intact. The classifier neither attaches an event by
session nor changes Call, CallLeg, Task, or provider-command state. A new fixed
`PROJECTION_OBSERVE_ORPHAN_RETRY` code identifies lookup failures rather than
mislabeling them as an application failure. Missing, live, contradictory, or
unavailable provider evidence never establishes an ended leg.

This is limited to orphan answered/hangup events. Other event types, known legs,
client-state correlations, active Calls/legs, unfinished commands, and ambiguous
sessions retain their existing projection paths. The new provider read runs only
after the local terminal-session checks pass.

## Production cleanup evidence

The authorized audit found three orphan receipts in one terminal session: one
answer and two hangups. The matched Product Call was RESOLVED, all three Product
legs were terminal, and all four commands were RECONCILED.

Two receipts refer to the same provider leg. Telnyx confirmed that exact leg had
ended, including the hangup after Product's recorded end time. Each was
terminalized in its own guarded transaction with an existing bound Platform
Operator identity and a `provider_receipt.terminalized` audit entry. Raw payload
checksums and unattached status were preserved; no Call, leg, or command state
was changed. The pending receipt count dropped from three to one.

The remaining receipt is intentionally unresolved. The provider active-call
listing is empty and its event history includes a hangup, but its direct status
response still reports `is_alive=true` with no end time. No live-leg command was
sent and that contradictory evidence was not discarded. This case motivated the
exact status requirement rather than relying on absence from an active list.

Alerts remain disabled at the user's request.

## Verification and release scope

The initial regression failed on the original implementation. Focused adapter
and integration tests cover positive termination evidence, contradictory and
missing provider fields, provider failures, live state, ambiguous sessions,
unfinished commands, exact leg ownership, state changes during provider reads,
raw evidence preservation, and absence of unintended product/provider effects.

Verified locally with Go 1.26.7, `GOMAXPROCS=2`, and the disposable database
`acuity_orphan_receipts_test` on local PostgreSQL:

- Focused regression, provider adapter, unrelated-event retry, parent-arrival,
  and projection-conflict tests: passed (`go test -p 1
  ./backend/internal/humancalling -run
  'Test(TerminalSessionOrphanReceiptStopsRetrying|TelnyxCallEndRequiresExactPositiveEvidence|UnrelatedHangupWaitsForRelatedFact|UnrelatedProviderReceiptStopsRetryingAfterOneDay|ChildReceiptWakesWhenParentAttachesRelatedCall|ProviderProjectionConflictRemainsRetryable)$'
  -count=1`).
- `go test -p 1 ./backend/... ./deploy -count=1`: passed with database-backed
  integration tests enabled. The complete Calling package passed in 52.715 s.
- `go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./backend/...`: no
  vulnerabilities found.
- `git diff --check`: passed.

No browser, OpenAPI, or dependency changes were made. Browser and release-container
checks were not run; those surfaces are unchanged. CI and deployment remain
separate release gates; the local results above do not establish either.
Production receipt cleanup is separate from deployment of this prevention code.
