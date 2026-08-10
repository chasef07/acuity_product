# Provider receipt recovery

Provider receipt recovery starts with aggregate evidence. Never bulk requeue a
quarantine: a replay is safe only after the projection defect is fixed and the
specific receipt is correlated to its Call.

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

## One-receipt recovery gate

For each attached candidate:

1. Fix and deploy the projection defect that created its quarantine group.
2. Open the Call's Platform Operator timeline and inspect the bounded event,
   command, and Call state sequence.
3. Use the timeline's opaque recovery reference with the existing one-receipt
   requeue action. Do not use an event ID or a database update.
4. Confirm the receipt reaches a terminal state and the Call converges without
   a new command, receipt, or CallLeg backlog.
5. Stop the group on the first unexpected transition and investigate before
   selecting another receipt.

The existing requeue transaction verifies Platform Operator authority, locks
one quarantined receipt, records `provider_receipt.requeued` in the access audit
log, and resets only that receipt for worker processing.
