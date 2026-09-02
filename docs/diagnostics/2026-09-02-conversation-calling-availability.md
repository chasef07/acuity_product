# Conversation loading and Calling availability incident

Read-only production evidence collected on 2026-09-02. All times below are UTC.
No production configuration, application data, provider state, or deployment was
changed. Patient content, request URLs, telephone numbers, and identity/session
identifiers are excluded.

## Finding

Both reported screens have matching production API failures. The shared Portal
API database pool repeatedly runs out of available connections while multiple
Calling-state validator queries execute for seconds. This is an active backend
incident as well as a frontend failure-recovery problem. Continuing call audio is
customer-reported; API failures do not prove that provider media disconnected.

## Deployed revision

- Release `1.0.6`, image tag/commit `0f84abdb13153958765f9784af5665dfbeadbc3d`.
- Cloud Build `43f84763-b022-4b11-a78f-a5836d519835` succeeded at
  `2026-09-02T04:07:42.778426Z`.
- Portal API revision
  `acuity-portal-api-43f84763-b022-4b11-a78f-a5836d519835` is serving 100% of traffic;
  Ready transition `04:06:39.996975Z`.
- Web and Realtime use the same revision suffix and each serve 100% of traffic.
- Serving metadata and successful deployment establish the deployed version;
  they do not establish customer-path availability.

## Request evidence

Window: `2026-09-02T09:00:00Z` through `2026-09-02T15:14:30.213649Z`.
Cloud Logging returned 42,244 matching requests across seven hourly slices; every
slice remained below its 20,000-row limit. OPTIONS requests were excluded.
All rows were on the current Portal API revision.

| Route | Successful responses | HTTP 503 | Other responses | 503 latency p50 / p95 |
| --- | ---: | ---: | ---: | ---: |
| GET `/v1/engagements/{phone}/timeline` | 3,948 × 200 | 18 | 0 | 1.502 / 1.504 s |
| GET `/v1/calling/state` | 12,596 × 200; 4,737 × 304 | 112 | 15 × 403; 2 × 401 | 5.291 / 7.032 s |
| PUT `/v1/calling/readiness` | 20,646 × 200 | 69 | 17 × 403 | 1.503 / 2.557 s |
| POST `/v1/calling/softphone/lease` | 83 × 200 | 1 | 0 | 1.789 / 1.789 s |

The 18 failed timeline requests cover 13 private route targets. Every failed
request had an earlier successful load and a later successful load for the same
target within the window. All 13 targets had a successful response after their
last observed failure. This confirms transient failed refreshes and subsequent
recovery; it does not establish the behavior of a particular browser instance.

The first matched 503 was Calling state at `12:56:06.281860Z`; the first timeline
503 followed at `12:56:19.928886Z`. Timeline failures also occurred at
`15:07:19.272623Z`, `15:07:20.932122Z`, and `15:07:28.148611Z`.

Successful/4xx Calling-state responses had p50 `0.686 s`, p95 `3.174 s`, and
p99 `5.180 s`; successful timeline responses had p95 `0.583 s`.
The 401/403 observations are separate authorization/authentication outcomes and
are not classified as backend failures.

**Later bounded check, `15:15:00Z`–`15:23:00Z`:** another 82 Portal API 5xx
responses occurred: 44 Calling-state, 12 readiness, seven timeline, and 19 other
routes. The last was at `15:22:19.834138Z`. The incident had not recovered by this
check.

## Database and resource evidence

Portal API structured telemetry, `12:00:00Z`–`15:15:00Z`, complete 1,777-row query:

- 269 pool-acquisition timeouts, with matching `acquire_timeout` execution
  classifications, typically after the configured 1.5-second acquisition limit.
- 73 canceled pool acquisitions.
- 72 database execution events classified `statement_timeout` by the application.
  Cloud SQL error signatures in the same window included 67 cancellations requested
  by the client and one explicit server statement timeout; do not treat all 72
  application classifications as independent server-side statement timeouts.
- 117 samples with all four Portal API pool connections acquired and zero idle,
  out of 1,094 pool samples. Last fully acquired sample: `15:13:48.697021139Z`.

Cloud Monitoring, `12:00:00Z`–`15:15:00Z`, paginated to completion:

- Cloud SQL CPU: median of one-minute maxima `63.6%`, p95 `89.2%`, peak `94.4%`.
- Cloud SQL memory: peak one-minute maximum `56.3%`.
- Portal CPU: peak one-minute p95 `39.9%`; Portal memory: peak one-minute p95
  `14.9%`.
- The queried SQL connection metric returned no series; it is not evidence of
  zero connections. A subsequent direct database activity snapshot did return
  current connection activity.

Read-only PostgreSQL snapshots identify `readCallingStateETag` in
`backend/internal/humancalling/state.go`, whose statement starts
`WITH relevant_call_ids AS MATERIALIZED`:

- At `15:19:45.791507Z`, six validator statements were actively executing without
  a wait event, up to `3.947 s` old; another validator was in `ClientWrite` at
  `3.509 s`. No blocking transactions were reported for these statements.
