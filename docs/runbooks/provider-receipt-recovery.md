# Provider receipt recovery

Provider receipt recovery starts with aggregate evidence and advances one
attached receipt at a time. Never bulk requeue a quarantine: replay is safe only
after the projection defect is fixed and the exact audited group is correlated
to one Call. A smaller quarantine metric is not success when a Call, CallLeg,
provider command, audit, or original receipt failure no longer agrees.

The controlled drain uses four Platform Operator HTTP interfaces:

- candidate selection returns the oldest attached receipt from one exact
  `eventType` and bounded `errorCode` group without changing it;
- status follows that exact opaque receipt reference across state changes;
- requeue accepts exactly one quarantined attached receipt; and
- `UNSAFE_TO_REPLAY` terminally resolves exactly one attached receipt while
  preserving its original evidence and projection error.

There is no bulk interface and no authorization path for unattached receipts.
Do not guess a Call, force an attachment, delete evidence, or use direct SQL to
change receipt state.

## 1. Capture the read-only aggregate audit

Use a separately authorized database URL through a local Cloud SQL Auth Proxy.
The command opens one connection, executes a read-only transaction, and emits
only state, event type, safe projection error code, Call attachment, attempts,
counts, and ages. It never reads or emits raw webhook bodies, receipt IDs, Call
IDs, phone numbers, or provider IDs.

```sh
AUDIT_DATABASE_URL='postgresql://...' \
  go run ./backend/cmd/receipt-audit
```

Save the sanitized output with the incident evidence. Before selecting a
candidate, confirm all of the following:

1. active `PENDING` plus `PROCESSING` depth is stable and understood;
2. the exact attached group is homogeneous by `eventType`, bounded `errorCode`,
   attempts, and age;
3. the projection defect for that group is fixed and deployed; and
4. every `attachedToCall=false`, `UNCLASSIFIED`, mixed, or unexplained group is
   recorded separately and excluded from the drain.

Stop if any evidence is missing. An unattached receipt remains an unresolved
group; it must not be attached to a guessed Call.

## 2. Select one candidate without changing it

Load a short-lived Platform Operator bearer token through the approved
credential mechanism. Do not paste the token into incident notes, logs, or
command output. Set `PORTAL_API_URL` without a trailing slash and set the exact
Practice and audited group values:

```sh
export PORTAL_API_URL='https://portal-api.example'
export PRACTICE_ID='00000000-0000-0000-0000-000000000000'
export EVENT_TYPE='call.answered'
export ERROR_CODE='PROJECTION_RETRY_EXHAUSTED'
export OPERATOR_TOKEN='...'
```

Select one oldest attached candidate from only that group:

```sh
curl --fail-with-body --silent --show-error --get \
  --header "Authorization: Bearer ${OPERATOR_TOKEN:?}" \
  --data-urlencode "eventType=${EVENT_TYPE:?}" \
  --data-urlencode "errorCode=${ERROR_CODE:?}" \
  "${PORTAL_API_URL:?}/v1/operator/practices/${PRACTICE_ID:?}/provider-receipts/quarantine-candidate"
```

Record the sanitized response. Manually copy its `receiptReference` and
`callId`; do not run a loop or select another candidate:

```sh
export RECEIPT_REFERENCE='copy-the-43-character-reference'
export CALL_ID='copy-the-call-id'
```

The candidate must match the requested Practice, `eventType`, and `errorCode`,
must have projection-attempt evidence, and must identify one attached Call.
Record its age and `remainingGroupCount` as the group baseline.

## 3. Read status and Call evidence before writing

Read the current status for only the copied reference:

```sh
curl --fail-with-body --silent --show-error \
  --header "Authorization: Bearer ${OPERATOR_TOKEN:?}" \
  "${PORTAL_API_URL:?}/v1/operator/practices/${PRACTICE_ID:?}/provider-receipts/${RECEIPT_REFERENCE:?}"
```

Read the selected Call's sanitized Platform Operator timeline:

```sh
curl --fail-with-body --silent --show-error \
  --header "Authorization: Bearer ${OPERATOR_TOKEN:?}" \
  "${PORTAL_API_URL:?}/v1/operator/calls/${CALL_ID:?}/timeline"
```

