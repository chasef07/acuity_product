# Acuity Product architecture

Acuity Product is a call-center operating workspace built as a Next.js web
application, one Go modular monolith, and PostgreSQL. The backend is deployed as
separate Cloud Run runtime roles so request traffic, provider ingress, realtime
streams, durable work, and schema migration can fail and scale independently
without splitting domain ownership across microservices.

Production readiness is an evidence claim, not a topology label. This document
uses three evidence levels:

- **Live** — observed in the `acuity-health-prod` Google Cloud project on
  2026-08-09.
- **Enforced** — checked by version-controlled code, tests, or deployment
  automation.
- **Gate** — required by the production runbook but not proven by configuration
  alone.

The detailed behavioral architecture lives in
[`docs/architecture/overview.md`](docs/architecture/overview.md). The checked
compute and recovery values live in
[`deploy/production-runtime-contract.json`](deploy/production-runtime-contract.json).

## Architecture at a glance

```mermaid
flowchart TB
    subgraph external["Users and external systems"]
        Browser["Staff browser"]
        Google["Google identity"]
        Abita["Abita AI agent"]
        Telnyx["Telnyx voice and messaging"]
    end

    subgraph gcp["Google Cloud project: acuity-health-prod"]
        Build["Cloud Build"]
        Registry["Artifact Registry<br/>immutable image digests"]
        Secrets["Secret Manager"]
        Logs["Cloud Logging"]

        subgraph east["us-east1 production runtime"]
            Web["acuity-web<br/>Next.js and Better Auth"]
            API["acuity-portal-api<br/>commands and queries"]
            Ingress["acuity-provider-ingress<br/>signed webhook receipts"]
            Realtime["acuity-realtime<br/>SSE version hints"]
            Worker["acuity-worker<br/>durable projection and effects"]
            Migrate["acuity-migrate<br/>single migration job"]
            SQL[("Cloud SQL PostgreSQL 16<br/>sole durable authority")]
            Attachments[("Private messaging objects")]
            Recordings[("Private recording objects")]
        end
    end

    Browser -->|"HTTPS"| Web
    Browser -->|"JWT commands and queries"| API
    Browser -->|"authorized SSE"| Realtime
    Browser <-->|"WebRTC media"| Telnyx
    Google -->|"OAuth"| Web
    Abita -->|"authenticated interactions and tasks"| API
    Telnyx -->|"signed webhooks"| Ingress
    Worker -->|"idempotent provider commands"| Telnyx

    Web -->|"auth schema"| SQL
    API --> SQL
    Ingress --> SQL
    Realtime -->|"queries and LISTEN"| SQL
    Worker --> SQL
    Migrate -->|"forward migrations"| SQL
    API --> Attachments
    Ingress --> Attachments
    Worker --> Attachments
    Telnyx -->|"recording export"| Recordings

    Build --> Registry
    Registry --> Web
    Registry --> API
    Registry --> Ingress
    Registry --> Realtime
    Registry --> Worker
    Registry --> Migrate
    Secrets -.-> Web
    Secrets -.-> API
    Secrets -.-> Ingress
    Secrets -.-> Realtime
    Secrets -.-> Worker
    Web -.-> Logs
    API -.-> Logs
    Ingress -.-> Logs
    Realtime -.-> Logs
    Worker -.-> Logs
```

The architecture deliberately combines one domain implementation with multiple
runtime roles. This preserves locality in the codebase while isolating unlike
production workloads.

## Live Google Cloud compute

All request roles use request-based billing. The worker uses instance-based
billing so durable recovery does not depend on an inbound request. Every role
has its own Google service account and its own least-privilege database
credential.

