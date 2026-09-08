# Booking analytics verification

Current scope supersedes the earlier caller-grouping prototype. Product branch:
`codex/analytics-booking-accuracy`, based on `4430d77`. Companion agent branch:
`codex/analytics-phone-lookup-evidence` in `livekit-agent`.

## Final behavior

- Each completed availability-search call counts independently, including calls
  from the same phone number. Only a booking on that same call converts it.
- Bookings retain the live distinct-confirmed-appointment definition. P50 pools
  valid individual booking-attempt call durations, including missed attempts.
- All metrics use each call's patient classification. Successful current or
  legacy identity outcomes establish the reporting group; otherwise one/multiple
  phone matches assume existing and missing/no-match evidence assumes new.
- The UI retains New, Existing, and Total with Bookings, Conversion, and P50.
  Phone grouping, grouped attempt selectors, and unused confirmed/assumed API
  subgroups were removed. P90 is absent from the UI.
- Historical assumptions are separate from provider evidence. The schema
  migration and private-input data backfill are described in
  `booking-analytics-release.md`. Production execution remains pending.

## Checks

Passed locally:

```sh
go generate ./backend/internal/api
pnpm --dir web api:generate
TEST_DATABASE_URL='postgres://127.0.0.1/acuity_analytics_test?sslmode=disable' go test -p 1 ./backend/internal/interaction ./backend/internal/httpapi ./backend/internal/migrations -count=1
pnpm --dir web lint
pnpm --dir web typecheck
pnpm --dir web test:unit
git diff --check
```

The migration suite includes a real upgrade from 0062: existing facts remain
unchanged while historical success evidence is classified. The data migration
is tested for dry-run rollback, apply, idempotence, native-evidence preservation,
and rejection of conflicting or unmatched evidence.

The required broad command was run:

```sh
TEST_DATABASE_URL='postgres://127.0.0.1/acuity_analytics_test?sslmode=disable' go test -p 1 ./backend/... ./deploy -count=1
```

It was not green: local migration/acquisition timeouts and the unchanged
`TestExecutorOwnsTransactionDeadlineAndRelease` rollback failure. That rollback
failure was previously reproduced on a clean HEAD baseline. No timeouts or
assertions were weakened. The subsequent serial run of all affected packages
passed, including the complete migrations package.

The companion agent's unchanged patch previously passed formatting, lint,
typecheck, all 881 tests, and its office-knowledge benchmark. Five cases prove
that closeout saves only the lookup status, without patient record details.

## Preview and release evidence

A read-only production snapshot supplies the separate local preview database.
The current release backfill was dry-run and applied only to that local preview,
using 3,120 privately stored positive historical phone-match assumptions.
Native current/legacy identity evidence was restored from the source first.
The original saved snapshot remains intact; no production data was written.

Source booking totals are 124 for 7 days and 540 for 30/90 days. The 30/90 totals
match because source history starts August 9. All comparisons use the same
Practice, office scope, and America/Los_Angeles calendar boundaries.

A bounded subagent recheck found no correctness or safety issues in the revised
per-call math, legacy classifier, or prepared release backfill. The migration
and release backfill are included for review. No deployment or production
backfill was performed. CI, deployed application behavior, provider execution,
and production backfill remain unverified.

Final browser and release-preview checks passed:

```sh
E2E_DATABASE_URL='postgres://127.0.0.1/acuity_analytics_e2e?sslmode=disable' ./scripts/run-e2e.sh booking-analytics.spec.ts booking-review.spec.ts
node .codex-work/booking-review-local/verify-full-preview.cjs
```

Both synthetic browser journeys passed. The saved screenshot contains synthetic
fixtures only. The private preview verifier independently compares every API
patient row and total with SQL call counts, booking counts, conversion fractions,
and `percentile_cont`, then checks all three UI range buttons and absence of P90.
Final per-call conversion: 7 days, 124/245 (50.6122%); 30/90 days, 540/964
(56.0166%). P50 is 440.563 seconds and 390.212 seconds respectively. These are
per-call metrics; the prior caller-grouped values are superseded.
