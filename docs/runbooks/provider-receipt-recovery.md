# Provider receipt recovery

Provider receipt recovery starts with aggregate evidence and advances one
attached receipt at a time. Never bulk requeue a quarantine: a replay is safe
only after the projection defect is fixed and the exact audited group is
correlated to one Call. A smaller quarantine metric is not success when a Call,
CallLeg, command, audit, or original receipt failure no longer agrees.

The controlled drain has four interfaces:

- `receipt-audit` is the read-only aggregate authority, including the separate
  `attachedToCall=false` groups;
- the Platform Operator candidate read selects one oldest attached receipt from
  an exact `eventType` and bounded `errorCode` group;
- the one-receipt status read follows that same opaque reference across state
  changes and returns bounded Call, CallLeg, provider-command, backlog, duplicate,
  and audit counts; and
- the existing requeue and unreplayable resolution writes each accept exactly
  one opaque receipt reference.

There is no unattached-receipt authorization path. Do not guess a Call, force an
attachment, or use direct SQL to move an unattached receipt.

## Aggregate audit

Use a separately authorized database URL through a local Cloud SQL Auth Proxy.
The command opens one connection, executes a read-only transaction, and emits
only state, event type, safe projection error code, Call attachment, attempts,
counts, and ages. It never reads or emits raw webhook bodies, receipt IDs, Call
IDs, phone numbers, or provider IDs.

```sh
AUDIT_DATABASE_URL='postgresql://...' \
  go run ./backend/cmd/receipt-audit
```

Group the output by `errorCode`, `eventType`, and `attachedToCall`. An unattached
receipt cannot use the current operator recovery endpoint and must not be forced
onto a guessed Call.

Save the sanitized output with the incident evidence. Before every one-receipt
action, confirm all of the following:

1. active `PENDING` plus `PROCESSING` depth is stable and understood;
2. the exact attached group is homogeneous by `eventType`, bounded `errorCode`,
   attempts, and age;
3. the projection defect for that group is fixed and deployed; and
4. every `attachedToCall=false`, `UNCLASSIFIED`, mixed, or unexplained group is
   recorded separately and excluded from the drain.

## Select one candidate without changing it

Use a short-lived Platform Operator bearer token. Keep it in the environment;
never put it in shell history or command output.

```sh
OPERATOR_TOKEN='...' \
  go run ./backend/cmd/receipt-recovery \
  --base-url='https://portal-api.example' \
  --practice-id='00000000-0000-0000-0000-000000000000' \
  --event-type='call.answered' \
  --error-code='PROJECTION_RETRY_EXHAUSTED' \
  --action='requeue'
```

Without `--apply`, the command is read-only. It selects exactly one oldest
attached candidate, reads that candidate's status, prints sanitized JSON, and
exits. It never selects a second candidate in the same invocation.

Record the returned `callId`, `receiptReference`, attempts, age,
`remainingGroupCount`, receipt state, duplicate count, Call state/version,
CallLeg state counts, command state counts, Practice-attached active/quarantine
counts, and requeue/resolution audit counts. Then read the Call's Platform
Operator timeline:

```text
GET /v1/operator/calls/{callId}/timeline
```

Stop before a write if the candidate is not `QUARANTINED`, the status does not
match the exact selected group, any provider command is `AMBIGUOUS` or `FAILED`,
the Call/CallLeg sequence is not understood, the duplicate count is unexplained,
or the aggregate audit changed while reviewing the candidate.

## One-receipt recovery gate

For a receipt proven safe to replay, rerun the same command with explicit apply
intent:

```sh
OPERATOR_TOKEN='...' \
  go run ./backend/cmd/receipt-recovery \
  --base-url='https://portal-api.example' \
  --practice-id='00000000-0000-0000-0000-000000000000' \
  --event-type='call.answered' \
  --error-code='PROJECTION_RETRY_EXHAUSTED' \
  --action='requeue' \
  --apply
```

The command selects once, requeues only that reference, polls only that
reference, and exits after it reaches `APPLIED` or a stop condition. The requeue
transaction verifies Platform Operator authority, locks one attached
`QUARANTINED` receipt inside the requested Practice, writes exactly one
`provider_receipt.requeued` audit, and resets only that receipt to `PENDING`.

After `APPLIED`, require all of this proof before another invocation:

1. the duplicate count did not change;
2. the exact receipt's requeue audit count increased by one;
3. no provider command is `AMBIGUOUS` or `FAILED`;
4. Practice-attached active depth returned to or below its baseline and
   quarantine depth decreased;
5. the new Call timeline contains no duplicate command/effect and the resulting
   Call and CallLeg transitions are the expected transitions for this audited
   group; and
6. a fresh `receipt-audit` shows no aggregate backlog growth or new unexplained
   group.

`requiresTimelineReview=true` means the Call state/version or CallLeg state
counts changed. The command deliberately does not decide whether that domain
transition was expected. Review the timeline and provider evidence, then stop or
start a new one-receipt invocation. The summary cannot by itself prove a
provider effect was not duplicated.

## Resolve one receipt proven unsafe to replay

Use terminal resolution only when retained evidence proves replay is unsafe or
obsolete and the incident record explains why. Inspect first with
`--action=resolve` and no `--apply`. Then run:

```sh
OPERATOR_TOKEN='...' \
  go run ./backend/cmd/receipt-recovery \
  --base-url='https://portal-api.example' \
  --practice-id='00000000-0000-0000-0000-000000000000' \
  --event-type='call.answered' \
  --error-code='PROJECTION_RETRY_EXHAUSTED' \
  --action='resolve' \
  --apply
```

The HTTP adapter sends the bounded intent `UNSAFE_TO_REPLAY`. HumanCalling locks
exactly one currently `QUARANTINED`, attached receipt, changes only its terminal
and quarantine scheduling state, preserves `raw_body`, the original
`projection_error_code`, attempts, last-attempt evidence, and projected time,
and atomically writes `provider_receipt.resolved_unreplayable`. The command then
requires `FAILED`, unchanged error/attempt/duplicate evidence, a one-count audit
increment, and decreased Practice quarantine depth.

Resolution is not available for unattached receipts. Keep those groups visible
and escalate the missing correlation/authorization decision instead of making
the metric zero.

## Stop conditions

Stop the group immediately on any of these observations:

- candidate ambiguity, empty/mixed group, unexpected bounded values, or a
  cross-Practice mismatch;
- receipt re-quarantine, `UNKNOWN`, `FAILED` after requeue, timeout, or database/
  HTTP failure;
- duplicate-count growth, provider-command `AMBIGUOUS`/`FAILED`, a rejected or
  duplicate provider effect, or an unexpected Call/CallLeg transition;
- active or quarantine backlog growth, a new audit mismatch, or a new aggregate
  group; or
- any unattached receipt, insufficient retained evidence, or uncertainty that
  replay is safe.

Do not retry through a stronger tool, direct database update, deletion, bulk
request, or forced attachment. Preserve the candidate, before/after status,
timeline, aggregate audit, and incident decision as the recovery proof.
