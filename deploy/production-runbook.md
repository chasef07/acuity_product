# Production deployment and recovery runbook

This runbook operates the checked lean production contract. The contract and
commands do not by themselves prove live acceptance; dated rollout evidence and
remaining gates are recorded separately below.

## Migration checkpoint: 2026-08-06

- The isolated `us-east1` stack exists with the checked Cloud SQL shape, four
  Cloud Run services, one worker-pool instance, regional buckets, secrets, and
  Artifact Registry. Service liveness/readiness and the web sign-in database
  path passed.
- The database contains the current schema plus one required Practice,
  Location, voice-number mapping, and Messaging-profile mapping. It contains no
  migrated users, invitations, calls, messages, or patient history.
- Telnyx Call Control and Messaging profile webhook URLs target the
  `us-east1` provider ingress. No live Call, SMS/MMS, retry, or signed burst was
  generated as part of the migration.
- The prior `us-central1` stack remains running as an isolated rollback asset.
  It must not receive fallback webhooks because it has a separate database.
- A successful automated backup and a successful on-demand backup exist in
  `us-east1`. A restore rehearsal is still required before launch.
- PostgreSQL reports 400 total connections and three superuser-reserved slots:
  a 397-connection non-superuser ceiling. The 22-connection production
  reservation therefore has 375 connections of configured ceiling headroom;
  eight client connections were active at the dated snapshot.

This checkpoint is infrastructure and configuration evidence, not Florida
latency, carrier delivery, continuous availability, or end-to-end Staff proof.

## Current automated release

The checked production stack runs in `acuity-health-prod` / `us-east1`.
The former `us-central1` pilot remains isolated as a temporary rollback asset;
ordinary releases target only `us-east1` and never copy data between regions.

Every push to GitHub `main` now follows one release path:

1. GitHub Actions runs the Go, web, generated-contract, and browser suites.
2. GitHub exchanges its `main`-bound identity for the
   `acuity-product-cloud-deploy` Google service account; there is no stored
   Google service-account key.
3. Cloud Build creates backend and web images tagged with the full Git commit
   SHA and resolves them to immutable digests.
4. `acuity-migrate` applies forward migrations and the reviewed runtime grants.
5. Backend services and the worker stage on the digest, become ready, and
   promote before web is released last.
6. Any post-promotion smoke failure returns request traffic and the worker split
   to the revisions captured at the start of the release. Expanded migrations
   remain in place.

Operators do not run `pnpm` or a deployment script for an ordinary release.
Push the reviewed commit to `main`, then follow the GitHub Actions run and its
linked Cloud Build. The web URL is
`https://acuity-web-cbuqwpsdsq-ue.a.run.app`.

## Owners and stop conditions

- `provider-ingress` owns signature verification and durable receipt before
  `204`. Stop if live acknowledgement p99 is one second or more.
- `portal-api` owns authenticated Staff commands and immediate call control.
  Stop if ten-Staff command or Florida-user latency misses the approved gate.
- `realtime` owns disposable version hints. Its failure may reduce freshness but
  must not lose durable state.
- `worker` owns receipt projection, provider-command retry, and reconciliation.
  Stop if either receipt or command lane stalls on one fixed instance.
- Cloud SQL is the sole durable authority. Stop if fewer than 22 connections are
  usable after current reserved and active sessions.

Do not collapse roles to mitigate a failed gate. Record pool wait, transaction
latency, CPU, memory, instance count, and queue depth; raise only the smallest
measured constraint and recalculate the connection reservation.

## Preflight

1. Render and review the checked contract:

   ```sh
   node deploy/render-production-runtime-contract.mjs \
     deploy/production-runtime-contract.json
   ```

2. Confirm `us-east1` for Cloud SQL, every Cloud Run service, the worker pool,
   the recording and Messaging attachment buckets, Artifact Registry, and any
   dependent regional resources. The runtime command independently rejects a
   mismatched Cloud SQL connection name or
   `MESSAGING_ATTACHMENT_BUCKET_LOCATION`.
3. Record the current Cloud SQL settings and query:

   ```sql
   SELECT setting::int AS max_connections
   FROM pg_settings
   WHERE name = 'max_connections';

   SELECT count(*) AS active_client_connections
   FROM pg_stat_activity
   WHERE backend_type = 'client backend';
   ```

   Prove at least 22 connections remain usable by the application and operator
   identities. Do not infer capacity from the machine size alone.
4. Confirm immutable backend and web image digests, exact database credentials
   per role, Secret Manager references, recording and Messaging attachment
   bucket IAM/retention, cross-runtime attachment visibility, alert notification
   delivery, and a deployable prior revision.
5. Confirm a current successful automated backup and a PITR recovery window.
6. Confirm Telnyx webhook retry/failover configuration and the rollback owner.

## Database creation target

`cloud-sql-commands.example.sh` renders the checked Enterprise, zonal,
2-vCPU/8-GiB, 50-GiB SSD PostgreSQL target. Running it creates a billable cloud
resource and requires an approved production change:

```sh
GCP_PROJECT=... \
CLOUD_SQL_INSTANCE_NAME=acuity-production \
  ./deploy/cloud-sql-commands.example.sh
```

