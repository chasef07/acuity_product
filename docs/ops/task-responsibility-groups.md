# Task responsibility groups — issue #298

This is the real portal implementation. Local evidence does not establish a
production rollout, provider outcome, or independently verified patient resolution.

## Normal portal development

This feature is integrated into the existing `/workspace` layout: grouped rows
under Tasks, central Interaction history, and the contextual Task detail card.
Use the normal local development and Google sign-in configuration described in
[README](../../README.md#interactive-development). There is no feature-specific
role chooser or persistent demo launcher. The ordinary automated browser harness
remains available for synthetic regression tests.

## Responsibility provisioning and rollout gaps

`config/task-responsibilities.json` contains the agreed South Florida roster.
Only Hollywood, Sweetwater, Sweetwater Optical, and North Miami Beach Optical
are configured. Spring Hill and unconfigured Locations retain shared behavior.
Responsibility records resolve existing Access Grants or Memberships by normalized
email and intersect existing Location Scope. No access is created or widened.
The roster contains eleven named Optical primary owners, including Everth;
there is no call center Optical backup. The storage/query supports primary and
backup responsibility with deduplicated results.

Valtenia's and Kristina's email identities remain unconfirmed. They are omitted
from the executable roster. Add their confirmed existing accounts to Pre-op and
Documentation respectively, then rerun provisioning. The checked-in production
Access Grants also do not contain `maria.g@abitaeye.com` or `drbach@abitaeye.com`.
The provisioner checks live existing Memberships too and reports unmatched
accounts instead of guessing or creating grants. Confirm these accounts before
rollout. Existing narrower office scopes produce coverage-gap entries; they
must not be widened automatically to satisfy the roster.

Run responsibility provisioning only against the intended environment after
schema migration and existing-account provisioning:

```sh
DATABASE_URL=... go run ./backend/cmd/task-maintenance \
  --mode responsibilities --input config/task-responsibilities.json \
  --output /private/path/responsibility-report.json
```

Output files are exclusively created with mode 0600. A report lists applied
responsibilities, unmatched accounts, and coverage gaps. A partial roster must
not be presented as fully provisioned. This local implementation did not execute
production provisioning.

## Reviewed open-task reclassification

The maintenance command is an operator tool, not a bulk HTTP endpoint. `plan`
is read-only. Supply the existing Platform Operator subject/email, explicit
Practice UUID, and approved Location UUIDs. The database independently verifies
that operator and scope.

```sh
DATABASE_URL=... go run ./backend/cmd/task-maintenance \
  --mode plan --run issue-298-reviewed-001 \
  --practice PRACTICE_UUID --locations LOCATION_UUID,LOCATION_UUID \
  --actor-subject EXISTING_OPERATOR_SUBJECT --actor-email EXISTING_OPERATOR_EMAIL \
  --output /private/path/plan.json
```

Review each Task's title, complete stored message, current category/version,
proposed category, and reason. Suggestions are conservative lexical aids, not
clinical inference. An empty `NewCategory` means needs review; retain it until a
human confirms the actual request. Correct the proposed category/reason in the
reviewed plan when necessary. Medication PA belongs to Clinical; service PA to
Insurance; records-release authorization to Documentation. Optical prescription
copies are Optical even when the title says “Expedite prescription.” A bare
mention of surgery or an unknown authorization subject remains reviewable.

Applying a reviewed plan requires a separate explicit command:

```sh
DATABASE_URL=... go run ./backend/cmd/task-maintenance \
  --mode apply --input /private/path/plan.json \
  --actor-subject EXISTING_OPERATOR_SUBJECT --actor-email EXISTING_OPERATOR_EMAIL \
  --output /private/path/apply-report.json
```

Each Task is a bounded transaction. Status, version, old category, Practice, and
Location must still match. Changed/completed Tasks are skipped and reported.
Each applied change preserves ID, age, source, immutable ingestion fingerprint,
and earlier Activity, and appends actor/time/old/new/run evidence. The durable
run/Task receipt makes interruption and repetition safe. No Task is deleted,
merged, completed, reassigned, or messaged by this command.

For restoration, generate a new reviewable plan using the original run ID:

```sh
DATABASE_URL=... go run ./backend/cmd/task-maintenance \
  --mode restore-plan --run issue-298-reviewed-001 \
  --practice PRACTICE_UUID --locations LOCATION_UUID,LOCATION_UUID \
  --actor-subject EXISTING_OPERATOR_SUBJECT --actor-email EXISTING_OPERATOR_EMAIL \
  --output /private/path/restore-plan.json
```

Review and apply it using `--mode apply`. Restoration checks the original durable
receipt and applied version and never overwrites subsequent staff edits or
completion. Keep all reports in authorized storage; real Task messages may
contain protected data. No production backfill or restoration was run here.

## Shared agent contract

The captured PHI-free payloads in
`backend/internal/httpapi/testdata/agent-444-staff-tasks.json` come from real
`create_staff_task` executions against an inert HTTP transport in the companion
agent commit `22fd0e5` (issue #444). They include all nine wire categories and
27 distinct synthetic requests. Portal integration submits those objects
unchanged through authenticated HTTP, persists them in PostgreSQL, reads grouped
Tasks, performs staff corrections/feedback, and replays the original submissions.
This establishes local tool/contract/durable portal behavior. It does not prove
unscripted model intake; the agent's model check lacked inference credentials.

Deploy portal taxonomy/migration support and provision responsibilities before
enabling new agent emissions. Legacy Billing submissions map to Other while
retaining their original fingerprint; completed historical Billing remains
readable. Run and review a production dry-run before separately authorizing apply.

### Compatibility release gate

Do not apply this feature migration while the pre-#298 writer is still accepting
Task submissions. That writer's alternate-idempotency-key fallback compares the
editable category and cannot find a reclassified or mapped-Billing Task.
Before the feature rollout, ship the immutable `source_category` snapshot and
matching duplicate fallback as a compatibility release to **every** writer;
only then enable category mutation, Billing mapping, and new agent emissions.
Alternatively use a separately approved write pause with all old writers drained
before migration/promotion. Do not roll back Task writers below that compatibility
release after categories have changed. A normal overlapping rollout of the
pre-feature writer and this feature has not been validated and is not approved
by these local checks. Production release planning must satisfy this gate.

## Local verification — September 9, 2026

All data was synthetic. These are local application and PostgreSQL results;
CI, deployed application paths, real provider outcomes, production provisioning,
and production backfill remain unverified.

| Command / check | Result |
| --- | --- |
| `TEST_DATABASE_URL='postgres:///acuity_298_test?sslmode=disable' go test -p 1 ./backend/... ./deploy -count=1` | Ran the full suite. Fixed migration-grant expectations, query-plan fixture arguments, and shard coverage; final affected rerun below passes. The unrelated PostgreSQL executor timeout test still fails locally. |
| `TEST_DATABASE_URL='postgres:///acuity_298_test?sslmode=disable' go test -p 1 ./backend/internal/work ./backend/internal/workspace ./backend/internal/httpapi ./backend/internal/migrations ./deploy -count=1` | All five packages pass after final changes, with disposable database integration enabled. |
| `pnpm --dir web lint` and `pnpm --dir web typecheck` | Pass. |
| `pnpm --dir web test:unit` | 249 unit tests and 19 render tests pass. |
| `E2E_DATABASE_URL='postgres:///acuity_298_e2e?sslmode=disable' ./scripts/run-e2e.sh` | Ran all 34 journeys: 30 passed initially. Fixed the grouped-detail projection bug and outdated Resolve selectors. |
| `E2E_DATABASE_URL='postgres:///acuity_298_e2e?sslmode=disable' ./scripts/run-e2e.sh human-calling.spec.ts messaging-workspace.spec.ts task-groups.spec.ts` | All 10 affected journeys pass on a fresh database, including all four initial failures. |
| `E2E_DATABASE_URL='postgres:///acuity_298_e2e?sslmode=disable' ./scripts/run-e2e.sh task-groups.spec.ts` | Final grouped journey passes after the filtered-detail projection fix and again after removing the demo launcher/role chooser. Builds the production frontend and real backend roles. |
| `go generate ./backend/internal/api && pnpm --dir web api:generate` | Pass; repeated generation leaves identical Go/TypeScript file hashes. |
| `AUTH_SCHEMA_CHECK_DATABASE_URL='postgres:///acuity_298_schema_check?sslmode=disable' ./scripts/check-auth-schema.sh` | Pass with a disposable schema-check database. |
| `pnpm --dir web audit --prod` | No known vulnerabilities. |
| `go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./backend/...` | No vulnerabilities found. |
| `bash ./scripts/test-release-container.sh` | Could not run the container: Docker is not installed. Host deploy tests pass; container evidence remains outstanding. |

`TestExecutorOwnsTransactionDeadlineAndRelease` fails at rollback with
`database execution failed: connection` on local PostgreSQL 16.15. The same
failure was reproduced from an unchanged archive of starting commit `d1ad9ad`:

```sh
TEST_DATABASE_URL='postgres://127.0.0.1/acuity_298_baseline_test?sslmode=disable' \
  go test ./backend/internal/postgres -run TestExecutorOwnsTransactionDeadlineAndRelease -count=1
```

### Standards review

The naming finding was fixed by renaming the metadata loader. The writer rollout
finding remains an explicit production prerequisite in the compatibility release
gate above. The reviewer confirmed that gate accurately scopes this local handoff;
this is not evidence for a normal overlapping old/new writer rollout.

### Spec review

All identified local findings were fixed: flagged recovery Tasks and their counts,
group membership lost during selected-detail refresh, and Activity detail being
inserted into an empty or completed/flagged queue. Regression tests reproduce the
projection failures and pass with the fixes. Final bounded re-review found no
remaining actionable issue. Pending real-account provisioning is listed above.
