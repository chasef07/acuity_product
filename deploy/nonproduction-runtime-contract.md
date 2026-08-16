# Slice 1 through 6 non-production runtime contract

This is a deployable contract, not a claim that an external environment exists.
Project, image, domain, service accounts, and database capacity remain
deployment inputs.

All backend services and the migration job run the same immutable image digest.
Only `ACUITY_RUNTIME_ROLE` changes.

| Runtime | Cloud Run kind | Concurrency | Min | Max | `DATABASE_POOL_MAX` | Other database connections | Acquisition timeout |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| web / Better Auth | service | 40 | 0 | 2 | 3 | 0 | 1500 ms |
| `portal-api` | service | 20 | 0 | 3 | 4 | 0 | 1500 ms |
| `provider-ingress` | service | 20 | 0 | 2 | 2 | 0 | 1500 ms |
| `realtime` | service | 50 | 0 | 2 | 3 | 1 direct `LISTEN` | 1500 ms |
| `worker` | worker pool | 4 lanes / instance (2 command) | 2 | 2 | 2 | 0 | 1500 ms |
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
Each instance runs one receipt lane, two command lanes, and one maintenance
lane. Its two-connection pool remains the hard database-concurrency bound.
Command claims lock a Call only while committing one command as `SENDING`;
provider I/O happens after commit. Commands for another Call and receipt
projection can therefore use the remaining capacity, while commands for the
same Call remain serialized. Queue lanes poll at 250 milliseconds while work is
moving, then back off empty claims deterministically through 500 milliseconds
and one second to a two-second ceiling; any progress resets them immediately.
This cuts steady empty polling by 8x while bounding idle wake-up latency at two
seconds. Queue and maintenance failures use independent equal-jitter exponential
backoff from 250 milliseconds to 10 seconds and reset after successful work.

Each runtime gets a distinct service account. Only `migrate` receives schema
DDL authority and provisioning-file access.
`backend/internal/migrations/database-grants.sql` is the runtime authority
contract and the migration job reapplies it after every forward migration:
`portal-api` receives only the Access, HumanCalling,
and Work table operations used by its request paths; `realtime` receives Access
reads plus the single Platform Operator binding column; and `worker` receives
only durable HumanCalling projection, command, and reconciliation authority.
`provider-ingress` receives column-scoped receipt INSERT, event-ID-scoped
SELECT FOR UPDATE, and duplicate-count UPDATE authority, but no provider API
credential. It does not read Call or attempt state; the worker attaches
`receipt.call_id` while projecting the signed fact. The worker receives the
Telnyx API credential needed to execute
durable commands. `portal-api` receives that credential only for lease-bound
short-lived media JWT issuance; no provider credential reaches the browser. The
web service gets Better Auth schema access, its auth secret, and Google OAuth
configuration, but no product-schema mutation authority.
Each database authority is delivered through a distinct Secret Manager secret;
runtime roles never share a database credential.

Deploy migrations as a blocking Cloud Run job before starting a new revision.
Most migrations expand first, but migration 0020 is an intentional CallLeg
replacement: run the Telnyx cutover preflight, drain the old runtime completely,
apply the migration, and deploy only the replacement. Never overlap a pre-0020
revision with the CallLeg schema. Never run migrations from application startup.

Required service configuration:

- `portal-api`: database settings, browser origin, Better Auth JWKS HTTPS URL,
  exact issuer and audience, SIP domain, handoff-token key, scoped Abita service
  credential and Practice, Telnyx command configuration, the private
  voicemail-playback signing key, the safe voicemail greeting text, the
  Messaging attachment mount, and the shared
  `HUMAN_CALLING_RING_WINDOW_SECONDS`,
  `HUMAN_CALLING_LEASE_SECONDS`, and
  `HUMAN_CALLING_READINESS_GRACE_SECONDS` values.
- `realtime`: the same authority configuration plus bounded heartbeat, stream
  lifetime, revalidation, and reconnect intervals.
- `provider-ingress`: database settings, HTTP port, and the Telnyx Ed25519
  webhook public key. It permits public HTTPS invocation because Telnyx must
  reach it; exact-body signature and timestamp verification are the application
  authentication boundary.
- `worker`: database settings, Telnyx command configuration, the same SIP
  domains, handoff/media keys, Human Calling timing values, and safe voicemail
  greeting text as `portal-api`, plus the Messaging attachment mount. The
  worker owns durable receipt projection, so these values must be identical.
  Voicemail audio has no copy worker or object-store configuration.
- `migrate`: database settings and, only for reviewed provisioning, paired
  input/output paths. Production Access Grants emit no human credential.
- web: bounded Better Auth pool settings, Better Auth URL/secret/trusted origin,
  internal portal API URL, API audience, Google OAuth configuration, and the
  two browser-visible HTTPS API origins. The browser origins are immutable
  web-image build arguments; the remaining values are runtime configuration. Web,
  `portal-api`, and `realtime` permit unauthenticated Cloud Run invocation
  because sign-in/Access/health interfaces and browser JWT requests reach
  them directly; their application interfaces still enforce the OpenAPI and
  Better Auth authorization contract.

Readiness is dependency-aware; liveness is process-only. Realtime marks itself
unready until its dedicated PostgreSQL listener is connected. SSE hints carry
only Practice ID and version. Every reconnect revalidates Access and the browser
refetches the authoritative snapshot.

The shared Telnyx Call Control Application and Credential Connection remain
deployment inputs. Its webhook target must be the public `provider-ingress`
route. Historical connected-call and voicemail rows may retain legacy object
metadata, but no active runtime reads or writes it and Slice 6 issues no new
connected-call recording commands. Telnyx owns voicemail audio. PostgreSQL
keeps the durable recording ID and lifecycle evidence; `portal-api` rechecks
current Location access, fetches a fresh provider download URL server-side,
and streams the audio without returning that URL or the Telnyx credential.
