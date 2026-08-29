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
  migrated user accounts, calls, messages, or patient history.
- Telnyx Call Control and Messaging profile webhook URLs target the
  `us-east1` provider ingress. No live Call, SMS/MMS, retry, or signed burst was
  generated as part of the migration.
- The prior `us-central1` services, migration job, worker pool, Cloud SQL
  instance, buckets, Artifact Registry repository, and database URL secrets
  were removed on 2026-08-06. `us-east1` is the only production region; there
  is no cross-region standby or provider fallback stack.
- A successful automated backup and a successful on-demand backup exist in
  `us-east1`. A restore rehearsal is still required before launch.
- PostgreSQL reports 400 total connections and three superuser-reserved slots:
  a 397-connection non-superuser ceiling. The dated checkpoint used a
  28-connection reservation; the current 36-connection contract leaves 361
  connections of configured ceiling headroom. Eight client connections were
  active at the dated snapshot.

This checkpoint is infrastructure and configuration evidence, not Florida
latency, carrier delivery, continuous availability, or end-to-end Staff proof.

## Current automated release

The checked production stack runs only in `acuity-health-prod` / `us-east1`.
Ordinary releases target that region and never copy data between regions.
Application rollback uses compatible prior `us-east1` revisions; database
recovery uses backup/PITR rather than a second regional stack.

Every reviewed change merged to GitHub `main` follows one validation path, but
only a merged Release Please pull request starts a production deployment:

1. GitHub Actions runs the Go, web, generated-contract, and browser suites.
2. Successful `main` CI starts a separate Release workflow. It has no
   superseding workflow-level concurrency, so a newer `main` run cannot cancel
   a pending release decision.
3. For an ordinary change, Release Please creates or updates the product
   release pull request and the Release workflow ends without deploying.
4. When the Release Please pull request is merged, `main` CI runs the same
   suites again. Release Please recognizes the merged version and changelog,
   tags that commit, and publishes the GitHub release.
5. The Release workflow checks out that exact released SHA and reruns the Go,
   web, generated-contract, and browser suites. A later `main` commit's results
   cannot stand in for the released commit.
6. GitHub exchanges its `main`-bound identity for the
   `acuity-product-cloud-deploy` Google service account; there is no stored
   Google service-account key.
7. Cloud Build creates backend and web images tagged with the released commit's
   full SHA and resolves them to immutable digests.
8. `acuity-migrate` applies forward migrations and the reviewed runtime grants.
9. Backend services and the worker stage on the digest, become ready, and
   promote before web is released last.
10. Any post-promotion smoke failure returns request traffic and the worker split
   to the revisions captured at the start of the release. Expanded migrations
   remain in place.

Operators do not run `pnpm` or a deployment script for an ordinary release.
Merge the reviewed Release Please pull request, then follow the GitHub Actions
run and its linked Cloud Build. The web URL is
`https://acuity-web-cbuqwpsdsq-ue.a.run.app`.

## Custom domain cutover

The production front door reserves global IP `136.68.242.183` and routes to
`acuity-web` through the `acuity-web-neg` serverless NEG. The HTTPS URL map
serves `acuityhealth.io` and permanently redirects `www.acuityhealth.io` to the
apex. The HTTP URL map redirects both hosts to HTTPS.

Certificate Manager pre-provisions TLS without moving traffic:

- DNS authorizations `acuityhealth-apex-dns-auth` and
  `acuityhealth-www-dns-auth` use per-project CNAME records in Vercel DNS.
- Google-managed certificate `acuity-web-dns-cert` covers the apex and `www`.
- Certificate map `acuity-web-cert-map` has one active entry per hostname and
  is attached to `acuity-web-https-proxy`.
- The original Compute certificate `acuity-web-managed-cert` remains available,
  but the target proxy uses the attached certificate map.

The Certificate Manager certificate must be `ACTIVE` before cutover. Test the
Google path without changing public DNS:

```sh
curl --noproxy '*' \
  --resolve acuityhealth.io:443:136.68.242.183 \
  https://acuityhealth.io/
curl --noproxy '*' \
  --resolve www.acuityhealth.io:443:136.68.242.183 \
  --head https://www.acuityhealth.io/
```

Prepare without moving traffic:

1. Keep `BETTER_AUTH_URL`, `BETTER_AUTH_JWKS_URL`, and `BETTER_AUTH_ISSUER` on
   the current `run.app` origin.
2. Add `https://acuityhealth.io` beside the current origin in the comma-separated
   `BETTER_AUTH_TRUSTED_ORIGINS` and backend `BROWSER_ORIGIN` values.
