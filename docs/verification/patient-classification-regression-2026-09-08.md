# Historical patient classification regression

## Finding

PR #289 (`43d6a25`) switched booking analytics from `booking_patient_group` to
`booking_patient_basis`. The new classifier treats absent patient/phone evidence
as `assumed_new`. The release required a separate historical phone-lookup backfill,
but the inspected production cohort had no backfilled or native lookup statuses.
Missing telemetry therefore changed historical patient categories immediately.

Production inspection used the portal database role through a local Cloud SQL
proxy, with `default_transaction_read_only=on`, a 15-second statement timeout,
and explicit read-only transactions. Only aggregate evidence was returned. No
production rows, schema, cloud resources, or provider data were changed.

For the previous 30 complete UTC days, the Abita Eye Group cohort contained:

| Observation | Calls | Booked calls |
| --- | ---: | ---: |
| Previous existing group, now assumed new | 508 | 221 |
| Same group, excluding successful non-superseded patient-not-found evidence | 448 | 216 |

All 448 calls had completed availability searches. These are per-call counts,
not distinct appointment counts or unique patients. The remaining 60 calls retain
the current new category because explicit negative identity evidence takes
precedence. The demo Practice contributed none of the 448 affected calls.

## Correction

Migration 0064 adds `legacy_existing` as an explicit reporting assumption. The
existing successful patient-outcome and phone-lookup precedence is unchanged.
Only when native and backfilled lookup status are both missing, and no successful
identity result has already classified the call, does the classifier retain the
previous existing category, provided migration 0064 captured that completed call
as historically Existing. New inserts (even backdated imports) and later closeouts
cannot gain that eligibility. An explicit patient-not-found result still assumes
new. This intentionally narrows #289's assume-new rule: unavailable historical
telemetry no longer overrides a previous reporting category.

The API maps `legacy_existing` to the existing patient row. The migration
reprojects eligible completed records without changing any other stored fact or
provider payload. Its trigger also follows corrections to transcript, booking,
and lifecycle evidence, preventing stale fallback categories. A subsequent
phone-lookup backfill or native result replaces the fallback.

The fallback preserves historical reporting; it does not independently prove
that those callers are established patients. Calls without stronger evidence or
a previous existing category still assume new under the current product rule.

## Before and after proof

A synthetic successful historical availability search with no patient/phone
outcome reproduced the failure through the actual database trigger:

```text
TestBookingPatientBasisUsesOutcomesAndPhoneEvidence/
  historical_completed_search_retains_existing_assumption
basis=assumed_new want=legacy_existing
```

After the fix, the same regression passes. Additional local tests cover:

- Upgrade from migration 0063, comparing every stored field except the corrected
  reporting basis and historical eligibility flag before and after.
- New inserts and later closeouts without telemetry remaining assumed new.
- Native transcript and legacy execution formats; explicit negative lookups,
  patient creation, verification, and saved phone matches.
- Transcript and appointment corrections invalidating stale categories.
- The authenticated booking API returning historical booked and non-booked calls
  in Existing, with the correct booking and conversion totals.

## Verification

Follow-up checks use a fresh local database named `acuity_pr291_guard_test`
on port 5432, with synthetic fixtures only.

```sh
export TEST_DATABASE_URL='postgres://127.0.0.1/acuity_pr291_guard_test?sslmode=disable'
go test -p 1 ./backend/internal/migrations ./backend/internal/httpapi \
  -run 'Test(Booking(Patient|Phone|Analytics)|HistoricalPatient)' -count=1
go test -p 1 ./backend/... ./deploy -count=1
go test ./backend/internal/postgres -count=1
go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./backend/...
bash ./scripts/test-release-container.sh
git diff --check
```

- Targeted migration and API tests: passed.
- Full serial database-backed Go suite: all packages passed except the unchanged
  PostgreSQL `TestExecutorOwnsTransactionDeadlineAndRelease` (rollback timeout).
  The isolated PostgreSQL package rerun reproduced it, as did the same command
  on the untouched original checkout (`69a0f08`). No database environment skips.
- Vulnerability check: passed, no vulnerabilities found.
- Release container test: could not run because `docker` was unavailable
  (`command not found`). Deployment runtime validation remains unverified.
- Whitespace check: passed.

No frontend or API schema changed, so frontend/browser and contract-generation
checks were not rerun. Validation was performed locally. Production migration,
application deployment, CI, and the deployed corrected report remain unverified.
