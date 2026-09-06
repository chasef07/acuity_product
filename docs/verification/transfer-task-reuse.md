# Reuse the Task for an explicitly linked transfer

## Accepted behavior

The caller's same request retains one Task through AI creation, a transfer to staff,
and missed-call or voicemail recovery. The voice agent passes the successful
`create_staff_task` Task reference to `transfer_call` only for that same request.
For a separate request, the tool receives `taskId: null` and the Agent omits the reference from the Product request. Product must never infer same-need ownership
from phone, source call alone, or most recent Task.

Product validates the Task against service subject, Practice, Location, source call,
and phone before admitting a handoff. It persists the optional reference and includes
it in the handoff's idempotency fingerprint. Existing handoffs without a reference
retain their original fingerprint and behavior. A retry cannot change its reference.

Work reuses the explicitly selected Task in the caller-outcome transaction. Its
original title, source, category, urgency, and completion remain intact. Call evidence
uses the existing Task Interaction relationship and Activity. Concurrent or delayed
recovery facts cannot create another Task or reopen a completed Task. Without a
reference, existing missed-call/voicemail Task creation remains available.

The brief Engagement History entry and side popup are unchanged. This change does
not consolidate historical Tasks, change automatic completion policy, or recover
missing AI source evidence. A successful staff bridge alone does not complete an
AI Task.

## Failing state and proof

`TestRecoveryReusesExplicitAITask` failed against the previous implementation:
recovery created a different `MISSED_CALL_RECOVERY` Task titled `Return missed call`,
even when given the original AI Task reference. It passes after the Work change.
All fixtures are synthetic.

Focused database tests cover original Task preservation, concurrent missed-call and
voicemail retries, wrong-reference rejection, separate needs on the same phone,
completion before first attachment, and completion before delayed recording evidence.
The HumanCalling test crosses actual handoff admission and provider-fact projection;
the HTTP test checks both accepted handoff request formats and persisted Task identity.
Agent tests cross actual Task-tool receipt creation and handoff payload construction,
and verify unknown references, separate requests, and immutable retry payloads.

## Delivery

Local implementation only. Product must release its migration and optional API field
before the Agent begins sending `taskId`. No production data changes, deployment,
provider calls, or historical repair are authorized by this implementation.

## Review

Standards review found one retry-state defect: a local Task-reference mismatch was
classified like an ambiguous provider transfer. Fixed by validating before the
announcement and returning a correctable tool error. A regression verifies failed
admission, mismatched retry, and successful corrected retry. Re-review found no
remaining actionable standards findings. Spec review found no actionable findings.
No review findings were rejected.

The full Agent suite also caught the strict tool-schema requirement. `taskId` is
required and nullable at the voice tool, while remaining optional in Product HTTP.
The strict-schema tests now pass for every registered office tool catalog.

## Validation

All commands use the isolated worktrees and synthetic data. Product database checks
use `TEST_DATABASE_URL=postgres://postgres@127.0.0.1:55440/acuity_transfer_task_test?sslmode=disable`.
Agent commands use Node 22.22.1 and `corepack pnpm@10.34.3`; web commands use
`corepack pnpm@10.34.5` and Node 24.19.0 for the browser build.

- Focused Product: `go test -p 1 ./backend/internal/work ./backend/internal/humancalling ./backend/internal/httpapi -run 'TestRecovery(Reuses|Keeps)|TestTransferredAITask|TestCallingHTTPInterface' -count=1` — passed.
- Agent: `format:check`, `lint`, `typecheck`, and `test` — passed; 43 files, 865 tests, plus the Office Knowledge benchmark (44.87 ms / 250 ms).
- Web: `lint`, `typecheck`, `test:unit` — passed; 238 library tests and 16 render tests.
- Contracts: `go generate ./backend/internal/api && corepack pnpm@10.34.5 --dir web api:generate` — passed; regeneration reproduces the generated files exactly.

The full Go runs caught a test-fixture regression: adding an Abita Office Route to
every shared calling fixture rejected existing fixture names containing underscores.
Restricted the route addition to the new linked-Task fixture. The first run also
failed two unchanged PostgreSQL tests with 20 ms acquisition budgets under concurrent
local test load. The isolated PostgreSQL package rerun
passed. The final full serial suite passed after the fixture correction; results follow below.

The initial browser build could not follow a dependency symlink outside its
Turbopack filesystem root. Cloning the existing dependencies into the isolated
worktree fixed the test setup; the production browser build then passed.

Live Agent model selection of the correct same-request reference and deployed
provider behavior still require release and a controlled live call. No such call
or deployment was performed.


- Browser: `E2E_DATABASE_URL=postgres://postgres@127.0.0.1:55440/acuity_transfer_task_e2e?sslmode=disable ./scripts/run-e2e.sh human-calling.spec.ts messaging.spec.ts` — production build and all 3 matched calling journeys passed. `messaging.spec.ts` matches no file; the messaging workspace suite was not run. No messaging code changed.
- Local UI evidence is the existing calling browser journeys; the new Task-reference behavior is proven through Agent, HTTP, Work, and HumanCalling database integration tests, not a live voice call.

- Final Product suite: `TEST_DATABASE_URL=postgres://postgres@127.0.0.1:55440/acuity_transfer_task_test?sslmode=disable go test -p 1 ./backend/... ./deploy -count=1` — passed, including calling, Work, migrations, HTTP, database deadlines, and deploy tests. No database integration tests were skipped for missing configuration.
- `git diff --check` — passed in both worktrees.

Implementation uses `codex/reuse-transfer-task` in both repos.
Original verification Product base: `db8beee` (includes shared call history PR #281).
Original verification Agent base: `bd117974327c64839d6825b02b5452e6b02636d1`.
The original checkouts' unrelated changes were preserved. The temporary local
PostgreSQL instance was stopped after verification; no production system was changed.
