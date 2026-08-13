# Messaging Thread list PostgreSQL evidence

Captured locally on PostgreSQL 16 with `EXPLAIN (ANALYZE, BUFFERS)` on
2026-08-13. The disposable fixture contained one Practice, one Location,
5,000 Message Threads, and 50,000 Messages (10 per Thread). It contained no
patient data. Timings below are the second warm run of the production-shaped
Thread-list query with a 51-row limit.

| Plan | Execution | Shared buffer hits | Latest-Message lookup |
| --- | ---: | ---: | --- |
| Before | 16.726 ms | 35,181 | backward timeline-index scan, 5,000 loops |
| After | 14.222 ms | 30,181 | primary-key join, 5,000 loops |

The direct join reduced warm execution time by 15.0% and shared-buffer hits by
14.2%. The former latest-Message subtree consumed 30,000 buffer hits: 20,000
for repeated timeline-index probes and 10,000 for attachment probes. The new
Message plus attachment joins consumed 25,000, removing 5,000 buffer hits and
the per-Thread `ORDER BY ... LIMIT 1` lookup.

The remaining correlated activity calculation still executed once per Thread
to preserve the existing Call and Task activity ordering semantics. This
change intentionally does not introduce `latest_activity_at`, another index,
a cache, or a second authority because the maintained Message reference alone
produced a material improvement.

This is local query evidence, not production latency proof.

## Authenticated burst evidence

Repeated locally on PostgreSQL 16 on 2026-08-14 through the production HTTP
handler and database executor. The fixture again contained 5,000 Threads and
50,000 Messages. Twenty distinct authenticated Staff identities started the
same 50-row Thread query together against a four-connection pool and the
production 1,500 ms acquisition timeout.

- 20 of 20 responses returned `200`.
- No request exhausted the database acquisition timeout.
- The slowest HTTP response completed in 145.6 ms.
- The longest measured database connection hold was 48.7 ms.

`TestMessageThreadBurstUsesFourConnectionsWithoutTimeouts` recreates the
fixture and assertion. This is production-sized local evidence, not a claim
about Cloud Run or Cloud SQL latency. The production runtime contract separately
sets portal concurrency to eight; its live 20-workspace verification is the
production admission-control proof.

## Migration-first rollout compatibility

Migration `0037_messaging_latest_message.sql` owns latest-Message advancement
with an `AFTER INSERT` trigger. It backfills existing Threads, covers old
binaries that only insert Messages during the migration-to-deploy gap, and
orders delayed provider Messages by `(created_at, id)` without regressing the
reference. Portal and worker roles do not receive direct update authority for
`messaging_threads.latest_message_id`.
