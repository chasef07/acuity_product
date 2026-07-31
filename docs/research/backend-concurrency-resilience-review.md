# Backend concurrency and resilience review

Audited 2026-07-24 against the current Acuity Portal specifications and primary documentation from Google Cloud, PostgreSQL, Telnyx, and Better Auth.

## Verdict

Keep **Next.js + one Go modular monolith + PostgreSQL**. It is a strong fit for this product's concurrency and reliability needs, including multiple signed-in staff, simultaneous call actions, outbound calls, inbound provider events, and concurrent reads and writes.

The choice is strong because the hardest correctness problems are transactional:

- exactly one staff member wins a call offer;
- a replayed command or webhook produces one effect;
- task ownership and provider intent change together;
- provider-confirmed facts cannot be replaced by browser intent;
- failed background work remains durable and retryable.

PostgreSQL can enforce these invariants with short transactions, conditional updates, row locks, and unique constraints. Go provides a small, concurrency-safe runtime around those transactions. Cloud Run can scale and replace instances without making process memory authoritative.

The current direction does need one refinement before implementation:

> “One Go service” should mean one codebase, one image, one set of domain modules, and one PostgreSQL authority. It should not require webhooks, short API requests, long-lived SSE streams, background workers, and migrations to run in one Cloud Run process.

Those workloads have materially different scaling and failure behavior. Separate runtime roles from the same Go image preserve the modular monolith while preventing an SSE surge or slow job from starving call commands or webhook receipt.

The original review recommended regional high availability. The checked
2026-07-29 pilot contract supersedes that recommendation for Acuity's observed
traffic: Cloud SQL Enterprise, single-zone, 2 vCPU / 8 GiB, and 50 GiB SSD in
`us-east1`. Acuity accepts that a database or zone outage does not fail over
automatically; the portal and call control can remain unavailable until
recovery or restore. Telnyx retries and durable reconciliation protect
correctness, not availability. Regional HA remains a future option if measured
availability requirements justify its cost.

## Recommended deployment shape

Build one Go binary with explicit modes and publish one immutable container image. Deploy that image into these roles:

| Runtime role | Owns | Isolation reason |
| --- | --- | --- |
| `portal-api` | Authenticated commands and queries | Keeps latency-sensitive call and task actions away from webhook bursts and long-lived streams |
| `provider-ingress` | Telnyx voice, messaging, recording, and transcription webhooks | Uses a short request deadline and acknowledges only after durable receipt |
| `realtime` | SSE update hints | Long-lived requests consume request-concurrency slots and reconnect by design |
| `worker` | PostgreSQL job/outbox processing and reconciliation | Must continue independently of browser and provider request traffic |
| `migrate` | Forward-only schema migration | Runs once before traffic movement, never during every application instance startup |

This is deployment-role separation, not microservices. All roles import the same `Access`, `Work`, `HumanCalling`, `Messaging`, and `EvidenceArchive` modules and use the same schema. There is still one owner for every invariant and no network API between domain modules.

