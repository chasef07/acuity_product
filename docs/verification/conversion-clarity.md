# Conversion analytics clarity

Production evidence was captured September 3, 2026, and the local implementation
was verified September 4. The code has not been deployed and no production data
was changed.

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
and production-built workspace with nine synthetic calls. Seven enter the
booking availability denominator and four booked. Six searches have exact
completed middleware evidence; one uses legacy invocation history. One
multi-patient call and that legacy call remain unclassified, one versioned
blocked invocation is excluded, and one legacy call has no availability
history. Repeated checks count once per call.

Before the fix, the page hid unclassified search calls, presented legacy
invocations as completed searches, and mixed three populations on the chart.
The updated browser regression failed against that implementation.

The corrected page shows one overall daily trend and a complete Conversion
table:

| Patient status | Booked calls | Calls checking availability | Conversion |
| --- | ---: | ---: | ---: |
| New | 2 | 3 | 66.7% |
| Existing | 2 | 2 | 100.0% |
| Unclassified | 0 | 2 | 0.0% |
| Total | 4 | 7 | 57.1% |

The table uses converted calls, not distinct appointment identifiers. The
headline states its numerator and denominator, the two outcome counts add up
to the denominator, and coverage uses the same seven calls. The page says that
six have exact completed-search evidence and warns that the one legacy call may
be a blocked attempt. The partial-group note explains that booking receipts can
supply status, making the classified group rates higher. Sparse observations
remain visible; no-check days remain missing, not zero. Bookings and Duration
retain their existing behavior.

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
The backend migration suite and full serial backend/deploy suite cover the
versioned projection and historical appointment-change repair. The API adds one
aggregate coverage count; no dependency change is included.

## Classification cause

Read-only inspection of the local Agent source found a route where a
preloaded existing patient is activated from the caller's speech without a
`resolve_patient` tool call. That path updates identity state but does not
emit the `patient_verified` domain receipt consumed by Product's classifier.
The closeout serializes domain receipts; a successful booking can subsequently
supply the appointment type. This is a concrete source-level evidence gap,
not proof that all 481 reported calls followed that route.

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
cancellations. The reschedule and cancellation calls invoked availability but
cannot be treated as failed new bookings. Removing those 158 known appointment
changes would produce 496/866 (57.3%), before addressing blocked availability
invocations or calls with more than one intent.

The reported 481 unclassified calls contain 126 reschedules, 20 cancellations,
and 335 calls with no completed appointment action. Typed appointment receipts
can immediately recover 89 existing and 40 new classifications from the first
two groups; 352 remain unsupported by those receipts. Of the 335 unfinished
calls, 72 invoked `add_patient`; 205 invoked neither patient tool. At least 36
have only the explicit “verify or create the patient” availability block and
no recognized middleware availability result. An invocation therefore does
not prove that availability was checked.

The Agent requires an active patient before it performs the middleware
availability request. Across the 496 confirmed bookings, every one of the 229
new-patient calls invoked `add_patient`; 202 of 267 existing-patient calls used
neither patient tool because the pre-call candidate path can activate them.
This supports the hypothesis that no-patient-tool calls are usually existing,
but it cannot classify blocked attempts or abandoned new-patient work.

The production evidence identifies two owning data defects: appointment-change
invocations pollute booking conversion, and pre-call patient activation is not
persisted as the patient-status evidence consumed by Product. The Agent fix
now carries successful availability intent and active-patient classification
as versioned structured evidence. Product consumes it without reading
transcript prose. The forward migration excludes legacy appointment changes,
reprojects their typed receipts, and leaves unsupported records unclassified.