3. Add `https://acuityhealth.io` and
   `https://acuityhealth.io/api/auth/callback/google` to the Google OAuth web
   client without removing the existing origin and callback.
4. Keep the Vercel apex and `www` traffic records unchanged until the explicit
   cutover window. Preserve both Certificate Manager validation CNAMEs for
   automatic certificate renewal.

At cutover, confirm the certificate and both map entries are still `ACTIVE`,
then point the apex and `www` traffic records to `136.68.242.183`. After the
Google path is reachable through public DNS, make `https://acuityhealth.io` the
Better Auth base URL, JWKS URL, and issuer while retaining the current
`run.app` origin as a trusted rollback path. A failed sign-in, session, API, or
realtime check returns the traffic records to their captured Vercel values; do
not delete the Vercel deployment or the validation CNAMEs before acceptance.

## Owners and stop conditions

- `provider-ingress` owns signature verification and durable receipt before
  `204`. Stop if live acknowledgement p99 is one second or more.
- `portal-api` owns authenticated Staff commands and immediate call control.
  Stop if ten-Staff command or Florida-user latency misses the approved gate.
- `realtime` owns disposable version hints. Its failure may reduce freshness but
  must not lose durable state.
- `worker` owns receipt projection, provider-command retry, and reconciliation.
  Stop if either receipt or command lane stalls on one fixed instance.
- Cloud SQL is the sole durable authority. Stop if fewer than 36 connections are
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

Before the CallLeg replacement migration, capture the live Telnyx and database
gate results in an evidence JSON file and run the read-only fail-closed check:

```sh
export TELNYX_API_KEY="$(gcloud secrets versions access latest --secret="$TELNYX_API_KEY_SECRET")"
export TELNYX_CALL_CONTROL_ID='<Product Call Control Application ID>'
export TELNYX_CREDENTIAL_CONNECTION_ID='<Product WebRTC Connection ID>'
export TELNYX_FROM_NUMBER='<Product E.164 DID>'
node deploy/check-telnyx-callleg-cutover.mjs \
  deploy/telnyx-callleg-cutover-contract.json \
  --print-provider-provenance
node deploy/check-telnyx-callleg-cutover.mjs \
  deploy/telnyx-callleg-cutover-contract.json \
  /absolute/path/to/live-cutover-evidence.json
```

Copy only the printed hashes into the evidence provenance. The checker performs
fresh authenticated `GET` reads for the Call Control Application, WebRTC
credential connection, their shared outbound voice profile, and the Product
DID's summary and voice settings; compares their sanitized snapshot hash and
resource hashes with the evidence; rejects enabled inbound DID recording; and
compares the live settings themselves with the checked contract. It never
prints provider bodies, credentials, signing keys, SIP usernames, or phone
numbers. Unset `TELNYX_API_KEY` after the check.

The evidence must prove the current REFER route user survives unchanged, both
webhook signing keys verify, explicit inbound/outbound Bridge and voicemail
playback work live, and that old revisions, active Calls, in-flight commands,
pending supported runtime receipts, and stale credential mappings are all zero.
Immutable receipts for retired message kinds are audit history, not pending
runtime work. Do not run the migration when any gate is missing or false.

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

   Prove at least 36 connections remain usable by the application and operator
   identities. Do not infer capacity from the machine size alone. Record that
   freshly measured count in the GitHub `production` environment variable
   `USABLE_DATABASE_CONNECTIONS`; the automated release stops before its first
   Cloud command when the value is missing, invalid, or below the checked
   runtime contract.
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

## Google sign-in

Create one Google OAuth Web application for the production portal. Its exact
authorized redirect URI is:

```text
https://acuity-web-cbuqwpsdsq-ue.a.run.app/api/auth/callback/google
```

Store the client ID and client secret as separate Secret Manager values named
`acuity-product-google-client-id` and `acuity-product-google-client-secret`.
Grant the web runtime identity access to only those secrets, then add only the
two new mappings to `acuity-web`:

```sh
gcloud run services update acuity-web \
  --project acuity-health-prod \
  --region us-east1 \
  --update-secrets \
  GOOGLE_CLIENT_ID=acuity-product-google-client-id:latest,GOOGLE_CLIENT_SECRET=acuity-product-google-client-secret:latest \
  --quiet
```

Do not use `--set-secrets` for this live update because it replaces the
service's complete secret mapping. The checked deployment renderer uses the
complete `--set-secrets` contract only when creating the whole service.

