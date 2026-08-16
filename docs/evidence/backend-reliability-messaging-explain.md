# Message Thread query evidence

Evidence was captured on 2026-08-13 and 2026-08-14. Cloud Logging output was
restricted to timestamps, status codes, durations, revision names, and bounded
database metrics. Local database fixtures are synthetic. No patient
identifiers, phone numbers, Message bodies, transcripts, or credentials were
printed or retained.

## Production observation

Cloud Logging confirms the reported slow request: an authenticated
`POST /v1/message-threads/query` returned `200` in `2.529985617s` at
`2026-08-13T21:22:07Z`. It ran on revision
`acuity-portal-api-5069643e-632e-46fc-becf-46ab97bdb0c7`, configured with
Cloud Run concurrency 20 and `DATABASE_POOL_MAX=4`.

In the surrounding seven seconds, the revision recorded 198 successful
database acquisitions with no failures. Acquisition wait was 292 ms at p95
and 378 ms at maximum. The nearest pool sample reported two of four
connections acquired. This does not identify one request's database time, but
it shows the slow connection-holding work was concurrent with material pool
queueing.

The production database was not queried. The active Google identity had no
existing Cloud SQL IAM database login, and no Cloud SQL Auth Proxy was already
available, so inspection stopped at sanitized Cloud Logging and Cloud SQL
metadata rather than widening credential access.

## Deterministic regression

`TestMessageThreadQueryAggregatesActivityBeforeRanking` uses the real
authenticated HTTP handler and production database executor against 2,000
Message Threads, 20,000 Messages, 2,000 Calls with Handoffs, and 2,000 Tasks.
Half the Tasks are linked directly to Message Threads; half exercise the
phone-only fallback.

The first 5,000-Thread representative run made the existing handler return
`503`. The minimized 2,000-Thread test completed and deterministically failed
on the stronger plan invariant: the Call relation and the combined Task
relation each executed 2,000 times, once per candidate Message Thread before
sorting and limiting.

The fixed query scans Calls once and the two split Task branches once each. It
aggregates Message, Call/Handoff, directly linked Task, and phone-only Task
activity before ranking. Only the top 51 ranked Message Threads perform the
latest-Message preview, attachment, and unread lookups.

On PostgreSQL 16, the final warm representative plan completed in 106.4 ms
with 66,012 shared-buffer hits. The real handler held its database connection
for 111.7 ms. The regression asserts the Call/Task operation counts and that
the targeted phone-activity index is selected; it does not use a timing-only
pass/fail threshold.

## Index decision

Splitting the Task `OR` produced two one-pass sequential scans on the
representative shape, so no Task index was added without evidence. EXPLAIN did
show that mapping Call and phone-only Task activity back to Message Threads
was forced through indexes ordered for different access patterns.

The narrow
`messaging_threads_phone_activity_idx (practice_id, location_id, external_phone, id)`
was selected for both mappings. On the same fixture it reduced the plan from
297.7 ms and 131,025 shared-buffer hits to 106.4 ms and 66,012 hits, while the
real handler connection hold fell from about 300 ms to 111.7 ms. Migration
`0037_messaging_thread_activity.sql` contains only this proven index.

## Latest-Message decision

The prior maintained `latest_message_id` proposal improved its Message-only
fixture from 16.726 ms to 14.222 ms but required a backfill, foreign key,
security-definer trigger, rollout compatibility rules, and ordering ownership
on every Message insert. That 15% local improvement did not address the
dominant Call/Task work and did not justify the write-side authority.

The projection and trigger were removed. The read query aggregates Message
activity once for ranking, then uses the existing Message timeline index to
load the latest preview for only the selected page. Delayed and equal-time
Messages retain the existing `(created_at, id)` ordering semantics without a
second maintained source of truth.

## Authenticated Acuity Demo waves

Against the currently deployed concurrency-8/pool-4 revision, three fresh
waves of 20 simultaneous authenticated Acuity Demo Message Thread reads
returned 60 of 60 HTTP `200` responses. Browser-observed p95/max latency was
2.773s/2.838s for the first wave, 379ms/379ms for the second, and 265ms/278ms
for the third.

Sanitized Cloud request logs for the 62 authenticated reads in the same
window, including two workspace reload reads, showed p50 38 ms, p95 91 ms,
p99 2.236s, and a 2.353s maximum. The two slow requests were in the first
wave; the remaining server-side requests were at or below 106 ms. In the
six-second wave window, 65 database acquisitions all succeeded, with p95 48
ms and maximum 244 ms. This verifies that admission control prevents 5xx
failures but does not remove the cold expensive-query tail this change targets.

The checked production contract remains concurrency 8, pool 4, minimum 1,
maximum 3 and is untouched by this change. Read-only Cloud Run inspection
reported the live revision at concurrency 8 and pool 4 but with minimum scale
unset and `maxScale=20`; this configuration drift was not mutated.