| Runtime | Kind | Workload | CPU / memory | Concurrency | Scale |
| --- | --- | --- | --- | ---: | ---: |
| `acuity-web` | Cloud Run service | Next.js, Better Auth | 1 vCPU / 512 MiB | 40 | 1–2 |
| `acuity-portal-api` | Cloud Run service | authenticated commands and queries | 1 vCPU / 512 MiB | 20 | 1–3 |
| `acuity-provider-ingress` | Cloud Run service | verify and durably receipt webhooks | 1 vCPU / 512 MiB | 20 | 1–2 |
| `acuity-realtime` | Cloud Run service | authorized SSE hints | 1 vCPU / 512 MiB | 50 | 1–2 |
| `acuity-worker` | Cloud Run worker pool | projection, retry, reconciliation | 1 vCPU / 512 MiB | n/a | 1 fixed |
| `acuity-migrate` | Cloud Run job | reviewed schema and grant migration | 1 vCPU / 512 MiB | n/a | 1 task, 0 retries |

The database is Cloud SQL for PostgreSQL 16 Enterprise in `us-east1`, with 2
vCPU, 8 GiB memory, 50 GiB SSD, automatic storage growth, deletion protection,
daily backups, seven retained backups, and seven days of point-in-time recovery
logs. It is intentionally zonal: a database or zone outage does **not** have
automatic failover.

The checked connection reservation is explicit:

```text
maximum request-role demand          11
overlapping worker revisions          2
single migration task                 1
autoscaler overshoot headroom         5
operator and recovery headroom        3
                                      --
required usable connections          22
```

Each request-role instance has a one-connection application pool; realtime
also owns one dedicated PostgreSQL `LISTEN` connection. The small pools make
database pressure bounded and visible instead of allowing autoscaling to
silently exhaust PostgreSQL.

## Deep modules and explicit seams

A module earns its place by hiding meaningful behavior behind one small
interface. HTTP handlers, SQL, authentication, Telnyx, and object storage are
adapters at explicit seams; they do not own domain state.

```mermaid
flowchart TB
    subgraph adapters["Inbound adapters"]
        HTTP["HTTP commands and queries"]
        Webhooks["Telnyx webhook adapter"]
        Auth["Better Auth JWT and JWKS adapter"]
        Jobs["Durable worker adapter"]
    end

    subgraph modules["Deep domain modules"]
        Access["Access<br/>identity, scope, authorization"]
        Work["Work<br/>accountable task lifecycle"]
        Calling["HumanCalling<br/>Call and CallLeg lifecycle"]
        Messaging["Messaging<br/>conversation and delivery lifecycle"]
        Interaction["AIInteraction<br/>AI call and outcome evidence"]
        Evidence["EvidenceArchive<br/>protected media metadata"]
    end

    subgraph outbound["Outbound adapters"]
        Postgres["PostgreSQL adapter"]
        Voice["Telnyx voice adapter"]
        SMS["Telnyx messaging adapter"]
        Objects["Protected object-storage adapter"]
    end

    HTTP --> Access
    Auth --> Access
    Webhooks --> Calling
    Webhooks --> Messaging
    Jobs --> Calling
    Jobs --> Messaging
    Jobs --> Interaction

    Access --> Work
    Access --> Calling
    Access --> Messaging
    Access --> Interaction
    Access --> Evidence
    Calling --> Work
    Messaging --> Work

    Work --> Postgres
    Calling --> Postgres
    Messaging --> Postgres
    Interaction --> Postgres
    Evidence --> Postgres
    Calling --> Voice
    Messaging --> SMS
    Messaging --> Objects
    Evidence --> Objects
```

The important seams have two justified adapters: a production adapter and a
deterministic test adapter. PostgreSQL is tested as real, local-substitutable
infrastructure so transaction, lock, uniqueness, and retry behavior remain
inside the module implementation.

## One durable event path

Provider acknowledgements never wait on business projection or another remote
effect. A webhook receives success only after its unique receipt commits.
Replays, reordering, runtime loss, and ambiguous provider outcomes converge
through durable state.

