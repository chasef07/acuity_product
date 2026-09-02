# Admin analytics: local query verification

Verified September 2, 2026 against disposable local PostgreSQL databases.
Nothing in this report is production or deployment evidence.

## Findings and changes

The first implementation sorted wide booking records that contained transcript
JSON. On the larger fixture it wrote about 56 MB of temporary sort data, with a
1,433 ms query. Staff queries also scanned older history and sorted rows that
only needed aggregation. Concurrent reports could occupy the shared portal pool.

The final implementation:

- Maintains four booking facts alongside committed source evidence, with a
  trigger that also covers older application revisions during rollout.
- Backfills historical calls in resumable batches of 500, with a real 120-second
  whole-CALL timeout on a pinned connection and one-second lock waits, and builds indexes
  concurrently. The final booking index contains every field the report reads.
- Reads a maximum of 50,001 source records and rejects oversized reports rather
  than presenting partial totals. Accounts are bounded separately at 5,000.
- Removes unnecessary staff-query sorts and uses indexed reporting dates.
- Releases the database transaction before computing task percentiles.
- Shares one analytics permit per portal instance; excess reports receive 429
  with Retry-After. Normal portal work does not use this permit.
- Caps analytics requests at two seconds, statements at 1.5 seconds, lock waits
  at 100 ms, and SQL sort memory at 4 MB; parallel SQL workers are disabled.
- Aborts and fences obsolete browser requests. Switching Bookings/Conversion/
  Duration reuses one report; neither report polls in the background.

## Measured results

The isolated scale fixture contained 106,167 AI calls, 103,705 human Calls and
Staff CallLegs, and 100,924 Tasks. It mixed reporting-period activity with older
history. Added AI calls included approximately 50 KB of synthetic transcript
JSON each. No patient data was used.

| Measurement | Result |
| --- | ---: |
| Original 90-day booking SQL | 1,433 ms; about 56 MB temporary sort writes |
| Final 90-day booking SQL | 6.7 ms; index-only scan; no sort or temporary writes |
| Booking HTTP response p95 | 28.6 ms |
| Staff HTTP response p95 | 192.1 ms |
| Ordinary access reads during concurrent analytics burst | 12/12 HTTP 200; p95 16.6 ms |
| Analytics burst | 1 report admitted, 19 returned HTTP 429 promptly |

HTTP timing covers four sequential requests to each report for each 7/30/90-day
window, using the actual authenticated Go HTTP handlers and a three-connection
portal pool. The burst sent 20 analytics requests alongside 12 ordinary Access
reads. These are local measurements, not a sustained production capacity test.

## Verification commands

Executed against `acuity_booking_test` and `acuity_booking_e2e`, both disposable:

```sh
TEST_DATABASE_URL=... go test -p 1 ./backend/... ./deploy -count=1
go vet ./backend/... ./deploy
GOTOOLCHAIN=go1.26.7 go generate ./backend/internal/api
pnpm --dir web api:generate
pnpm --dir web lint
pnpm --dir web typecheck
pnpm --dir web test:unit
pnpm --dir web build
pnpm --dir web audit --prod
E2E_DATABASE_URL=... E2E_BASE_URL=http://127.0.0.1:3107 \
  E2E_PORTAL_API_URL=http://127.0.0.1:18080 \
  pnpm --dir web exec playwright test booking-analytics.spec.ts staff-analytics.spec.ts
git diff --check
```

After the review fixes, the full serial Go suite, Go vet, frontend lint and
type checking, 243 unit/render tests, production build, dependency audit, and
both analytics browser journeys passed. The larger workspace reconnect journey
was skipped against the live design workspace because its isolated provisioning
and runtime harness was not supplied; that journey remains a CI gate.

The focused migration test exercises more than one backfill batch, a partially
projected starting state, interruption after the first committed batch, resumption,
repeat application, and subsequent evidence changes. A separate one-connection
test proves a real PostgreSQL statement timeout preserves committed progress
and prevents temporary session settings from leaking back into the pool.
The browser journeys use real database aggregates and cover Admin visibility,
Staff denial, office filters, completion/phone metrics, report switches, retry,
sparse chart points, incomplete classification and availability coverage,
sorting without another API request, and chart labels fitting inside their viewport.

Production rollout must apply the migrations and finish the backfill before
serving the new report query. Production latency and sustained concurrency
remain unverified.

## Parallel review improvements

Backend, frontend, and metric reviews found and corrected an ineffective
procedure-local statement timeout, hidden sparse chart observations, and a
shared missing-duration flag that incorrectly hid both phone directions.
The standalone preview and duplicate browser aggregation were removed. The
workspace rail and projection now share one Practice Admin access predicate.

The first aggregate-only, read-only production check found 184 of 200 booked
calls unclassified by the initial outcome-only rules. That finding exposed an
incomplete classifier, not absent patient evidence: confirmed booking receipts
already include the actual appointment type.

A follow-up read-only comparison on the latest 200 Abita Eye Group completed
BOOKING calls in the preceding 30 days used the exact old and new SQL classifier
expressions against the same records (September 2, 2026, 21:47 UTC):

| Initial classification | Corrected classification | Calls |
| --- | --- | ---: |
| Existing | Existing | 16 |
| Unclassified | Existing | 84 |
| Unclassified | New | 100 |

All 200 had matching receipt/appointment IDs and explicit new/established type
labels. Type IDs independently agreed with those labels. Corrected coverage
was 200/200: 100 new and 100 existing. This is a bounded sample, not proof of
complete coverage for every historical call or unbooked conversion denominator.

After explicitly defining post-op as existing, a second bounded read-only audit
covered every completed confirmed booked call in the last 90 complete calendar
days (America/Los_Angeles): **467/467 classified, 216 new and 251 existing**.
The 30-day cohort contained the same 467 calls; the 7-day cohort contained 124
(66 new, 58 existing). No booked calls remained unclassified in those windows.
The classifier was evaluated as a SELECT; no production backfill was run.

The regression test initially reproduced successful patient creation being
classified as unknown. It now covers all twelve receipt labels (including post-op as existing), missing legacy
outcomes, family-member switches, receipt precedence, failed/mismatched
receipts, superseded creation, and unsupported types. The browser fixture also
uses new-patient and post-op booking receipts with no patient domain outcomes.

CI initially found compressed OpenAPI bytes differed between local Go 1.27.1
and CI Go 1.26.7. Regenerating with the CI toolchain produced byte-identical
output on repeat; decoded contract contents were unchanged.

No production records, application configuration, or cloud resources were changed.