Cloud Run formally distinguishes HTTP services, run-to-completion jobs, and continuous worker pools. A worker pool has no public endpoint and is intended for continuous non-request work, but it is manually scaled; a Cloud Run Job is appropriate for a migration that runs and exits. [Cloud Run resource types](https://docs.cloud.google.com/run/docs/overview/what-is-cloud-run)

Start with the roles above, but do not create separate repositories, schemas, domain APIs, Kafka topics, or Redis-owned state. If operating five deployments is too much for the pilot, `realtime` can initially live with `portal-api`; `provider-ingress`, `worker`, and `migrate` should still remain operationally distinct because their acknowledgement, execution, and rollout contracts differ.

## Concurrency and PostgreSQL correctness

Multiple logins and concurrent calls are normal workload, not a reason to abandon PostgreSQL.

Use database-enforced transitions rather than process locks:

- elect one call winner with one conditional `UPDATE ... WHERE offer_state = 'OFFERED' RETURNING ...`, or an equivalent unique invariant;
- use a monotonically increasing row version for stale browser commands;
- insert idempotency receipts and provider event IDs behind unique constraints;
- keep transactions short and lock shared entities in one deterministic order;
- claim job rows with `FOR UPDATE SKIP LOCKED`, which PostgreSQL explicitly supports for multiple consumers of a queue-like table; and
- retry a whole transaction, not only its final statement, after a serialization failure or lost connection.

PostgreSQL documents `SKIP LOCKED` as unsuitable for a general-purpose consistent view but suitable for avoiding contention among consumers of a queue-like table. [`SELECT ... SKIP LOCKED`](https://www.postgresql.org/docs/current/sql-select.html) PostgreSQL's `INSERT ... ON CONFLICT` uses a unique constraint or index as the concurrency arbiter, which is the correct boundary for webhook and command deduplication. [`INSERT ... ON CONFLICT`](https://www.postgresql.org/docs/current/sql-insert.html)

The unavoidable database/provider gap must be explicit. A database transaction cannot atomically commit with a Telnyx API call. For latency-sensitive commands such as Answer:

1. commit ownership and a durable provider-command record with a stable command ID;
2. issue the Telnyx request immediately after commit;
3. let a worker retry or reconcile if the process dies or the response is uncertain; and
4. mark connected/delivered only from provider evidence.

Telnyx ignores duplicate Voice API commands with the same `command_id` within a 60-second window. That is useful protection, but the portal must still persist command identity and reconciliation state beyond that provider window. [Telnyx Voice webhook and command behavior](https://developers.telnyx.com/docs/voice/programmable-voice/receiving-webhooks)

## Connection budget and scaling

The main infrastructure risk is not PostgreSQL itself. It is **connection-pool fan-out**:

```text
worst-case application connections
  = sum(each runtime's service max instances × pool max)
  + fixed worker connections
  + Better Auth / Next.js connections
  + dedicated LISTEN connections
  + migration and operational headroom
```

Cloud Run permits up to 100 Cloud SQL connections per container instance, but that is a platform ceiling, not a safe pool setting. As instances scale, every instance gets its own pool, so the total grows with every deployment role. Google recommends explicitly limiting open connections per instance and notes that broken connections must be re-established. [Cloud Run to Cloud SQL connection limits](https://docs.cloud.google.com/sql/docs/postgres/connect-run) [Cloud SQL connection management](https://docs.cloud.google.com/sql/docs/postgres/manage-connections)

Before production:

1. read the actual database limit with `SELECT setting FROM pg_settings WHERE name = 'max_connections'`;
2. set a small `pgxpool.MaxConns` independently for every role;
3. set a **service-level** maximum instance count for every Cloud Run service;
4. include the Next.js/Better Auth pool and worker pool in the same budget; and
5. leave explicit capacity for overlapping deployments, migrations, recovery, autovacuum, and operator access.

The checked pilot contract replaces the earlier percentage heuristic with an
exact 22-connection reservation: 11 across configured request-service maxima,
2 across one old and one new worker revision, 1 migration connection, 5 for one
extra instance of every request role, and 3 operator/recovery connections.
Production still reads the actual `max_connections` limit before deployment.
Cloud Run can temporarily exceed a configured maximum, so this is not claimed
as a hard physical ceiling. Any later pool or instance change must recalculate
the reservation and repeat the production-shaped contention tests.

Cloud Run recommends service-level maximum instances when the goal is to protect a backing database. Revision-level limits can briefly fan out during overlapping deployments, and rapid spikes can temporarily exceed a configured limit. [Cloud Run maximum instances](https://docs.cloud.google.com/run/docs/configuring/max-instances) Concurrency per instance is also a deliberate control: Google recommends beginning below a very high default—its example starting point is 8—then increasing from measurement. [Cloud Run concurrency](https://docs.cloud.google.com/run/docs/about-concurrency)

Do not adopt Cloud SQL Managed Connection Pooling on day one merely because it exists. It requires Enterprise Plus, and transaction-pooling mode does not support `LISTEN`. Start with small direct `pgxpool` pools. If connection surges later justify managed pooling, route ordinary short transactions through it while retaining a dedicated direct connection for `LISTEN/NOTIFY`. [Cloud SQL Managed Connection Pooling](https://docs.cloud.google.com/sql/docs/postgres/managed-connection-pooling)

The initial zonal Cloud SQL target has no standby failover. The Go service must
use bounded connection acquisition, exponential backoff with jitter for
reconnects after recovery, and idempotent whole-operation retries only where
safe. If regional HA is adopted later, failover still closes existing
connections and requires the same recovery behavior. [Cloud SQL high availability](https://docs.cloud.google.com/sql/docs/postgres/high-availability)

## Webhook durability, retries, and ordering

The provider-ingress success boundary should be:

```text
raw request
  → verify signature and timestamp
  → insert ProviderEvent by unique (provider, event_id)
  → insert/ensure durable processing job in the same transaction
  → commit
  → return 2xx
```

Do not acknowledge before commit. If PostgreSQL is unavailable, return a failure and let Telnyx retry. Do not execute call-control logic, download media, or call another provider before acknowledgement.

Telnyx states that webhooks can arrive out of order, concurrently, and more than once; non-2xx or slow acknowledgements are retried, and handlers should return 2xx within two seconds. Webhooks contain a unique event ID and are signed with Ed25519 headers. [Telnyx webhook fundamentals](https://developers.telnyx.com/development/api-fundamentals/webhooks/receiving-webhooks) Voice events also carry call-leg and call-session identifiers for correlation. [Telnyx Voice webhooks](https://developers.telnyx.com/docs/voice/programmable-voice/voice-api-webhooks)

Therefore:

- deduplicate receipt by provider event ID, not by timestamp or phone number;
- store the raw verified payload before normalization;
- make normalized transitions accept valid late facts without regressing terminal state;
- never assume delivery order;
- make each processor attempt idempotent; and
- expose permanently failed or uncorrelated events for repair instead of dropping them.

Telnyx supports a failover webhook URL, but a second hostname backed by the same service and same regional database is not regional disaster recovery. Treat it as endpoint-level protection only unless its durable landing path has a genuinely independent failure domain.

## SSE and long-lived requests

SSE remains the right application transport because the browser only needs update hints and can refetch authoritative state over HTTP. It is simpler than adding an application WebSocket and is tolerant of instance replacement.

Cloud Run treats long-lived streams as requests. A request defaults to a five-minute timeout and can be configured up to 60 minutes; clients must reconnect and are not guaranteed to reach the same instance. [Cloud Run request timeouts](https://docs.cloud.google.com/run/docs/configuring/request-timeout)

The realtime contract should therefore be:

- configure a jittered application stream lifetime with explicit margin below
  the Cloud Run request timeout;
- send heartbeats so dead connections are detected;
- connect through `ready`, then perform exactly one full authorized
  reconciliation at initial connection and after every reconnect;
- coalesce disposable hints to the highest observed Practice version with one
  reconciliation in flight, never applying an older snapshot;
- retry a failed stream immediately once, then use exponential full jitter with
  a bounded cap; only after a short grace expose degraded freshness and begin
  bounded jittered HTTP fallback polling;
- send stable IDs and row versions only, never treat an SSE event as durable state; and
- count every open stream against the realtime service's concurrency and capacity.

Loss or recovery of a runtime's PostgreSQL listener changes the listener
generation and closes every stream from the old generation. This prevents a
browser from appearing live while its runtime is blind; the reconnect path is
the resynchronization path.

`LISTEN/NOTIFY` is acceptable only as cross-instance wake-up. PostgreSQL delivers notifications to sessions listening at that moment, after transaction commit, and can fold identical notifications from one transaction. The durable rows must remain the recovery mechanism. [`NOTIFY`](https://www.postgresql.org/docs/current/sql-notify.html)

## Authentication boundary

Better Auth is compatible with this topology, but it should remain an authentication adapter rather than the product authorization authority.

Better Auth's JWT plugin exposes token and JWKS endpoints specifically for services that cannot use the browser session directly. Its default token expiration is 15 minutes; key rotation is optional and disabled unless configured. [Better Auth JWT](https://better-auth.com/docs/plugins/jwt)

For production:

- keep human sessions database-backed and support multiple active sessions;
- issue short-lived JWTs for direct Go calls;
- verify signature, `kid`, issuer, audience, subject, and expiry locally in Go;
- cache JWKS but refresh on an unknown `kid`;
- enable signing-key rotation with an overlap grace period;
- keep the JWT payload minimal; and
- resolve practice, location, role, and current membership from Acuity's PostgreSQL tables, not from stale JWT claims.

This keeps routine Go authorization independent of a Next.js network hop. The remaining risk is revocation lag for an already issued JWT, bounded by token lifetime. Test sign-out, membership removal, key rotation, concurrent login, and auth-schema migration before declaring Better Auth production-ready for this product.

## Clean deployments and migrations

Use one immutable image digest for every role and this release order:

1. deploy the image as a no-traffic tagged revision and smoke-test startup and dependencies;
2. run `migrate` once as a Cloud Run Job;
3. deploy compatible API, webhook, realtime, and worker revisions;
4. shift traffic gradually and monitor;
5. backfill asynchronously when needed; and
6. remove old columns or behavior only in a later release.

Every database change must use expand/contract compatibility so both old and new revisions can run during rollout and rollback. Cloud Run can split traffic and preserve in-flight requests while revisions overlap, so rollback is only safe if the schema remains backward-compatible. [Cloud Run rollouts and rollbacks](https://docs.cloud.google.com/run/docs/rollouts-rollbacks-traffic-migration)

Do not run migrations from every application instance. For large live tables:

- add nullable columns before backfilling and enforcing `NOT NULL`;
- add supported constraints as `NOT VALID`, then validate separately to reduce writer blocking; and
- create indexes with `CREATE INDEX CONCURRENTLY`, accounting for its extra scans, inability to run inside a transaction block, and possible invalid-index cleanup after failure.

PostgreSQL documents that many `ALTER TABLE` forms take strong locks and that `NOT VALID` plus later validation reduces impact on concurrent writers. [`ALTER TABLE`](https://www.postgresql.org/docs/current/sql-altertable.html) It also documents that normal index creation blocks writes while `CREATE INDEX CONCURRENTLY` permits writes at the cost of more work and special failure handling. [`CREATE INDEX`](https://www.postgresql.org/docs/current/sql-createindex.html)

All processes must handle `SIGTERM`: stop taking new work, cancel or finish short requests, release job leases, close database pools, and end SSE streams. Cloud Run provides a 10-second shutdown period before forced termination, so durable state—not shutdown cleanup—must guarantee correctness. [Cloud Run container contract](https://docs.cloud.google.com/run/docs/container-contract)

## Production proof required

The architecture is ready to build, but it is not production-proven until these tests pass:

1. simultaneous accepts across multiple Go instances produce one call winner;
2. reordered, duplicate, and concurrent Telnyx events produce one valid state;
3. a process killed after database commit but before or after a Telnyx request reconciles correctly;
4. webhook receipt stays below Telnyx's two-second deadline under burst load;
5. the calculated connection reservation covers peak load, measured autoscaler overshoot, and an overlapping rollout;
6. a Cloud SQL outage causes visible errors and recovery after restore without false success;
7. SSE clients reconnect and reconstruct current state after timeout, revision rollout, and instance death;
8. a worker killed mid-job does not lose or double-apply the effect;
9. an application rollback works after every migration; and
10. backup restore and regional recovery procedures meet the chosen RTO/RPO.

The first checked envelope is 5–10 logged-in staff, 7 webhook requests in the
highest visible second, 35 in the busiest visible minute, and about 5,544
webhook events per day. Events are not calls. The deterministic proof uses 25
simultaneous signed webhook requests and 10 concurrent staff commands. Live
Cloud Run, Cloud SQL, Telnyx, Florida latency, active-call, SSE, and retention
measurements remain acceptance gates; they do not require a different
architecture unless the evidence exceeds the checked bounds.

## Decision

Proceed with the stack, with runtime-role isolation and an explicit connection budget added to implementation planning.

Do not move to microservices, Kafka, Redis-owned state, Kubernetes, or a different database preemptively. Revisit those choices only when measured load, a contractual multi-region requirement, or independent team/release ownership creates a concrete boundary PostgreSQL and the modular monolith cannot meet.