```mermaid
sequenceDiagram
    participant T as Telnyx
    participant I as provider-ingress
    participant P as PostgreSQL
    participant W as worker
    participant M as Owning module
    participant R as realtime
    participant B as Browser
    participant A as portal-api

    T->>I: Signed provider event
    I->>I: Verify signature and bound body
    I->>P: Insert unique receipt and work item
    P-->>I: Commit
    I-->>T: 204 acknowledgement
    W->>P: Claim committed work with a bounded lease
    W->>M: Apply provider fact idempotently
    M->>P: Commit state transition and next command
    W->>T: Execute provider command outside the transaction
    T-->>W: Definitive or ambiguous result
    W->>P: Record evidence or reconcile
    P-->>R: NOTIFY version hint
    R-->>B: Disposable SSE hint
    B->>A: Refetch authoritative state
    A->>P: Query authorized projection
    P-->>A: Current committed state
    A-->>B: Authoritative response
```

The browser never becomes an authority for call state, message delivery, or
appointment outcomes. Provider evidence and committed receipts do.

## Command and recovery model

```mermaid
flowchart LR
    Intent["Authorized intent"] --> Commit["Commit state and stable command ID"]
    Commit --> Execute["Execute outside the transaction"]
    Execute --> Result{"Provider result"}
    Result -->|"definitive"| Evidence["Commit provider evidence"]
    Result -->|"ambiguous"| Reconcile["Reconcile without blind resend"]
    Result -->|"retryable"| Backoff["Bounded retry with backoff"]
    Backoff --> Execute
    Reconcile --> Evidence
    Evidence --> Converged["One durable outcome"]
```

This pattern keeps external latency out of database transactions, prevents
duplicate side effects, and makes failure visible and recoverable. Ambiguous
mutations are reconciled; they are not automatically repeated.

## Auth and trust paths

```mermaid
flowchart TB
    Human["Human user"] --> Google["Google OAuth"]
    Google --> BetterAuth["Better Auth session"]
    BetterAuth -->|"signed 15-minute JWT"| Access["Access module"]
    Access -->|"practice, role, location scope"| Protected["Protected behavior"]

    Telnyx["Telnyx"] -->|"signed webhook"| Verify["Provider signature adapter"]
    Verify --> Receipt["Durable receipt"]

    Abita["Abita AI agent"] -->|"service bearer credential"| ServiceAccess["Service-principal authorization"]
    ServiceAccess --> Protected

    Runtime["Runtime-specific service account"] --> Secrets["Secret Manager references"]
    Runtime --> Grants["Role-specific PostgreSQL grants"]
    Secrets --> Protected
    Grants --> Protected
```

Human identity, service identity, provider authenticity, tenant scope, and
runtime identity are separate checks. Client-supplied IDs are requested
context, never authorization proof. Private object buckets enforce uniform
bucket-level access and public-access prevention.

## Tested release pipeline

Only a tested `main` commit can enter the production release. GitHub Actions
uses Workload Identity Federation, so the pipeline does not store a long-lived
Google Cloud key.

```mermaid
flowchart LR
    Change["Pull request"] --> CI{"Required CI"}
    CI --> Backend["Go and PostgreSQL tests"]
    CI --> WebTests["Lint, types, unit, build"]
    CI --> Contracts["Generated and auth schema checks"]
    CI --> Browser["Playwright journey"]
    Backend --> Main["Merge to main"]
    WebTests --> Main
    Contracts --> Main
    Browser --> Main
    Main --> OIDC["GitHub OIDC and Workload Identity"]
    OIDC --> Build["Cloud Build"]
    Build --> Images["Build and push immutable digests"]
    Images --> Migration["Run one forward migration job"]
    Migration --> Stage["Stage backend revisions with no traffic"]
    Stage --> Verify["Verify digest, liveness, readiness"]
    Verify --> PromoteWorker["Promote worker revision"]
    PromoteWorker --> PromoteAPI["Promote request roles to 100 percent"]
    PromoteAPI --> StageWeb["Stage and verify web revision"]
    StageWeb --> Smoke["Smoke sign-in, session, and readiness"]
    Smoke --> Released["Release complete"]

    Verify -.->|"failure"| Rollback["Restore captured revisions"]
    PromoteAPI -.->|"failure"| Rollback
    Smoke -.->|"failure"| Rollback
```

