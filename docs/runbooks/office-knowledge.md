# Office knowledge search

Product owns an imported, versioned office corpus in the existing PostgreSQL
database. Abita owns conversation and calls Product through one read-only
`search_office_knowledge` tool. This release has no Portal editor, submission or
review workflow, general agent feedback intake, or background publication worker.
Knowledge changes do not create patient Tasks.

## Existing infrastructure and preparation evidence

Read-only checks on 2026-09-09 confirmed:

| Item | Observed value |
| --- | --- |
| Project | `acuity-health-prod` |
| Cloud SQL connection | `acuity-health-prod:us-east1:acuity-production` |
| Database | `acuity_product` |
| Engine | PostgreSQL `16.14` (`POSTGRES_16`) |
| Instance tier | `db-custom-2-8192` |
| pgvector | `0.8.5` available; not installed at inspection |
| Portal service account | `acuity-portal-api@acuity-health-prod.iam.gserviceaccount.com` |
| Portal database secret | `acuity-product-east1-portal-database-url` |
| Migration database secret | `acuity-product-east1-migrate-database-url` |
| Latest applied migration | `0065_staff_text_analytics_index.sql` |
| Knowledge tables | Absent at inspection |
| Existing runtime knowledge configuration | No `KNOWLEDGE_*` variables at inspection |

Metadata was queried through an existing Cloud SQL Auth Proxy using
`--gcloud-auth` and sessions forced to `default_transaction_read_only=on`.
No patient tables or secret values were printed. Both existing database roles,
`acuity_portal` and `acuity_migrate`, have `cloudsqlsuperuser` membership and
CREATE on the database and public schema. Neither is a PostgreSQL superuser.
These preexisting role attributes were not changed. Application table authority
is assigned by `backend/internal/migrations/database-grants.sql`; the Knowledge
runtime receives corpus SELECT and retrieval-observation INSERT only.

The Vertex AI API and custom role
`projects/acuity-health-prod/roles/acuityKnowledgePredict` were prepared in this
project. The role contains only `aiplatform.endpoints.predict` and is intended
only for the existing Portal API service account; the live binding was verified.
The reproducible setup command
is:

```sh
GCP_PROJECT=acuity-health-prod bash deploy/knowledge-search-setup.sh
```

This command checks the existing service account, enables the API, creates or
updates this dedicated role, and adds its binding. It does not install pgvector,
create a database, import documents, change Cloud Run, or activate Abita.
It creates no service-account keys. Runtime authentication uses the attached
service account and Application Default Credentials (ADC).

## Model and corpus contract

The application pins `text-multilingual-embedding-002`, 768 dimensions, with
regional prediction in `us-east1`. Document embeddings use
`RETRIEVAL_DOCUMENT`; queries use `RETRIEVAL_QUERY`. Never change the model or
dimension for queries without rebuilding the entire corpus in the same change.
Google's [text embedding contract](https://cloud.google.com/vertex-ai/generative-ai/docs/embeddings/get-text-embeddings)
defines task types and dimensionality. Whole titled sections preserve facts with
their restrictions. The provider must reject oversized inputs rather than
silently truncate them.

