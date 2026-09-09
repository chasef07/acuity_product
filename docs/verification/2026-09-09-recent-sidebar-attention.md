# Recent sidebar attention verification

Initial implementation verified locally on September 9, 2026. No deployment,
cloud change, or production data mutation was performed.

## Problem and resulting behavior

Previously, Texts counted only loaded rows after client-side unread filtering
and phone grouping. A synthetic 100-thread dataset produced a badge of 14 with
64 eligible phone numbers. Appointments counted the full backend set. Neither
personal attention list expired old entries.

Both lists now use a rolling seven-day window. Appointment age uses the outcome
event, not the scheduled appointment date. Text age uses the latest inbound
message; outgoing replies cannot extend it. Text eligibility and phone grouping
are evaluated before pagination, with a full authorized total returned alongside
rows. The existing historical conversation query remains unrestricted by age.

The menus offer `Mark all read` and `Mark all reviewed` for the signed-in User's
selected authorized Practice/Location scope, including unloaded pages. Bulk
commands preserve other Users' attention, excluded old markers, history, and
Tasks. New inbound messages restore recent unread attention. Clicking retains
per-User read/review behavior; stale responses cannot restore a just-reviewed
appointment. Periodic attention refresh also ages out rows during idle sessions.

The approved policy change is documented in VISION.md and the product spec:
recent attention can age out without resolution; durable follow-up belongs in
Tasks, which do not expire.

## Verification

All data was synthetic. PostgreSQL 16 ran in a task-owned local cluster on
127.0.0.1:55439, with distinct disposable databases ending in `_test` and `_e2e`.
The frontend checks used Node 24.19.0 and pnpm 10.34.5.

| Exact command | Result |
| --- | --- |
| `TEST_DATABASE_URL='postgres://127.0.0.1:55439/acuity_sidebar_test?sslmode=disable' go test -p 1 ./backend/... ./deploy -count=1` | Passed, including database integration tests. |
| `go generate ./backend/internal/api` | Passed; regenerated output reproduced the saved hash. |
| `cd web && pnpm api:generate` | Passed; all regenerated TypeScript files reproduced their saved hashes. |
| `cd web && pnpm lint` | Passed. |
| `cd web && pnpm typecheck` | Passed. |
| `cd web && pnpm test:unit` | 262 passed: 246 logic tests and 16 component tests; none skipped. |
| `E2E_DATABASE_URL='postgres://127.0.0.1:55439/acuity_sidebar_e2e?sslmode=disable' ./scripts/run-e2e.sh messaging-workspace.spec.ts` | Production frontend build passed; six Chromium journeys passed. |
| `git diff --check` | Passed. |

Regression evidence includes:

- 65 eligible text phone groups traversed across seven pages, with duplicate
  phone numbers across Locations kept together and read threads excluded before
  pagination. Counts remain independent of loaded rows.
- The exact seven-day boundary qualifies; a microsecond older does not.
  Recent outgoing activity does not revive old inbound messages.
- Bulk clearing across unloaded pages, cross-Location denial and isolation,
  per-User isolation, retained old markers/history, and open Task preservation.
- 57 recent appointment events, a future scheduled appointment date, individual
  review, bulk review, and expiry without deleting evidence.
- Browser proof of a badge showing 64 with 50 loaded rows; failed bulk saving
  keeps the count and exposes an error; retry clears all 64 and survives reload;
  a new inbound message restores one row. Old appointment events are excluded.
- Refetch after bulk success preserves new arrivals rather than assuming zero.
  Missing totals fail visibly; older snapshots cannot restore reviewed outcomes.
- Visual inspection confirmed the `7d` label fits beside Appointments.

The browser test caught a missing Base UI menu-group wrapper during development;
the corrected menu passed the same journey. An existing API fixture queried
before its own future-dated closeouts; its test clock now advances beyond those
events without weakening the time filter.

## PR preparation on current main

The change was applied without conflicts to main at `4a9485a` in an isolated
worktree. The full serial database-backed Go suite, frontend lint, typecheck,
all 262 frontend tests, contract regeneration, and whitespace checks were rerun
there and passed. Browser/build evidence above is from the initial implementation
checkout; the frontend change is identical. Unrelated workspace files and the
already-merged analytics commits are excluded from this PR.

## Evidence limits

These are local application-path and disposable-database results. CI, deployed
application behavior, production backlog totals, and live provider behavior were
not verified. Provider interactions in the browser journey used the local fixture.
