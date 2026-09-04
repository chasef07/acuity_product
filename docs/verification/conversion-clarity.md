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

The chart mixed three populations, labeled only the overall series, and used
smoothed curves between daily observations. The coverage note counted all
completed calls rather than the availability-check cohort being compared.

## Local reproduction and correction

The browser fixture uses the real database projection, authenticated Go API,
and production-built workspace with seven synthetic calls. Six invoke
`get_availability` and four book. One invocation remains unclassified and one
call has no availability history. Repeated invocations count once per call.

Before the fix, the page hid unclassified invocation calls and mixed three
populations on the chart. The updated browser regression failed against that
implementation.

The corrected page shows one overall daily trend and a complete Conversion
table:

| Patient status | Booked calls | Calls with availability attempts | Conversion |
| --- | ---: | ---: | ---: |
| New | 2 | 3 | 66.7% |
| Existing | 2 | 2 | 100.0% |
| Unclassified | 0 | 1 | 0.0% |
| Total | 4 | 6 | 66.7% |

The table uses converted calls, not distinct appointment identifiers. The
headline states its numerator and denominator, the two outcome counts add up
to the denominator, and coverage uses the same six calls. The partial-group
note explains that booking receipts can supply status, making the classified
group rates higher. Sparse observations remain visible; no-invocation days
remain missing, not zero. Bookings and Duration retain their existing behavior.

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
  ./scripts/run-e2e.sh booking-analytics.spec.ts
git diff --check
```

Lint and type checking passed. All 233 library tests and 12 render tests
passed; the production dependency audit reported no known vulnerabilities.
The browser harness built the Go runtimes and frontend production bundle,
then passed the real booking analytics journey with no skipped test. That
journey also covers duration, report retry, office scope, and Staff denial.
The final API keeps the intermediate `preciseSearchCalls` field as deprecated
rolling-deploy compatibility; it remains zero and the current frontend does
not display it. A later release can remove it after the old frontend is no
longer deployable. A compensating migration restores the original invocation
projection after the intermediate precise-evidence migrations while retaining
their schema history. No dependency change is included.

## Classification cause

Read-only inspection of the local Agent source found a route where a preloaded
existing patient is activated from the caller's speech without a
`resolve_patient` tool call. Product can classify confirmed bookings from their
appointment type and some other calls from explicit patient receipts, but that
preloaded path leaves no patient-status receipt. This is a concrete reason some
invocation calls remain unclassified, not proof that all 481 followed it.

Availability checks also do not require successful new-patient registration.
Therefore absence of an `add_patient` call cannot establish that a patient is
existing. If all 481 were existing, the existing rate would be 267/777 (34.4%);
overall conversion would remain 496/1,024 (48.4%). That is a hypothetical, not
a proposed reclassification.

## Production read-only audit

After GCP reauthentication, an aggregate-only audit reproduced the supplied
August 4 through September 2 Abita Eye Group report exactly. The database
session enforced `default_transaction_read_only=on`; queries emitted no phone
numbers, patient identifiers, appointment identifiers, transcripts, or tool
arguments. No production state changed.

The 1,024 denominator contains 496 confirmed bookings, 370 calls ending
without an appointment action, 138 successful reschedules, and 20 successful
cancellations. All remain in the requested invocation-based denominator.

The reported 481 unclassified calls contain 126 reschedules, 20 cancellations,
and 335 calls with no completed appointment action. Typed appointment receipts
can immediately recover 89 existing and 40 new classifications from the first
two groups; 352 remain unsupported by those receipts. Of the 335 unfinished
calls, 72 invoked `add_patient`; 205 invoked neither patient tool. At least 36
have only the explicit “verify or create the patient” availability block and
no recognized middleware availability result. These calls still count under
the requested invocation-based metric.

The Agent requires an active patient before it performs the middleware
availability request. Across the 496 confirmed bookings, every one of the 229
new-patient calls invoked `add_patient`; 202 of 267 existing-patient calls used
neither patient tool because the pre-call candidate path can activate them.
This supports the hypothesis that no-patient-tool calls are usually existing,
but it cannot classify blocked attempts or abandoned new-patient work.

The production evidence explains the apparent mismatch: 481 denominator calls
were hidden from the patient-status breakdown, and 146 of those were known
appointment changes. The Product fix keeps every invocation in the denominator,
shows every unclassified invocation in the table, and does not guess that
missing new-patient evidence means existing.