Google identity is authentication, not authorization. Better Auth creates a
Google-backed User only when `Access` confirms the verified email has an
unrevoked Access Grant or Platform Operator record. The first Access discovery
claims a Practice Access Grant and creates its exact Membership atomically.
Acceptance requires one eligible Google login, one ineligible Google login
rejection, and proof that the eligible User receives only the scope authorized
by `Access`. Production has no password or verification-email authentication.

## Clean-stack bootstrap

A schema-only database is intentionally unusable for provider traffic. Before
provider routing, provision the required Practice, Location, voice number,
Messaging profile, and reviewed Access Grant configuration through the audited
provisioning path. Do not copy calls, messages, Users, or patient history merely
to create this configuration.

The reviewed bootstrap input is
`config/production-provisioning.json`; the backend image carries the same file
at `/etc/acuity/production-provisioning.json`. Its steady-state topology is:

| Practice | Operational Location | Source office routes | Voice | Messaging |
| --- | --- | --- | --- | --- |
| Abita Eye Group | Spring Hill | `spring-hill` | `+17275919997` | `+17275919997` |
| Abita Eye Group | Crystal River | `crystal-river` | `+13523202007` | Not activated |
| Abita Eye Group | Hollywood | `hollywood` | `+19542872010` | `+19542872010` |
| Abita Eye Group | Sweetwater | `sweetwater` | `+17864654836` | `+17864654836` |
| Abita Eye Group | Sweetwater Optical | `sweetwater-optical` | Not activated | Not activated |
| Abita Eye Group | North Miami Beach Optical | `north-miami-beach-optical` | `+13055095333` | Not activated |
| Acuity Demo | Rheumatology | `dev`, `rheumatology-demo` | `+14843989071` | `+14843989071` |
| Acuity Demo | Ophthalmology | `ophthalmology-demo` | `+18027878312` | Not activated |
| Acuity Demo | Mental Health | `mental-health-demo` | `+13207388132` | Not activated |

The configured Abita voice Locations share the reviewed “Abeeta Eye Group”
voicemail greeting. The Acuity Demo Practice is a separate tenant.
Rheumatology preserves the stable `demo-484` provisioning key, its existing
voice and Messaging configuration, and its Acuity Demo greeting while both
`dev` and `rheumatology-demo` resolve to that one Location. Ophthalmology and
Mental Health have voice configuration only; neither has an inferred Messaging
sender or profile. Sweetwater Optical also has no voice or Messaging
configuration yet. The same file contains 32 Abita Access Grants: Jason is the
sole Admin and the other 31 entries are Staff with reviewed Location Scopes.
Access Grants create no separate credential or human password.

Migration `0023_split_abita_locations.sql` upgrades the already-provisioned
four-Location production topology before account provisioning. It preserves the
existing South Florida Medical row and its `+17864654836` voice/Messaging
configuration as Sweetwater, preserves South Florida Optical and its
`+13055095333` voice configuration as North Miami Beach Optical, and creates
Hollywood and Sweetwater Optical with their own office routes. It fails closed
if Abita Access Grants or Memberships exist, or if the combined
Hollywood/Sweetwater Location contains a Call, handoff, voicemail, Message
thread, or Task. Those conditions require an explicit data migration instead
of relabeling records.

The production input now reconciles the established reviewed topology by stable
provisioning keys and adds the approved Access Grants idempotently. Before the
first account run, verify that no earlier Abita grant or Membership exists. A
nonzero result is a stop-and-inspect condition, not permission to delete data:

```sql
SELECT
  (SELECT count(*)
   FROM access_grants access_grant
   JOIN access_practices practice ON practice.id = access_grant.practice_id
   WHERE practice.provisioning_key = 'abita-eye-group') AS access_grants,
  (SELECT count(*)
   FROM access_memberships membership
   JOIN access_practices practice ON practice.id = membership.practice_id
   WHERE practice.provisioning_key = 'abita-eye-group') AS memberships;
```

Both values must be zero for the first run. A later reviewed rerun may find the
same 31 provisioning keys and creates no duplicates.

After reviewing the exact image digest and JSON, an operator may run the
reconciliation against the existing `acuity-migrate` job:

```sh
gcloud run jobs update acuity-migrate \
  --project acuity-health-prod \
  --region us-east1 \
  --update-env-vars \
  PROVISIONING_INPUT=/etc/acuity/production-provisioning.json,PROVISIONING_OUTPUT=/tmp/production-provisioning-output.json \
  --quiet

gcloud run jobs execute acuity-migrate \
  --project acuity-health-prod \
  --region us-east1 \
  --wait \
  --quiet

gcloud run jobs update acuity-migrate \
  --project acuity-health-prod \
  --region us-east1 \
  --remove-env-vars PROVISIONING_INPUT,PROVISIONING_OUTPUT \
  --quiet
```

