# Conversion analytics clarity

Production evidence was captured September 3, 2026, and the local implementation
was verified September 4. The audit changed no production data. Release 1.2.0,
which contains the intermediate implementation, deployed successfully through
the release workflow on September 4.

## Failure

The reported page showed overall conversion of 496/1,024 (48.4%), alongside
new-patient conversion of 229/247 (92.7%) and existing-patient conversion of
267/296 (90.2%). The displayed groups omitted 481 availability-check calls.
Their zero conversions explain the lower overall rate. These counts are
derived from the supplied screenshot, not a fresh production query.

The original chart mixed three populations without reconciling their
denominators. The intermediate release then reduced Conversion to one overall
line while Bookings kept the clearer cohort treatment. The coverage note
counted all completed calls rather than the availability-search cohort being
compared.

## Local reproduction and correction

The browser fixture uses the real database projection, authenticated Go API,
and production-built workspace with eleven synthetic calls. Ten invoke
`get_availability`; eight have successful tool outputs, one fails, and one is
incomplete. Two successful executions finish as appointment changes. The other
six form the conversion denominator, and one additional call has no availability
history. Repeated completed searches count once per call.

Before the fix, the page left successful searches Unclassified, so the New and
Existing rows did not add up to the overall denominator. Its chart treatment
also differed from Bookings. The updated browser regression failed against that
implementation.

The corrected page shows total, new-patient, and existing-patient daily trends
and a complete Conversion table:

| Patient status | Booked calls | Completed availability searches | Conversion |
| --- | ---: | ---: | ---: |
| New | 2 | 3 | 66.7% |
| Existing | 2 | 3 | 66.7% |
| Total | 4 | 6 | 66.7% |

The table uses converted calls, not distinct appointment identifiers. The
headline states its numerator and denominator. The New and Existing rows cover
all six completed searches,
so their rates reconcile with the total. Conversion and Duration use the same
three-series monotone line and subtle fill treatment as Bookings. Staff uses the
same treatment for its single task-duration series. Isolated observations retain
a dot instead of disappearing across missing days. The Staff table initially sorts accounts
by inbound time descending.

## Verification

Run from the isolated worktree root using the pinned frontend package manager:

```sh
corepack pnpm@10.34.5 --dir web lint
corepack pnpm@10.34.5 --dir web typecheck
corepack pnpm@10.34.5 --dir web test:unit
corepack pnpm@10.34.5 --dir web audit --prod
GOTOOLCHAIN=go1.26.7 TEST_DATABASE_URL=... \
  go test -p 1 ./backend/... ./deploy -count=1
GOTOOLCHAIN=go1.26.7 \
  E2E_DATABASE_URL='postgres://postgres@127.0.0.1:55436/acuity_conversion_e2e?sslmode=disable' \
  ./scripts/run-e2e.sh booking-analytics.spec.ts staff-analytics.spec.ts
git diff --check
```

Lint and type checking passed. All 233 library tests and 12 render tests
passed; the production dependency audit reported no known vulnerabilities.
The browser harness built the Go runtimes and frontend production bundle,
then passed both booking and Staff analytics journeys with no skipped tests.
These also cover sparse duration points, default Staff sorting, report retry,
office scope, and Staff denial.
The final API keeps the intermediate `preciseSearchCalls` field as deprecated
rolling-deploy compatibility; it remains zero and the current frontend does
not display it. A later release can remove it after the old frontend is no
longer deployable. A compensating migration projects completed tool executions
and exhaustive conversion cohorts after the intermediate precise-evidence
migrations while retaining their schema history. No dependency change is
included.

## Classification cause

Read-only inspection of the local Agent source found a route where a preloaded
existing patient is activated from the caller's speech without a
`resolve_patient` tool call. Product can classify confirmed bookings from their
appointment type and some other calls from explicit patient receipts, but that
preloaded path leaves no patient-status receipt. This explains why the deployed
page currently shows an Unclassified cohort.

The revised product rule intentionally replaces that evidence model for
conversion: a completed search preceded by `add_patient` is New, and every other
completed search is Existing. This is a reporting definition based on tool
sequence, not verified patient identity.

## Production read-only audit

After GCP reauthentication, an aggregate-only audit reproduced the supplied
August 4 through September 2 Abita Eye Group report exactly. The database
session enforced `default_transaction_read_only=on`; queries emitted no phone
numbers, patient identifiers, appointment identifiers, transcripts, or tool
arguments. No production state changed.

The original 1,024 denominator contains 496 confirmed bookings, 370 calls
ending without an appointment action, 138 successful reschedules, and 20
successful cancellations. Excluding the 158 appointment-change calls leaves
866 invocation candidates. The completed-output rule may exclude additional
failed or incomplete executions; the final historical denominator requires a
post-migration production readback.

The reported 481 unclassified calls contain 126 reschedules, 20 cancellations,
and 335 calls with no completed appointment action. Typed appointment receipts
can immediately recover 89 existing and 40 new classifications from the first
two groups; 352 remain unsupported by those receipts. Of the 335 unfinished
calls, 72 invoked `add_patient`; 205 invoked neither patient tool. At least 36
have only the explicit “verify or create the patient” availability block and
no recognized middleware availability result. Completed outputs determine
which of these calls enter the revised denominator.

The Agent requires an active patient before it performs the middleware
availability request. Across the 496 confirmed bookings, every one of the 229
new-patient calls invoked `add_patient`; 202 of 267 existing-patient calls used
neither patient tool because the pre-call candidate path can activate them.
This supports the hypothesis that no-patient-tool calls are usually existing,
but it cannot classify blocked attempts or abandoned new-patient work.

The production evidence explains the apparent mismatch: 481 denominator calls
were hidden from the patient-status breakdown, and 146 of those were known
appointment changes. The Product fix removes reschedules, cancellations,
failed executions, and incomplete executions, then partitions every remaining
completed search into New or Existing from the explicit tool-sequence rule.