- At `15:21:53.710282Z`, five were actively executing without a wait event and
  another was in `ClientWrite`, up to `2.079 s` old. No blockers were reported.
- This establishes the slow query owner under live load. It does not isolate one
  expression as the complete cause or establish the performance of a proposed
  rewrite.
- `jit=on`, but `pg_jit_available()=false`; JIT compilation is not a supported
  explanation for these observed statements. `pg_stat_statements` and
  `pg_stat_monitor` are absent.

At `15:21:54.437557Z`, the database held 4,032 Calls, one nonterminal Call, 772
undisposed voicemail Calls, 15,625 CallLegs, and 32,449 provider commands. One Call
had 2,836 commands. Counts change during live operation.

A later aggregate byte-only read of undisposed voicemail state at `15:23:18Z`
found 773 Calls, 3,466 related CallLegs, and 7,514 related provider commands. The
JSON components that the validator constructs totaled approximately 6.7 MB across
all Locations: 708,516 Call bytes, 4,064,637 CallLeg bytes, and 1,922,175 selected
command bytes. No row contents were returned. A single user's scope can be
smaller; this is not the exact customer request payload.

`EXPLAIN (FORMAT JSON)` without `ANALYZE`, using all existing practice/location
scopes and a synthetic unmatched staff subject, estimated cost `2,638,507.56` for
the deployed validator and `11,869.09` for an equivalent uncorrelated voicemail
membership predicate. Both plans excluded JIT. These are planning estimates,
not measured production execution improvements. No full validator benchmark was
run against production during the incident.

## Reproduction and evidence boundaries

Deployment metadata was read using:

```sh
gcloud run services describe acuity-portal-api --region=us-east1 \
  --project=acuity-health-prod \
  --format='json(status.latestReadyRevisionName,status.traffic,status.conditions)'
gcloud builds describe 43f84763-b022-4b11-a78f-a5836d519835 \
  --region=us-east1 --project=acuity-health-prod \
  --format='json(status,finishTime,substitutions._IMAGE_TAG)'
```

Request queries used `resource.type="cloud_run_revision"`,
`resource.labels.service_name="acuity-portal-api"`, explicit UTC lower/upper
bounds, `httpRequest.requestMethod!="OPTIONS"`, and the route filters
`/v1/engagements/[^/]+/timeline` or
`/v1/calling/(state|readiness|softphone/lease)`. Results were grouped by a
normalized route and response status before any output. Raw URL values were not
saved. Queries were split hourly and capped at 20,000 rows per slice.

Pool queries used the same service/revision bounds, `jsonPayload.msg="call_center_metric"`,
and metrics `acuity_call_center_database_pool_acquire`,
`acuity_call_center_database_pool`, and `acuity_backend_database_execution`.
PostgreSQL queries used a local Cloud SQL Auth Proxy with the existing authorized
account, `default_transaction_read_only=on`, and `statement_timeout=5000`.
Credentials were read into process memory without printing or saving them.
All diagnostic database connections closed; the diagnostic local proxy was
stopped at `15:26:41Z`.

Sanitized local evidence files are under `/tmp/acuity-incident-20260902-` with
suffixes `route-aggregate.json`, `pool-aggregate.json`, `resource-aggregate.json`,
`latest-errors.json`, `pg-activity.json`, `pg-plans.json`, and
`validator-bytes.json`. The read-only PG diagnostic helper is
`/tmp/acuity-incident-pg-audit.py`. These temporary files are supporting session
artifacts, not committed product state.

The customer's exact browser request/correlation identifiers, network state,
Telnyx media continuity, and per-call durable/provider outcomes remain unverified.
No patient data, Call/CallLeg state, command, receipt, or production setting was
modified. Local implementation and test results are recorded below separately.

## Verified local fixes

The implementation was prepared on `codex/conversation-load-recovery`, based on
`473dec58a32945303f08705713206391933b5c30`. The production observations above
describe the pre-fix release; the measurements below describe local validation.

- **Backend:** `readCallingStateETag` selects the same latest authorized,
  Caller-backed voicemail as the visible Calling state before loading related
  CallLegs and provider commands. Active Staff Call, incoming offer, disposition,
  and transfer branches remain unchanged. The change does not delete history or
  change the visible voicemail selection rule.
- **Conversation:** a successful timeline response clears the prior error;
  failures retain previously loaded history, expose Try again, and do not claim
  that the history is empty. A missing access token settles loading into a
  retryable error.
- **Calling warning:** background request failures display delayed status or
  unconfirmed availability rather than asserting media disconnected. Successful
  polling and readiness heartbeats no longer clear each other's unresolved
  failure. A refresh error clears only after both state and required Call detail
  succeed. Automatic media reconnection is distinguished from terminal media
  failure. Active media and call controls retain their existing behavior.

The database regression uses a disposable PostgreSQL database with synthetic
4,032 Calls, 772 undisposed voicemails, 15,625 CallLegs, and 32,449 provider
commands, including one Call with 2,836 commands. The selected voicemail is
deterministic. The unchanged-state query used one database connection.