The migration uses [Cloud SQL's supported vector extension](https://cloud.google.com/sql/docs/postgres/extensions).
Search starts with [pgvector exact comparison](https://github.com/pgvector/pgvector#querying)
over only the authorized office's current revision. CI uses the official
pgvector PostgreSQL 16 image pinned to version 0.8.5 and its multi-platform digest.
There is no ANN index, global query cache, managed retrieval service, or new
hosting tier.

The initial calibration selects minimum cosine similarity `0.52` and at most
four passages. The real Google embedding evaluation artifact lives in the Agent
repository at `docs/evidence/office-knowledge-google-evaluation.json`. Keep the
fixed cases and before/after coverage with the threshold decision; a passing
retrieval score still does not prove a supported answer or acceptable voice
latency. Recalibrate before expanding beyond the pilot.

Office identity is `(practice_id, office_key)`, including separate routes that
share one Location. Practice comes from the authenticated service identity.
The trusted agent runtime supplies `X-Office-Key`; model arguments cannot choose
a Practice, Location, or route. The API accepts only a short, non-patient query:

```http
POST /v1/agent/knowledge/search
Authorization: Bearer <existing Practice service credential>
X-Office-Key: spring-hill
Content-Type: application/json

{"query":"When does the office close on weekdays?"}
```

The response distinguishes `found`, `no_relevant_information`, and
`temporary_failure`. Passages include section and revision identifiers.
Similarity ranks passages; it does not establish that they answer the question.
Do not send transcripts, names, patient IDs, appointments, or insurance details
for embedding. Tool passages are untrusted data, never executable instructions.

## Reviewed rollout sequence

1. Review and commit the Product and Agent changes. Complete disposable database,
   authorization, model-tool, and real embedding checks before production setup.
   Keep the Abita pilot flag unset during preparation.
2. Apply additive migration `0066_office_knowledge.sql` through the existing
   migration path and reapply the reviewed `database-grants.sql` contract.
   It installs `vector` and adds corpus, revision, passage, and sanitized
   observation tables. Do not execute all pending migrations blindly: inspect
   `schema_migrations` first and follow the production release runbook.
3. Deploy the reviewed Product image through the existing release process.
   Configure only `acuity-portal-api` with:
   `KNOWLEDGE_GOOGLE_PROJECT=acuity-health-prod` and
   `KNOWLEDGE_GOOGLE_LOCATION=us-east1`. For an explicit, reviewed configuration
   update after deployment:

   ```sh
   gcloud run services update acuity-portal-api \
     --project acuity-health-prod --region us-east1 \
     --update-env-vars KNOWLEDGE_GOOGLE_PROJECT=acuity-health-prod,KNOWLEDGE_GOOGLE_LOCATION=us-east1
   ```

   The release script also accepts these variables when invoked directly.
   Ordinary releases leave them unchanged when `KNOWLEDGE_GOOGLE_PROJECT` is
   unset, because deployment uses `--update-env-vars`. The initial full-stack
   example uses `--set-env-vars` and must receive the desired configuration
   explicitly. Provider ingress, realtime, worker, and web do not receive it.
4. Review one pilot corpus against its exact approved repository source commit.
   Preserve qualifications, provider-name variants, prices and exceptions,
   available/not-offered/not-supplied distinctions. Remove workflow instructions
   that are not office facts. Do not manufacture missing information. Record
   source path, source commit, approval and exclusions in `provenance`.
5. Validate a complete import manifest, then explicitly apply using the existing
   privileged migration/operator database credential and an existing bound
   Platform Operator. Source approval is a human responsibility; CLI validation
   is not source approval. Set credentials securely without shell tracing:

   ```sh
   # Export KNOWLEDGE_IMPORT_DATABASE_URL through the approved secret workflow.
   # Export KNOWLEDGE_OPERATOR_EMAIL for the existing bound Platform Operator.
   export KNOWLEDGE_GOOGLE_PROJECT=acuity-health-prod
   export KNOWLEDGE_GOOGLE_LOCATION=us-east1
   go run ./backend/cmd/knowledge-import --file /absolute/path/reviewed-office.json
   go run ./backend/cmd/knowledge-import --file /absolute/path/reviewed-office.json --apply
   ```

   ADC must be usable by the import process as well as the deployed API; a
   working `gcloud` user session alone does not prove local ADC availability.
   The CLI derives audit attribution from the bound operator record. Do not
   include `actorSubject` in the manifest. `id` is a fresh UUID and idempotency
   key, `practiceId` is the verified route's Practice, and `expectedRevisionId`
   is empty only for first import:

   ```json
   {
     "id": "00000000-0000-4000-8000-000000000001",
     "practiceId": "00000000-0000-4000-8000-000000000002",
     "officeKey": "synthetic-office",
     "expectedRevisionId": "",
     "provenance": "Synthetic approved source; replace with reviewed attribution",
     "reason": "Initial reviewed corpus",
     "sections": [{"id":"hours","title":"Office hours","text":"The synthetic office closes at 5 pm. Saturday hours were not supplied."}]
   }
   ```

   Use actual verified identifiers and approved facts for a real import. The
   command prepares all embeddings outside a database transaction and performs
   an atomic pointer change against the expected previous revision. Retry the
   same unchanged manifest after an uncertain result. A conflicting pointer
   requires review; do not silently substitute a new expected revision.
6. Prove authenticated Product search with the real service identity, route, and
   returned revision before enabling the Agent pilot. Then set only the pilot
   Agent runtime:
   `ACUITY_PRODUCT_KNOWLEDGE_PILOT=spring-hill` and
   `ACUITY_PRODUCT_KNOWLEDGE_URL=https://acuity-portal-api-cbuqwpsdsq-ue.a.run.app/v1/agent/knowledge/search`.
   The existing `ABITA_EYE_GROUP_PRODUCT_SERVICE_SECRET` supplies authorization.
7. Run the fixed semantic cases and one representative authorized pilot call.
   Record retrieval coverage, actual model tool consumption, grounded answers,
   and audio latency separately. Compare direct/indirect closing time, Spanish,
   Saturday follow-up, names, exceptions, absent facts, unrelated queries, and
   office changes. Exercise provider timeout and missing-corpus paths. Expansion
   requires a measured median/p95 retrieval and spoken-answer budget agreed from
   pilot evidence.

## Failure, evidence, and rollback

A failed import leaves the prior complete revision searchable. Each replacement
retains immutable historical content and source attribution. Restore approved
older content by importing it under a NEW UUID with the CURRENT revision as
`expectedRevisionId` and an explicit restore reason; do not mutate history.

A migrated office must expose temporary search failure and avoid guessing or
silently switching to repository files. A deliberate rollout rollback is a
separate operation: remove the explicit Agent pilot flag to restore the
unmigrated path after reviewing the repository facts. Clearing
`KNOWLEDGE_GOOGLE_PROJECT` alone disables the provider and leaves pilot queries
failing visibly; it does not perform that Agent rollout rollback. Keep corpus
history and additive tables intact during either rollback.

Check sanitized retrieval observations by scope, revision, outcome, section IDs,
and elapsed time. Never log the raw query, returned office text, tokens,
credentials, or caller context. A complete imported corpus proves database
availability. A successful deployed authenticated search proves that retrieval
path. An observation proves a retrieval occurred. None alone proves a correctly
spoken answer. Zero observations is zero evidence of Agent retrieval.

At preparation time no production schema migration, corpus import, service
deployment, or Agent pilot activation was performed by this runbook work.
Record these later actions and their evidence separately from local and CI
checks. Broader ACU-54 Portal editing/review and feedback ownership remain later
scope, explicitly excluded from this implementation.

## Implementation verification (2026-09-09)

Product implementation starts from `d1ad9ade3ce078ab67b74f61f9fa621d61f70698`.
The isolated Product and Agent branches are both `codex/office-knowledge-search`.
Local verification completed with disposable PostgreSQL 16 and pgvector 0.8.1:

- `TEST_DATABASE_URL=.../acu55_test go test -p 1 ./backend/... ./deploy -count=1`: passed the complete suite, without skipped database packages.
- `go generate ./backend/internal/api` and `corepack pnpm@10.34.5 --dir web api:generate`: regenerated both contracts.
- `corepack pnpm@10.34.5 --dir web typecheck`, `lint`, and `test:unit`: passed; 265 frontend unit/render tests.
- `E2E_DATABASE_URL=.../acu55_e2e ./scripts/run-e2e.sh`: production frontend build and all 33 Chromium journeys passed. The local command used a temporary wrapper for the pinned pnpm version.
- `GOTOOLCHAIN=go1.26.7 go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./backend/...`: no vulnerabilities found.

The first full run exposed the missing entries in the exhaustive expected-grant
matrix; those were added without broadening actual grants. One preexisting
subsecond executor timing test failed during concurrent build load; the complete
serialized rerun passed without weakening it. A compilation race while a review
fix introduced its new source file also disappeared in the frozen full rerun.

Code review found and corrected: corpus foreign keys conflicting with Access's
route reconciliation; read locks requiring mutation permission; missing sanitized
failure diagnostics; and apparent model/dimension configuration that could not
actually vary. Retired routes now retain immutable import evidence while Access
denies retrieval. Query failures log a bounded cause and elapsed time, never
query text or provider bodies. Model and dimensions have one code owner.

The local combined trial imported the exact reviewed facts candidate into a
separate `knowledge_live_test` database, searched through the actual generated
Product HTTP handler and real Google embeddings, and ran the real Abita
AgentSession/model against it. Its evidence is in the Agent repository's
`docs/evidence/office-knowledge-real-model-product.jsonl`. These are local
application/database/provider and text-model observations, not deployed service
or spoken-answer proof. The saved local ADC could not refresh; a temporary
operator-only client used the existing signed-in gcloud token without printing
or persisting it. The committed runtime and import CLI continue to use ADC.