Save both responses as the before evidence. Stop before a write unless:

- the status Practice, Call, reference, event type, and error code exactly match
  the selected candidate;
- receipt state is `QUARANTINED` and its attempts are explained;
- every provider command state is understood, with none `AMBIGUOUS` or `FAILED`;
- the duplicate count and Call/CallLeg sequence are explained; and
- active and quarantine depths have not grown unexpectedly.

## 4A. Requeue exactly one receipt proven safe to replay

This request mutates only the copied reference and atomically records one
`provider_receipt.requeued` Platform Operator audit:

```sh
curl --fail-with-body --silent --show-error \
  --request POST \
  --header "Authorization: Bearer ${OPERATOR_TOKEN:?}" \
  --header 'Content-Type: application/json' \
  --data '{}' \
  "${PORTAL_API_URL:?}/v1/operator/practices/${PRACTICE_ID:?}/provider-receipts/${RECEIPT_REFERENCE:?}/requeue"
```

A successful acceptance returns that same reference in `PENDING`. Do not treat
the HTTP response as convergence proof.

Manually repeat only the status request from step 3 for the same
`RECEIPT_REFERENCE`. Never use a shell loop, `watch`, `xargs`, or a command that
selects the next candidate. Wait for `APPLIED`, stopping immediately on
`QUARANTINED`, `UNKNOWN`, `FAILED`, timeout, or any HTTP/database failure.

After `APPLIED`, read the Call timeline again and require all of this proof:

1. duplicate count is unchanged;
2. requeue audit count increased by exactly one;
3. no provider command is `AMBIGUOUS` or `FAILED`;
4. active depth returned to or below baseline and quarantine depth decreased;
5. Call and CallLeg transitions are expected and no provider command/effect was
   duplicated; and
6. a fresh aggregate audit shows no backlog growth or new unexplained group.

Only after a human accepts all six checks may a new invocation of step 2 select
another receipt.

## 4B. Resolve exactly one receipt proven unsafe to replay

Use terminal resolution only when retained evidence proves replay is unsafe or
obsolete and the incident record explains why. Repeat steps 2 and 3 with the
same selected reference, then send the only accepted bounded intent:

```sh
curl --fail-with-body --silent --show-error \
  --request POST \
  --header "Authorization: Bearer ${OPERATOR_TOKEN:?}" \
  --header 'Content-Type: application/json' \
  --data '{"resolution":"UNSAFE_TO_REPLAY"}' \
  "${PORTAL_API_URL:?}/v1/operator/practices/${PRACTICE_ID:?}/provider-receipts/${RECEIPT_REFERENCE:?}/resolve"
```

The response must contain the same reference in `FAILED`. Read status again for
only that reference and require:

1. the status-visible original error code, attempts, and duplicate evidence
   remain unchanged;
2. resolution audit count increased by exactly one and requeue audit count did
   not change;
3. quarantine depth decreased by one and active depth did not grow; and
4. the Call and CallLeg projection did not change.

The safe status interface intentionally does not expose the raw receipt body.
The HumanCalling transaction preserves that body, last-attempt evidence, and
original projection error; database-backed tests are the application proof of
that invariant. Do not broaden the operator response or print raw evidence to
make it directly observable.

Resolution is not available for unattached receipts. Keep those groups visible
and escalate the missing correlation or authorization decision instead of
making the quarantine metric zero.

## Stop conditions

Stop the group immediately on any of these observations:

- candidate ambiguity, an empty or mixed group, unexpected bounded values, or a
  cross-Practice mismatch;
- receipt re-quarantine, `UNKNOWN`, `FAILED` after requeue, timeout, or database
  or HTTP failure;
- duplicate-count growth, provider-command `AMBIGUOUS` or `FAILED`, a rejected
  or duplicate provider effect, or an unexpected Call/CallLeg transition;
- active or quarantine backlog growth, a new audit mismatch, or a new aggregate
  group; or
- any unattached receipt, insufficient retained evidence, or uncertainty that
  replay is safe.

Do not retry through a stronger tool, direct database update, deletion, bulk
request, forced attachment, or automated next-candidate selection. Preserve the
candidate, before/after status, timeline, aggregate audit, and incident decision
as the recovery proof.
