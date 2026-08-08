# Production runtime capacity contract

This is the checked production target for Acuity's observed traffic and 5–10
logged-in call-center Staff. It is not evidence that a Cloud Run or Cloud SQL
environment exists. The machine-readable values live in
`production-runtime-contract.json`; tests verify the topology, resources,
billing mode, recovery controls, and connection arithmetic.

Production defaults to `us-east1` (South Carolina) because current users are in
Florida. Cloud Run services, the worker pool, Cloud SQL, recording storage, and
other regional dependencies stay co-located there. This geography is a starting
assumption. Measured Florida-to-`us-east1` latency remains a live release gate.

## Database

| Setting | Checked value |
| --- | --- |
| Product | Cloud SQL for PostgreSQL 16 |
| Edition | Enterprise, not Enterprise Plus |
| Availability | Single-zone (`ZONAL`), no automatic failover |
| Compute | 2 vCPU / 8 GiB |
| Storage | 50 GiB SSD initially, automatic growth on, alert before growth |
| Recovery | Daily automated backup at 04:00 UTC in `us-east1`, seven retained backups, seven days of PITR logs |
| Protection | Deletion protection on |
| Data cache | Off |

The accepted availability tradeoff is explicit: a database or zone outage does
not automatically fail over. Telnyx retries, committed receipts, stable command
IDs, and durable worker recovery protect correctness, but portal and call
control can remain unavailable until the database recovers or operators restore
it. A restore rehearsal is required before launch and after material recovery
changes.

`cloud-sql-commands.example.sh` consumes the checked database row. It is an
operator command, not an automated deployment and is never run by tests.

## Runtime floor and burst bounds

Every service and job is pinned to 1 vCPU / 512 MiB. Request services use
request-based billing (`--cpu-throttling`); the fixed worker pool and migration
job use instance-based billing. Maximum instance counts preserve PR #15's burst
capacity while the warm floor and local pools reflect the measured pilot load.

| Runtime | Kind | Billing | Concurrency | Min | Max | Pool max | Direct |
| --- | --- | --- | ---: | ---: | ---: | ---: | ---: |
| web / Better Auth | service | request | 40 | 1 | 2 | 1 | 0 |
| `portal-api` | service | request | 20 | 1 | 3 | 1 | 0 |
| `provider-ingress` | service | request | 20 | 1 | 2 | 1 | 0 |
| `realtime` | service | request | 50 | 1 | 2 | 1 | 1 `LISTEN` |
| `worker` | worker pool | instance | n/a | 1 fixed | 1 fixed | 1 | 0 |

The realtime service explicitly keeps Cloud Run's request timeout at 300
seconds. The application rotates each SSE stream between 240 and 270 seconds
(`streamMaximumSeconds=270`, `streamJitterSeconds=30`), preserving at least a
30-second shutdown margin below the platform deadline and spreading planned
reconnections across instances.

The web keeps one instance warm because it is the public website and owns the
authentication entrypoint. `portal-api`, `provider-ingress`, and `realtime`
also keep one instance warm. The worker keeps one fixed instance. The runtime
roles remain separate because their failure owners remain separate; saving a
few dollars is not a reason to couple ingress acknowledgement or durable
recovery to portal traffic.

## Exact PostgreSQL reservation

Request services are bounded by service-level maximums across all traffic-split
revisions in ordinary operation:

```text
web                 2 × (1 + 0) = 2
portal-api          3 × (1 + 0) = 3
provider-ingress    2 × (1 + 0) = 2
realtime            2 × (1 + 1) = 4
                                     --
configured request-service demand    11
```

Only the worker pool gets a rollout-overlap multiplier:

```text
request services under configured service caps  11
two worker revisions                  2 × 1 =     2
one migration task, pool max 1                   1
one extra instance per request role               5
operator/database-operations headroom            3
                                                  --
required usable application connections         22
```

