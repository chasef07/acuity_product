# Slice 1 and 2 non-production runtime contract

This is a deployable contract, not a claim that an external environment exists.
Project, image, domain, service accounts, database capacity, and email sender
remain deployment inputs.

All backend services and the migration job run the same immutable image digest.
Only `ACUITY_RUNTIME_ROLE` changes.

| Runtime | Cloud Run kind | Concurrency | Min | Max | `DATABASE_POOL_MAX` | Other database connections | Acquisition timeout |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| web / Better Auth | service | 40 | 0 | 2 | 3 | 0 | 1500 ms |
| `portal-api` | service | 20 | 0 | 3 | 4 | 0 | 1500 ms |
| `provider-ingress` | service | 20 | 0 | 2 | 2 | 0 | 1500 ms |
| `realtime` | service | 50 | 0 | 2 | 3 | 1 direct `LISTEN` | 1500 ms |
| `worker` | worker pool | one loop per instance | 2 | 2 | 2 | 0 | 1500 ms |
| `migrate` | job | one task | 0 | 1 | 2 | 0 | 5000 ms |

One fully scaled revision can use at most:

`(2×3) + (3×4) + (2×2) + (2×(3+1)) + (2×2) + (1×2) = 36`
connections.

A conservative rolling-deployment calculation allows two complete revisions,
so the bounded rollout peak is 72. A 20 percent operating margin raises the
required application budget to 87. Reserving 10 connections for database
administration makes the minimum database capacity input 97. Deployment must
stop if the selected
database cannot supply that budget; lowering max instances or pool sizes is the
safe correction.

The worker pool is fixed at two instances in this non-production contract.
Worker pools do not expose request concurrency or autoscale from zero; rollout
uses an instance split between revisions while keeping the total fixed.

Each runtime gets a distinct service account. Only `migrate` receives schema
DDL authority and provisioning-file access. `portal-api`, `realtime`, and
`worker` receive product DML authority. `provider-ingress` receives
only the receipt-table INSERT, event-ID-scoped SELECT FOR UPDATE, and
duplicate-count UPDATE authority required for signed Telnyx receipts, but no
provider API credential. It does not read Call or attempt state; the worker
attaches `receipt.call_id` while projecting the signed fact. The worker receives
the Telnyx API credential needed to execute
durable commands. `portal-api` receives that credential only for lease-bound
short-lived media JWT issuance; no provider credential reaches the browser. The
web service gets Better Auth schema access, its auth secret, and its SMTP sender,
but no product-schema mutation authority.
Each database authority is delivered through a distinct Secret Manager secret;
runtime roles never share a database credential.

Deploy migrations as a blocking Cloud Run job before starting a new revision.
Migrations are forward-only and expand first. Keep the prior revision live
during health checks, allow old and new code to overlap within the 72-connection
bound, then shift traffic. Never run migrations from application startup.

Required service configuration:

- `portal-api`: database settings, browser origin, Better Auth JWKS HTTPS URL,
  exact issuer and audience, SIP domain, handoff-token key, scoped Abita service
  credential and Practice, Telnyx command configuration, and the shared
  `HUMAN_CALLING_OFFER_SECONDS`,
  `HUMAN_CALLING_CONNECTION_TIMEOUT_SECONDS`,
  `HUMAN_CALLING_LEASE_SECONDS`, and
  `HUMAN_CALLING_READINESS_GRACE_SECONDS` values.
- `realtime`: the same authority configuration plus bounded heartbeat, stream
  lifetime, revalidation, and reconnect intervals.
- `provider-ingress`: database settings, HTTP port, and the Telnyx Ed25519
  webhook public key. It permits public HTTPS invocation because Telnyx must
  reach it; exact-body signature and timestamp verification are the application
  authentication boundary.
- `worker`: database settings, Telnyx command configuration, the same four
  Human Calling timing values as `portal-api`, and the expected direct-recording
  GCS bucket name.
- `migrate`: database settings and, only for initial provisioning, paired
  input/output paths. The output contains one-time invitation credentials and
  must be captured as a `0600` secret artifact. An invitation link uses
  `https://<portal-domain>/invite#<credential>` so the credential fragment is
  never sent in an HTTP request URL.
- web: bounded Better Auth pool settings, Better Auth URL/secret/trusted origin,
  internal portal API URL, API audience, SMTP configuration, and the two
  browser-visible HTTPS API origins. The browser origins are immutable web-image
  build arguments; the remaining values are runtime configuration. Web,
  `portal-api`, and `realtime` permit unauthenticated Cloud Run invocation
  because sign-in/invitation/health interfaces and browser JWT requests reach
  them directly; their application interfaces still enforce the OpenAPI and
  Better Auth authorization contract.

Readiness is dependency-aware; liveness is process-only. Realtime marks itself
unready until its dedicated PostgreSQL listener is connected. SSE hints carry
only Practice ID and version. Every reconnect revalidates Access and the browser
refetches the authoritative snapshot.

The shared Telnyx Call Control Application and Credential Connection remain
deployment inputs. Its webhook target must be the public `provider-ingress`
route, and its custom recording storage must point directly at the dedicated
private GCS bucket named by `TELNYX_RECORDING_BUCKET`. GCS access, lifecycle,
retention, consent, and release approval are external gates; the application
does not proxy recording bytes or create transcripts.
