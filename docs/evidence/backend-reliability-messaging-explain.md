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