Cloud Run documents that rapid traffic spikes or maintenance can temporarily
exceed a configured maximum. The five-connection autoscaler allowance covers
one extra web, portal, ingress, and realtime instance, including realtime's
direct listener. It is explicit headroom, not a claim that the platform cannot
overshoot it. The three operator connections provide two simultaneous
diagnostic sessions and one recovery session. Autovacuum uses PostgreSQL worker
slots rather than these client connections. Deployment reads the actual
`max_connections` value and active reserved usage; it stops before any `gcloud`
call unless at least 22 connections are measured usable. Live maximum-instance
overshoot and pool saturation remain acceptance gates.

Every runtime pool uses `MinConns=0`, its checked `DATABASE_POOL_MAX`, a 1500 ms
acquisition/connect timeout, a five-minute idle limit, and bounded connection
lifetime jitter. The migration job uses one task, pool max 1, a 5000 ms timeout,
and no automatic retry. Realtime's dedicated `LISTEN` connection is outside its
pool and is counted once for every allowed realtime instance.

Listener generations are part of the runtime contract: loss or recovery of the
PostgreSQL listener closes every stream from the old generation. Browsers then
reconnect and reconstruct authority over HTTP; no client may remain apparently
live while its runtime is blind to `NOTIFY` wake-ups.

## Deterministic acceptance evidence

Observed legacy traffic peaked at seven webhook requests in one visible second,
35 in the busy minute, and about 5,544 events/day. Events are not Calls. The
checked proof deliberately exceeds that observation:

- 25 simultaneous correctly signed requests traverse the real
  `provider-ingress` HTTP handler with its 1.5-second deadline and one database
  connection;
- every response is `204`, duplicates converge, and the 25-request nearest-rank
  p99 (the burst maximum) must stay below one second;
- ten simultaneous Staff commands run through the calling module on a
  one-connection portal pool;
- portal and realtime authorization database paths remain responsive while
  ingress is observably blocked; and
- one worker connection keeps receipt and command lanes moving while a provider
  command is held.

Ten repeated runs on 2026-08-06 passed at the one-connection portal floor. HTTP
webhook burst maxima were about 20–66 ms, mixed-role webhook p99 was about
16–169 ms, and ten-command p99 was about 10–41 ms. Cumulative portal pool wait
during each ten-command window was about 80–334 ms. These are deterministic
local PostgreSQL 16 measurements, not
Cloud Run, Cloud SQL, network, Florida-user, or Telnyx production evidence.

## Operator use

Production runtime rendering and its fail-closed capacity check:

```sh
ACUITY_DEPLOYMENT_PROFILE=production \
USABLE_DATABASE_CONNECTIONS=22 \
MESSAGING_ATTACHMENT_BUCKET_LOCATION=us-east1 \
  ./deploy/cloud-run-commands.example.sh
```

The command also rejects a Cloud SQL connection name or Messaging attachment
bucket location outside `us-east1`.

Messaging adds one shared private Cloud Storage volume mounted at
`MESSAGING_ATTACHMENT_DIRECTORY` by `portal-api`, `provider-ingress`, and
`worker`. Those three service accounts need only the object permissions their
runtime path exercises. Provider ingress mounts the volume read-only and
receives the media-signing secret; the worker receives that secret plus the
public media and webhook bases. Production rollout must verify cross-runtime
attachment visibility, bucket IAM, retention/backup policy, and signed URL
expiry before traffic shifts.

See `production-runbook.md` for the deployment, restore rehearsal, rollback,
and live acceptance sequence, and `production-cost-estimate.md` for the
rate-card model.

Remaining release gates are live Cloud Run/Cloud SQL load and latency, measured
Florida-to-`us-east1` portal/call-control latency, Cloud SQL backup/PITR restore,
actual usable connection capacity, rolling-revision behavior, cross-runtime
Messaging storage, and live Telnyx delivery/retry/reconciliation. This contract
claims none of those are complete.