The target enables automated backups stored in `us-east1`, seven days of PITR
logs, seven retained backups, deletion protection, and storage auto-increase.
Data cache is off. Alert at 70% disk use and investigate growth before the
automatic increase changes the cost baseline; never trade database availability
for an unreviewed fixed-disk ceiling.

## Clean-stack bootstrap

A schema-only database is intentionally unusable for provider traffic. Before
provider routing, provision only the required Practice, Location, voice number,
and Messaging profile configuration through the reviewed provisioning path.
Invitations may remain empty until a real operator is invited. Do not copy
calls, messages, users, or patient history merely to create this configuration.

Ordinary releases never seed a hard-coded demo number. The migration job
removes the legacy `MIGRATE_VOICE_*` environment variables and preserves the
already reviewed configuration rows.

## Restore rehearsal

Run before launch, after recovery-setting changes, and at least quarterly:

1. Record the source backup/PITR timestamp and open a timed incident-style
   rehearsal.
2. Restore to a new temporary instance in `us-east1`; never overwrite the
   production instance.
3. Use a rehearsal-only service account and network path. Verify schema version,
   tenant counts, latest durable receipt/command timestamps, and PHI-safe
   aggregate checks. Do not route staff or Telnyx traffic to the restored copy.
4. Run read-only portal, receipt, command, Task, and realtime queries. Record
   recovery-point error, restore duration, validation duration, and every manual
   step.
5. Obtain operator sign-off, then remove the temporary instance as a separate
   approved destructive action.

The rehearsal is incomplete until restore and validation timings are recorded.
The checked configuration alone is not restore evidence.

## Release sequence

1. Build and validate one immutable backend image and one immutable web image.
2. Run the single migration job with pool max 1 and no automatic retry. It
   reapplies the exact reviewed database grants after all forward migrations.
   A new relation has no runtime authority until its role grant is added to
   `backend/internal/migrations/database-grants.sql`.
4. Deploy tagged `portal-api`, `provider-ingress`, and `realtime` revisions with
   no traffic at 1 vCPU / 512 MiB and request-based billing.
5. Exercise readiness and database paths on each tagged revision.
6. Deploy the one-instance worker revision. During overlap, confirm at most one
   old and one new worker and no duplicate provider command ID.
7. Shift request traffic gradually. Verify instance caps, pool use, webhook
   acknowledgement, command latency, receipt/command age, the 300-second
   realtime request timeout, 240–270-second application stream rotation, and
   SSE reconnect.
8. Deploy web last. It may scale to zero; verify its first-request cold path
   separately from the warm call-control path.
9. Enable provider routing only after every live acceptance gate below passes.

## Live acceptance gates

- From representative Florida networks, measure sign-in, workspace load,
  command commit, realtime reconnect, and provider-confirmed call-control
  latency to `us-east1`. Approve or change the region from measurements, not
  geography alone.
- Through the deployed `provider-ingress`, repeat at least ten 25-request signed
  bursts. Every response must be `204`, acknowledgement p99 must remain below
  one second, and duplicates must converge.
- With 10 logged-in Staff, prove command, portal query, and realtime paths stay
  responsive while ingress is pressured.
- With multiple established browser streams, terminate one realtime
  PostgreSQL listener and prove every affected client leaves the old listener
  generation, preserves its last good snapshot, reconnects after recovery, and
  performs one authoritative HTTP reconciliation. Repeat during a rolling
  realtime revision and confirm planned rotation is distributed rather than a
  synchronized reconnect wave.
- With one worker, prove receipt and command lanes continue moving, including a
  held/uncertain provider command and rolling-revision overlap.
- Prove outbound and inbound Messaging attachments cross the portal, worker,
  and read-only provider-ingress mounts without broadening object access.
- Confirm the live Cloud SQL connection reservation, Cloud Run maximum-instance
  overshoot, pool wait, transaction latency, CPU, memory, storage growth,
  backup, PITR, and restore results.
- Confirm live Telnyx retry timing, duplicate delivery, command-ID behavior,
  provider receipt correlation, WebRTC media, and reconciliation after an
  uncertain provider response.

## Database or zone outage

This target has no automatic database failover. During an outage:

1. Keep `provider-ingress` fail-closed. Return retryable failure when a receipt
   cannot commit; never return false `204`.
2. Mark portal and call control unavailable. Do not claim that a Staff command
   succeeded without its durable commit and provider evidence.
3. Preserve Telnyx retry delivery and command IDs. Do not manually replay an
   uncertain provider effect under a new ID.
4. Recover the zonal instance or restore from backup/PITR into `us-east1`.
5. Re-establish database roles and the measured 22-connection reservation.
6. Start the single worker first for durable convergence, then ingress,
   portal/realtime, and web. Reconcile every indeterminate command before
   restoring ordinary traffic.

## Rollback

Shift request traffic to the prior compatible revisions, keep the prior worker
within the two-revision overlap budget, and preserve the expanded database
schema. Never reverse a migration destructively during rollback. If the
database itself is unavailable, use the outage procedure; a Cloud Run traffic
rollback cannot repair it.
