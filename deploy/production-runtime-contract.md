# Production runtime capacity contract

This is the checked production configuration target. It is not evidence that a
Cloud Run or Cloud SQL environment exists. The machine-readable values live in
`production-runtime-contract.json`; CI verifies the warm-capacity invariants and
connection arithmetic.

All Go runtimes and the migration job use one immutable backend image digest.
Only `ACUITY_RUNTIME_ROLE` and role-specific secrets change. The web runtime
uses the matching immutable web digest.

| Runtime | Kind | Concurrency | Min | Max | Pool max | Direct connections |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| web / Better Auth | service | 40 | 1 | 2 | 3 | 0 |
| `portal-api` | service | 20 | 2 | 3 | 4 | 0 |
| `provider-ingress` | service | 20 | 2 | 2 | 2 | 0 |
| `realtime` | service | 50 | 2 | 2 | 3 | 1 `LISTEN` |
| `worker` | worker pool | not applicable | 2 fixed | 2 fixed | 2 | 0 |

`portal-api` and `provider-ingress` each keep two instances warm so staff
commands and provider acknowledgements do not depend on cold starts.
`realtime` also keeps two warm instances for active call-center sessions. The
worker pool keeps exactly two instances available; during a compatible rollout,
the old and new revisions may temporarily contribute two each, but the active
capacity never drops below two.

## PostgreSQL ceiling

One fully scaled revision can open at most:

```text
web                 2 × (3 + 0) =  6
portal-api          3 × (4 + 0) = 12
provider-ingress    2 × (2 + 0) =  4
realtime            2 × (3 + 1) =  8
worker              2 × (2 + 0) =  4
                                      --
single revision                       34
```

The production reservation is:

```text
two complete overlapping revisions   2 × 34 = 68
one migration task, pool max 2                 2
operator and database-operations headroom     10
                                                --
required usable connections                    80
```

Cloud SQL must expose at least 80 connections usable by these application and
operator identities after provider-reserved connections. Deployment stops if
that bound is unavailable. Reduce maximum instances or pool maxima before
deploying; do not rely on connection acquisition timeouts as capacity control.

Every runtime pool uses `MinConns=0`, its checked `DATABASE_POOL_MAX`, a 1500 ms
acquisition/connect timeout, a five-minute idle limit, and bounded connection
lifetime jitter. The migration job uses one task, pool max 2, a 5000 ms timeout,
and no automatic retry. The dedicated realtime `LISTEN` connection is outside
its `pgxpool` and is counted separately for every maximum instance in both
revisions.

## Rollout contract

1. Confirm the database exposes the 80-connection reservation.
2. Run the single forward-only migration job with the migration database role.
3. Apply `database-grants.sql`; new relations receive no runtime authority until
   their exact role grants are added to that file.
4. Start the new request revisions without traffic and verify dependency-aware
   readiness.
5. Keep both worker revisions within the overlap budget while preserving at
   least two active worker instances.
6. Shift traffic gradually, verify tagged revisions, then retire the prior
   request and worker revisions.

Each runtime uses its own service account and database credential.
`portal-api` and `worker` alone receive the Telnyx command credential.
`provider-ingress` receives only the webhook verification key and its
column-scoped receipt authority. `realtime` receives no HumanCalling or Work
table authority. The web runtime receives only Better Auth schema authority.

Actual Cloud SQL failover/restore rehearsal, production load evidence, and
alert-delivery smoke tests remain release gates. This contract does not claim
those gates are complete.