| Local measurement | Before fix | After fix |
| --- | ---: | ---: |
| Serialized validator snapshot | 4,922,552 bytes | 6,007 bytes |
| Query execution | 125.74 ms | 5.91 ms |
| Unchanged conditional request | 135.68 ms | approximately 6–8 ms |

These are local measurements against the same synthetic data shape, not a
production latency promise. The regression asserts a generous 64 KiB snapshot
budget, unchanged hidden-history responses, latest-candidate changes,
disposition revealing the next candidate, voicemail command transitions,
Caller-leg eligibility, selected-Location exclusion, and Admin scope. It avoids
a timing assertion that would depend on the test machine.

## Verification

All checks below passed locally before PR creation. Remote CI and deployed
application-path verification are separate evidence and belong to the release
record.

From the repository root:

```sh
TEST_DATABASE_URL='postgresql://chasefagen@127.0.0.1:55439/conversation_failure_test?sslmode=disable' \
  go test -p 1 ./backend/... ./deploy -count=1
go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./backend/...
E2E_DATABASE_URL='postgres://chasefagen@127.0.0.1:55439/acuity_conversation_e2e?sslmode=disable' \
  ./scripts/run-e2e.sh messaging-workspace.spec.ts human-calling.spec.ts
git diff --check
```

The complete serial backend suite passed. The final capacity/access regression
and existing HTTP conditional/pool tests also passed after test-only refinement.
The browser command passed all eight journeys against the combined final
backend/frontend implementation, using a synthetic provider fixture.
`govulncheck` found no vulnerabilities.

From `web/`, using the repository's pinned pnpm 10.34.5:

```sh
pnpm install --frozen-lockfile
pnpm audit --prod
pnpm lint
pnpm typecheck
pnpm test:unit
NEXT_PUBLIC_PORTAL_API_URL=http://127.0.0.1:18080 \
NEXT_PUBLIC_REALTIME_URL=http://127.0.0.1:18081 pnpm build
```

All 230 library tests and 12 render tests passed. Audit reported no known
vulnerabilities; lint, type checking, and the production build passed. The new
conversation and Calling regressions were observed failing before their fixes.
Independent review found no actionable issue in the conversation recovery or
backend voicemail-selection changes.

## Capacity decision and release gate

Do not increase connection pools or workers as the first remedy for this
incident. The identified expensive query is triggered by browser polling;
additional worker processes do not remove that work. With database CPU already
near its limit during peaks, more concurrent queries can increase contention.

After deployment, verify the serving revision, then compare
Calling-state latency, API 503s, pool-acquisition waits/timeouts, and Cloud SQL
CPU under a comparable staffed workload. Assess capacity only after the query
work is bounded. The local speedup does not by itself prove production recovery,
and no live provider/audio outcome was changed or tested by these fixes.

## Attribution and yesterday comparison

Git history identifies PR #249 (`a22b96a25632f850360d8e057c26c47283158e6a`),
merged on September 1 at 19:35:23 Pacific, as the introduction of the expensive
validator. Before that change, the fingerprint was computed from the already
selected Calling state, including one visible voicemail. The replacement
queried all undisposed voicemail Calls and their related rows before deciding
whether a full projection was necessary.

Cloud Run traffic audit records show release 1.0.5 served from September 1
20:07:55 through 20:09:37 Pacific, then rolled back to 1.0.4. It served again
at 21:04:27 during the next deployment, followed by 1.0.6 at 21:06:40. Thus the
regression was present for the following staffed morning; a failed 1.0.5 build
does not mean that its application never served traffic.

Complete matched Calling-state request windows, 12:00–15:00 UTC (05:00–08:00
Pacific), using hourly slices below the 20,000-row cap:

| Calling-state GET evidence | September 1 | September 2 |
| --- | ---: | ---: |
| Recorded requests | 22,867 | 15,059 |
| HTTP 5xx | 0 | 94 (all 503) |
| Successful response p50 | 80 ms | 664 ms |
| Successful response p95 | 550 ms | 2,803 ms |
| Successful response p99 | 1,217 ms | 4,765 ms |
| Serving release | 1.0.3 | 1.0.6 |

Today's successful-response p95 was 5.1 times higher while recorded request
count was 34.1% lower. Completed request counts do not measure all latent demand,
but this comparison does not support blaming a higher observed request rate.
The deployed query difference and live slow-query fingerprints identify a
specific regression introduced in #249.

That PR also bounded worker recovery and improved deployment validation. Those
are verified implementation/test changes, not proof of a net production gain.
The matched worker window had zero pool-acquisition timeouts on both days.
Provider commands sent were 397 versus 408 and applied receipt observations
were 670 versus 715; these observations are not distinct customer outcomes or
a controlled throughput benchmark. No net live worker improvement is claimed.

The original conditional-poll tests verified response/invalidation correctness
with small Calling fixtures. The production-shaped worker test covered a
different execution path. The added capacity regression now covers accumulated
voicemail history in the frequently polled validator itself.
