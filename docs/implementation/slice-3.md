# Slice 3 implementation note

Issue [#9](https://github.com/chasef07/acuity_product/issues/9) is the
controlling contract. This slice turns the authenticated shell and deterministic
human-call path into one usable follow-up workspace. It does not expand the
provider acceptance gate recorded for Slice 2.

## Request path and ownership

`Work` owns the Task row, one-to-one Call link, lifecycle Activity, queue
ordering, protected search, optimistic version, and mutation rules.
`HumanCalling` continues to own the canonical phone, transfer reason, call
outcome, and exact-phone Call Engagement History. `Access` remains the sole
authority for current Practice and Location visibility and Platform Operator
write access.

The vertical path is:

1. Abita creates a handoff with an Abita office key, required canonical E.164
   phone, and optional transfer reason. `Access` resolves the current Abita
   Office Route and `HumanCalling` stores the resulting Location in the same
   transaction before returning the generic SIP destination. The former direct
   `locationId` input remains migration-only compatibility during the agent
   cutover; callers must send exactly one route input. The authenticated service
   credential has one primary Practice and may name explicit additional
   Practices for this handoff seam. Product scopes the service identity to the
   requested allowlisted Practice before resolving its Office Route; an
   unlisted Practice fails before any reservation is written.
2. The provider ingress does not depend on custom SIP headers or URI markers.
   It admits exactly one unconsumed, unexpired handoff reservation for the
   transferred caller; ambiguous admissions fail closed.
3. Provider-confirmed termination moves the winning Call to Needs Disposition.
   The disposition dock remains mounted until the winner chooses `Resolved` or
   `Create task`.
4. `Resolved` closes the Call without creating work. `Create task` changes the
   Call outcome and calls `Work.EnsureCallFollowUp` in the same PostgreSQL
   transaction.
5. `Work` creates exactly one open Task linked to that Call, snapshots the
   creating actor, appends `TASK_CREATED`, and records the existing
   Practice-scoped workspace hint. A replay returns the same Task ID.
6. The winning browser selects the returned Task. Other browsers treat the SSE
   message only as a hint, refetch the protected Task query, and preserve their
   current selection. If realtime remains unavailable past its grace period,
   the shell marks updates as delayed and uses bounded jittered authoritative
   polling until the stream recovers.
7. Rename, complete, and reopen commands lock current Access and Task state in
   one transaction, compare the expected Task version, append one Activity, and
   then publish a refetch hint.
8. Task and active-Call views query Engagement History through a trusted
   Task/Call lookup. `HumanCalling` returns only exact-phone Calls in currently
   authorized Locations, newest 20 per page but presented chronologically.

The browser never owns Task truth. PostgreSQL rows and current Access are
authoritative; generated HTTP clients and SSE are adapters.

## Task contract

- A Task is `OPEN` or `COMPLETED`, belongs to one Practice and Location, and is
  linked to exactly one Call.
- Its phone is canonical E.164. Its initial title is the trimmed transfer reason
  or `Follow up on call`.
- Created and completed actors are immutable snapshots. Reopening clears only
  completion fields; it does not replace the Call, Contact Context, creator, or
  title.
- Open Tasks sort oldest first. Completed Tasks follow them newest first. Query
  pages contain at most 50 Tasks.
- Search is submitted in a protected POST body and matches title or normalized
  phone digits. Contact Context is not placed in routes, query strings, SSE
  payloads, notifications, or ordinary logs.
- Completion and reopening are idempotent at the already-achieved state.
  Concurrent attempts serialize under the Task row and create one Activity.
- A stale rename returns conflict. The browser refetches the committed version
  while retaining the attempted title for an explicit retry.
- Practice Users mutate within current membership scope. Platform Operators
  read and mutate globally under their own identity; operator audits are
  committed inside the mutation transaction.

There is no manual Task creation, deletion, assignment, priority, due date,
note, message composer, or right-side context drawer in this slice.

## Workspace behavior

The left rail is dedicated to Tasks. Multi-Location Users default to
`All offices`; single-Location Users see their office directly. The rail keeps
open and completed groups, office labels only when they disambiguate an
all-office view, protected search, and cursor pagination.

The center is one interaction workspace. A newly won Call may take focus only
once. Navigating to a Task does not unmount or reinitialize the softphone, and
subsequent Call polling does not steal selection. After disposition, the
workspace selects the created Task or returns to the previously selected Task.
Task detail exposes only inline rename, complete/reopen, durable actor metadata,
and chronological Call Engagement History.

The visual treatment is a dense monochrome operating surface. Teal is reserved
for Acuity identity, current state, and the timeline marker. Motion is limited
to small state transitions and honors reduced-motion and dark-mode parity.

## Deterministic proof

`TEST_DATABASE_URL=... go test ./backend/...` exercises the Work lifecycle,
current-access reads, direct operator mutation audit, location scope, protected
search, cursor ordering, concurrent transitions, migration, atomic
disposition/Task creation, exact-phone history, authenticated HTTP surface, and
realtime hints against PostgreSQL.

`E2E_DATABASE_URL=... ./scripts/run-e2e.sh` builds the production Next.js and Go
runtimes and runs the Slice 1 and Slice 2 journeys together. The extended
two-browser call journey proves one Task row and creation Activity, winner
auto-focus, loser selection stability, protected phone search, return routing,
cross-browser rename/complete/reopen refresh, and the final Task/Activity rows.

These checks are deterministic product proof. They do not claim new live
Telnyx, LiveKit, browser-audio, public-webhook, or provider-recording evidence.
The controlled Slice 2 live gate remains the boundary before patient routing.
