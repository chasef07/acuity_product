# Provider receipt audit — 2026-08-11

## Verdict

Do not requeue the production quarantine yet. The durable queue is still
growing, almost every attempted projection is retrying, and current production
telemetry does not preserve enough bounded cause detail to select a safe
quarantine group.

## Live evidence

Read-only Google Cloud evidence collected at `2026-08-10T22:12:20Z`:

- queue depth: `425`;
- oldest pending or processing receipt: `176152` seconds, about 48.9 hours;
- quarantined depth: `1022`;
- processing outcomes since `2026-08-10T21:40:00Z`: `942 retry`,
  `3 quarantined`, and `2 applied`.

Worker errors since `2026-08-10T12:00:00Z` were independently aggregated from
Cloud Logging without identifiers or payloads:

- `357` provider command failures classified `TELNYX_CALL_ENDED`;
- `219` stale reconciliation failures reporting contradictory Telnyx Call event
  identity;
- `164` AI Interaction receipt failures caused by the separate
  `access_locations` lock permission defect;
- `11` stale reconciliation transport failures;
- `2` provider authentication rejections.

These errors do not establish which error created each provider receipt
quarantine. Receipt processing collapses an unexpected projection failure to
`PROJECTION_RETRY`, then stores `PROJECTION_RETRY_EXHAUSTED` after ten attempts.
The existing queue metric exposes only total and quarantined depth.

## Database limitation

A read-only aggregate SQL audit was attempted through the production Cloud SQL
Auth Proxy and the migration database secret. The proxy refused the connection
because the local Google credential requires reauthentication:

`invalid_grant: reauth related error (invalid_rapt)`

No database query executed and no production state changed.

## Branch outcome

This branch adds a read-only aggregate audit operation and JSON command. It
groups receipts by bounded state, event type, safe error code, Call attachment,
attempt range, count, and age. It does not read or return receipt IDs, Call IDs,
provider IDs, phones, or raw webhook bodies.

After Google reauthentication, run the command through a local Cloud SQL Auth
Proxy and use its groups to select the first attached quarantine for the
one-receipt recovery process in
[`docs/runbooks/provider-receipt-recovery.md`](../runbooks/provider-receipt-recovery.md).