Application rollback moves traffic and worker allocation to captured compatible
revisions. It never destructively reverses a migration. Schema changes remain
forward-compatible across the rollout window.

## Observable operations

Runtime modules emit a bounded, PHI-safe metric contract. The intended Google
Cloud operations loop is:

```mermaid
flowchart LR
    Runtime["Runtime roles"] -->|"structured bounded logs"| Logging["Cloud Logging"]
    Logging --> Metrics["Log-based counters and distributions"]
    Metrics --> Alerts["Alert policies"]
    Alerts --> Channel["Operator notification channel"]
    Channel --> Runbook["Production runbook"]
    Runbook --> Recovery["Rollback, replay, reconcile, or restore"]
    Recovery --> Runtime
```

The metric and alert definitions are checked into
[`deploy/observability`](deploy/observability/README.md), but the live project
inventory on 2026-08-09 contained no matching `acuity_call_center` log metrics
or alert policies. That operations loop is therefore an explicit launch gate,
not a completed live control.

## Production evidence snapshot

Read-only Google Cloud inspection on 2026-08-09 established:

- all four Cloud Run request services and the worker pool were `Ready`;
- all five active runtimes had 100% allocation to release
  `4e5aa997-7086-410a-8db0-415f989f404f`;
- the successful Cloud Build release was built from repository commit
  `618499027850101dad9267d8c0abc6b58c5a9cb3`;
- backend and web runtimes referenced immutable Artifact Registry digests;
- `acuity-production` was `RUNNABLE` in `us-east1` with the checked PostgreSQL,
  backup, PITR, storage, and deletion-protection configuration; and
- the regional messaging and recording buckets enforced uniform bucket-level
  access, public-access prevention, and seven-day soft deletion.

This proves deployed compute health and release alignment. It does not, by
itself, prove live carrier behavior, user latency, restore performance, or alert
delivery.

## Readiness ledger

| Control | Evidence | Status |
| --- | --- | --- |
| Workload isolation | four request roles, one fixed worker, one migration job | **Live** |
| Immutable release provenance | successful commit-tagged Cloud Build; digest-pinned active revisions | **Live** |
| Warm floor and bounded scaling | runtime min/max, concurrency, and pool limits match the checked contract | **Live** |
| Durable authority | PostgreSQL-backed receipts, commands, leases, idempotency, and interface-level tests | **Enforced** |
| Least-privilege runtime identity | distinct service accounts, Secret Manager references, database grants | **Live + enforced** |
| Recovery configuration | daily backups, seven-day PITR, deletion protection, soft-deleted objects | **Live** |
| Release safety | required CI, no-traffic staging, probes, smoke checks, captured-revision rollback | **Enforced** |
| Database availability | single-zone by explicit cost/availability decision | **Accepted limitation** |
| Restore performance | timed backup/PITR restore rehearsal and validation | **Gate** |
| Live capacity | Cloud Run/Cloud SQL load, pool saturation, autoscaler overshoot, Florida latency | **Gate** |
| Provider acceptance | live Telnyx delivery, retry, reconciliation, WebRTC, and recording proof | **Gate** |
| Alert delivery | apply metrics/policies and trigger one reversible incident per policy | **Gate** |
| Shared object behavior | cross-runtime visibility, IAM, retention, and signed-expiry proof | **Gate** |

The result is a modern, production-oriented architecture with strong correctness
and deployment controls. The remaining gates are deliberately visible because
production readiness depends on observed recovery and end-to-end behavior, not
the number of managed products in a diagram.

## Architecture references

- [Behavioral architecture and invariants](docs/architecture/overview.md)
- [Call-center observability contract](docs/architecture/call-center-observability.md)
- [Production runtime capacity contract](deploy/production-runtime-contract.md)
- [Production release and recovery runbook](deploy/production-runbook.md)
- [Product and technical specification](docs/acuity-portal-product-technical-spec.md)
- [Google Cloud call-center controls](docs/research/google-cloud-call-center-controls.md)
