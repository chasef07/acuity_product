# Versioned office knowledge

`offices/*.yaml` is the reviewed source for each office's reusable, non-patient
knowledge. PostgreSQL/pgvector holds published immutable revisions and generated
embeddings. Editing Git does not immediately change the live agent: publication
is a separate GitHub Actions run with a visible receipt.

```yaml
practiceId: 11111111-1111-4111-8111-111111111111
officeKey: synthetic-office
entries:
  - id: former-office-closure
    title: Former office location
    text: >-
      The former office is permanently closed. Do not direct patients there
      or offer to book or move appointments there.
```

Each entry has a stable ID, title, and complete text. Keep one coherent topic
per entry, including all qualifications and restrictions required to interpret
it correctly. Do not split by arbitrary character counts or duplicate the same
fact across entries. Existing large entries can be migrated gradually. Never put
patient information, transcripts, secrets, or operator identities in these files.
The Practice and Abita Office Route identify the authorized retrieval scope.

Write the useful fact directly. Do not include `Status: available` labels,
document-export boilerplate, or instructions explaining when to read the entry.
Keep actual restrictions and explicit missing information in ordinary sentences.
The Abita tool renders selected facts as plain text. Revision IDs and source IDs
remain in the API/audit records, rather than the model's tool response. No extra
AI call rewrites the facts.

## Editing and publishing

1. Edit an office YAML file in a branch and open a PR. Knowledge CI validates all
   sources and rejects unknown fields, duplicate entry IDs, malformed routing,
   empty content, and entries exceeding the importer limits. Review the actual
   facts and preserve their conditions; structural validation is not fact-checking.
2. Merge the reviewed PR into `main`.
3. Read the office's current active revision with the export command below.
   Inspect unexpected changes before publishing; do not blindly retry a conflict.
4. In **Actions → Knowledge → Run workflow**, select `main`, the office filename
   without `.yaml`, and the reviewed active revision UUID. Use `none` only when
   there is no existing corpus for that office.
5. Review the run's publication receipt. The importer generates embeddings,
   activates all entries atomically, records `git:<full commit SHA>` provenance,
   and verifies the active database pointer. Concurrent publication causes a
   conflict. Retrying the same commit, content, and expected revision is replay-safe.
6. If `evals/<office>.json` exists, the workflow automatically runs its synthetic
   questions against the deployed knowledge API and requires the newly published
   revision. Failed retrieval checks fail the workflow after publication; the
   database update remains active for inspection and deliberate rollback. The
   run preserves publication and retrieval JSON evidence as an artifact. An office
   without a fixture has database verification only. Check the agent conversation
   separately: API evidence does not prove the agent's spoken response.

All entries in a file replace the complete office corpus. Removing an entry from
Git removes it from the next published revision; prior revisions remain available
as evidence. To roll back, revert the content in a new PR and publish with the
then-current active revision. No agent deployment is needed for content-only edits.

## Operator commands

Validate without database access or credentials:

```sh
bash scripts/knowledge-validate.sh
go run ./backend/cmd/knowledge-import --source knowledge/offices/spring-hill.yaml
```

Export the live source using an authorized database connection:

```sh
go run ./backend/cmd/knowledge-import --export \
  --practice PRACTICE_UUID --office OFFICE_KEY > /tmp/office.yaml
```

YAML goes to stdout and the active revision receipt goes to stderr. This is a
read-only snapshot, not a command to overwrite reviewed Git files automatically.

For an explicit operator publication, set `KNOWLEDGE_IMPORT_DATABASE_URL`,
`KNOWLEDGE_OPERATOR_EMAIL`, `KNOWLEDGE_GOOGLE_PROJECT`, and optionally
`KNOWLEDGE_GOOGLE_LOCATION` (defaults to `us-east1`). Then:

```sh
go run ./backend/cmd/knowledge-import \
  --source knowledge/offices/spring-hill.yaml \
  --commit FULL_REVIEWED_GIT_SHA --expected-revision ACTIVE_REVISION_UUID --apply
```

The CLI rejects a source that differs from its bytes at the declared Git commit.
The workflow checks out that immutable commit automatically. The CLI resolves the supplied
email to an existing bound Platform Operator; a source cannot forge attribution.
Legacy reviewed JSON imports remain supported by `--file` for operational recovery.

## GitHub production configuration

The workflow reuses the production environment and existing Workload Identity
Federation variables: `GCP_WORKLOAD_IDENTITY_PROVIDER`,
`GCP_DEPLOY_SERVICE_ACCOUNT`, `GCP_PROJECT_ID`, and `GCP_REGION`. Configure:

- `KNOWLEDGE_SQL_INSTANCE`: Cloud SQL instance connection name.
- `KNOWLEDGE_DATABASE_SECRET`: Secret Manager name containing the authorized
  PostgreSQL URL; the workflow overrides its host with the local Cloud SQL proxy.
- `KNOWLEDGE_OPERATOR_EMAIL`: existing bound Platform Operator attribution.
- `KNOWLEDGE_API_URL`: deployed agent API base URL, required when the office has
  a retrieval evaluation fixture.
- `KNOWLEDGE_SERVICE_TOKEN_SECRET`: Secret Manager name containing the authorized
  agent service token for retrieval evaluation.

The publishing service account needs access to both configured secrets, Cloud SQL connection
permission, and Vertex AI embedding permission. It uses short-lived Google
credentials; database credentials stay in process memory and are never committed
or printed. The instance must be reachable by Cloud SQL Auth Proxy from the
runner. Publication is serialized and only permitted from `main` through manual
dispatch; PR runs never authenticate to production.