Access, Messaging, and voice configuration commit in one PostgreSQL
transaction; any validation failure rolls the entire customer topology back.
The restricted provisioning output is synced before that commit and contains no
credentials for Access Grants. Stop after any failed command and inspect the
migration execution before doing anything else. Do not rerun merely because
environment cleanup failed. Ordinary releases remove these variables before
migration, so they preserve the provisioned rows without silently replaying the
reconciliation.

The checked JSON and deterministic PostgreSQL test prove only the intended
database topology. Before provider traffic, separately prove the production
service identity and UUID authorization, Telnyx number/application routing,
signed webhook receipt, staff call fanout, shared voicemail playback, outbound
caller ID, and real SMS send/receive plus STOP/HELP behavior. Crystal River's
AI transfer to the external cell remains owned by `abita_agent` and needs a
live transfer test. The Demo path still needs a signed-in portal-to-database-to-
worker-to-provider/handset test. Activate one reviewed Staff Access Grant with
Google and verify its exact Location scope before broad staff sign-in.
The worker should discover the new operational User within 30 seconds and create
one unique Telnyx on-demand telephony credential on the shared Product WebRTC
Connection. Do not create a per-user Telnyx connection manually. Verify the
credential reached `ACTIVE` before marking the User calling-ready:

```sql
SELECT credential.state,
       credential.provider_credential_id IS NOT NULL AS has_provider_credential,
       credential.provider_sip_username IS NOT NULL AS has_sip_username
FROM human_calling_credentials credential
JOIN access_memberships membership
  ON membership.user_subject = credential.user_subject
WHERE membership.email = '<reviewed Google email>'
  AND membership.revoked_at IS NULL;
```

Calling-ready also requires the Staff browser softphone to be registered,
microphone/audio healthy, and explicitly available. Inbound transfers fan out
only to available Staff whose Location Scope includes the Call's Location;
Practice Admins do not join inbound fan-out. Task access follows the Membership
immediately. Messaging follows the same Location Scope, but outbound messages
require an activated sender in the topology table above.

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

The CallLeg schema replacement is a one-shot exception to the ordinary rolling
sequence. Automatic main deployment remains disabled while the repository
variable `CALLLEG_SCHEMA_CUTOVER_COMPLETE` is not `true`.

For that scheduled cutover only:

1. Set the legacy portal to reject new handoffs and show calling maintenance.
2. Drain to zero active Calls, pending/sending/ambiguous commands, every
   unprojected supported runtime receipt (including quarantined receipts), and
   voicemail work. Retained immutable receipts for retired message kinds do not
   count as executable backlog.
3. Scale the old API, ingress, realtime, and worker revisions to zero; expire
   softphone leases; switch to the pre-proven replacement Staff credentials;
   disable old credentials; and take the final verified Cloud SQL snapshot.
4. Capture a sanitized read-only Telnyx v2 snapshot plus referenced live-probe
   evidence. Validate it with `deploy/check-telnyx-callleg-cutover.mjs`.
5. Run `deploy/run-callleg-cutover.sh` with
   `CALLLEG_CUTOVER_WINDOW_CONFIRMED=true` and
   `CALLLEG_CUTOVER_EVIDENCE_PATH=<ephemeral-evidence.json>`. The replacement
   portal is deliberately deployed with `HUMAN_CALLING_HANDOFF_ADMISSION=closed`.
6. Run deterministic smoke proof and the scoped three-client/two-Call Telnyx
   gate. If any gate fails, keep admission closed and restore the recorded
   snapshot and revisions or forward-fix; never route the new schema to the
   legacy runtime.
7. Reopen admission with `HUMAN_CALLING_HANDOFF_ADMISSION=open`, then set
   `CALLLEG_SCHEMA_CUTOVER_COMPLETE=true` for subsequent ordinary releases.

Do not commit the live evidence file; it contains sanitized operational
provenance and belongs with the cutover record.

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
8. Deploy web last with one warm instance; verify the public page, sign-in, and
   session paths separately from the call-control path.
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
5. Re-establish database roles and the measured 36-connection reservation.
6. Start the single worker first for durable convergence, then ingress,
   portal/realtime, and web. Reconcile every indeterminate command before
   restoring ordinary traffic.

## Rollback

Shift request traffic to the prior compatible revisions, keep the prior worker
within the two-revision overlap budget, and preserve the expanded database
schema. Never reverse a migration destructively during rollback. If the
database itself is unavailable, use the outage procedure; a Cloud Run traffic
rollback cannot repair it.
