# Office knowledge search

All eight active Abita Office Profiles use Product-owned office corpora in the
existing PostgreSQL database. Abita exposes the read-only
`search_office_knowledge` tool and supplies the trusted active office route and
existing Practice service credential. The model supplies only a short,
non-patient question.

The Agent's keyword resolver, knowledge enrichment hook, filesystem index/cache,
source-file mapping, and Spring Hill pilot gate are removed. Patient/context
projection, structured insurance participation, scheduling, urgent handling, and
demo action tools retain their owners. Knowledge changes do not create patient
Tasks. ACU-55 staff editing, drafts, review/publication UI, and broader feedback
remain outside this release.

## Scope and authority

| Practice | Active office keys |
| --- | --- |
| Abita Eye Group | `spring-hill`, `crystal-river`, `hollywood`, `sweetwater`, `north-miami-beach-optical` |
| Acuity Demo | `ophthalmology-demo`, `new-tampa-demo`, `rheumatology-demo` |

Practice comes from authenticated service identity, not model arguments. Product
validates the supplied office route within that Practice. Distinct routes sharing
a Location retain separate corpora. `sweetwater-optical` is a handoff route whose
Agent trunk currently selects `sweetwater`; `dev` and `mental-health-demo` are
historical Product aliases. None is a new active Agent Office Profile and no
alias inherits knowledge through Location matching. An unimported authorized
route returns temporary failure rather than another route's facts.

The unused dermatology demo source has no configured Agent office or verified
Product route. It remains archived; this migration does not create a new route
or make its facts searchable under an unrelated office.

## Existing infrastructure

- Project: `acuity-health-prod`; region: `us-east1`.
- Cloud SQL: `acuity-health-prod:us-east1:acuity-production`, database
  `acuity_product`, PostgreSQL 16.14, existing `db-custom-2-8192` instance.
- Migration `0066_office_knowledge.sql` is applied; installed pgvector is `0.8.5`.
- Existing `acuity-portal-api` service account has the dedicated
  `projects/acuity-health-prod/roles/acuityKnowledgePredict` role containing only
  `aiplatform.endpoints.predict`. No new service-account key or database instance.
- Portal's database role has corpus/revision/passage SELECT and sanitized
  retrieval-observation INSERT. It cannot replace the corpus pointer. The
  controlled operator import uses the existing migration credential.

The reproducible Google dependency setup is
`GCP_PROJECT=acuity-health-prod bash deploy/knowledge-search-setup.sh`.
It enables the API and prepares that single existing service identity. It does
not deploy an application, import facts, or modify unrelated IAM permissions.

The embedding contract is pinned to `text-multilingual-embedding-002`, 768
dimensions, regional `us-east1`, `RETRIEVAL_DOCUMENT` and `RETRIEVAL_QUERY`.
See Google's [embedding contract](https://cloud.google.com/vertex-ai/generative-ai/docs/embeddings/get-text-embeddings)
and [prediction permission](https://docs.cloud.google.com/vertex-ai/docs/reference/rpc/google.cloud.aiplatform.v1).
There is no ANN index, managed RAG service, query cache, or new hosting tier.
Google requests run without holding a database connection or transaction.
Whole titled sections retain exceptions; oversized inputs fail without truncation.

## Controlled source migration

The Agent repository's `docs/knowledge/manifest.json` maps all eight corpora to
source hashes and import manifests. `docs/knowledge/sources/` preserves all nine
original Markdown files byte-for-byte at
`ad3d57550578096ace95df6600143c66d230189d`; supplemental prompt facts carry their own
exact source/hash provenance. The source archives and manifests are excluded from
the Agent container by its existing `docs/` exclusion and are never runtime input.

Seven deterministic candidates live under `docs/knowledge/imports/`. Spring Hill
reuses its already imported revision and content hash. In the Agent repository:

```sh
node scripts/generate-knowledge-imports.mjs --check
```

The generator verifies pinned sources and applies explicit reviewed transformations
without an LLM. It preserves prices, age/provider restrictions, contact purposes,
demo qualifiers, historical dates, and explicit available/not-offered/not-supplied
meanings. Behavioral safeguards move to runtime instructions or existing action
tools. Historical social guidance is offered at a normal close only when current
retrieval supplied it; there is no unconditional extra lookup on every close.

Validate the Practice against its configured service identity and office route,
then replace the placeholder in the manifest. For a replacement, explicitly set
`expectedRevisionId` to the reviewed current revision. Never silently substitute
that value after a conflict. Use an existing bound Platform Operator for audit
attribution; omit `actorSubject` from the input file.

```sh
# Set KNOWLEDGE_IMPORT_DATABASE_URL securely from the migration credential.
# Set KNOWLEDGE_OPERATOR_EMAIL to the existing bound Platform Operator.
export KNOWLEDGE_GOOGLE_PROJECT=acuity-health-prod
export KNOWLEDGE_GOOGLE_LOCATION=us-east1
go run ./backend/cmd/knowledge-import --file /absolute/path/validated-office.json
go run ./backend/cmd/knowledge-import --file /absolute/path/validated-office.json --apply
```

Normal operation uses Application Default Credentials. During this migration,
saved local ADC could not refresh, so an operator-only temporary client supplied
the existing signed-in gcloud token to the same committed import command/module.
It did not print or persist the token or change saved credentials. The committed
runtime and CLI retain ADC; regular local CLI use requires refreshing that ADC.

The importer validates route/current revision before embedding, prepares the whole
corpus, then atomically installs immutable content, vectors, provenance, reason,
actor, and the current pointer. Failed preparation leaves the old complete corpus
intact. Replay of the exact current import is idempotent. A superseded replay
cannot make older content current. Restore uses a new UUID and expected-current
revision, preserving history.

## Deployment sequence

Database preparation and code commits do not deploy the application. Complete
these steps in order through the existing release process:

1. Verify imported hashes and section counts for all eight active routes.
2. Deploy the reviewed Product image. Configure only `acuity-portal-api` with
   `KNOWLEDGE_GOOGLE_PROJECT=acuity-health-prod` and
   `KNOWLEDGE_GOOGLE_LOCATION=us-east1`. The release script preserves existing
   values when these variables are not explicitly supplied.
3. Verify authenticated deployed search with both real Practice service identities
   and every active office. A successful SQL diagnostic is not this proof.
4. Deploy the reviewed Agent with `ACUITY_PRODUCT_KNOWLEDGE_URL` pointing to the
   Product `/v1/agent/knowledge/search` endpoint and both existing Practice service
   credentials. There is no pilot flag or file fallback. Production startup
   requires the URL.
5. Run representative voice calls and measure median/p95 retrieval and complete
   spoken-answer latency. The local text-session timings below are diagnostic
   samples, not agreed production SLOs.

A deliberate rollback selects a reviewed prior application revision. Disabling
Product embeddings while running this Agent produces explicit temporary failures;
it does not restore repository knowledge. Keep additive tables and immutable
history intact during rollback.

## Verification and limitations

The all-office change adds only a Product HTTP integration test; no new schema,
API, provider, or authorization mechanism was required. The complete
`TEST_DATABASE_URL=.../acu55_test go test -p 1 ./backend/... ./deploy -count=1`
suite passed. The test uses the real two-token authenticator and deliberately
identical vectors to prove Practice, route, and revision isolation, including
shared Locations, cross-Practice office-key collisions, unimported aliases,
failed replacement/search, and superseded replay.

Agent commit `f66010e34a8f50605c8f72ef73b1062c95e0da6b` passes format, lint,
typecheck, build, and all 941 tests in 52 files. Its source/evaluation evidence
is in `docs/knowledge/README.md`. Its legacy resolver,
keyword hook/schema/cache tests and CPU benchmark are removed; the active route,
AgentSession, context-consistency, action, privacy, and cancellation tests remain.

Real Google calibration over 123 sections includes 56 factual cases and 16
separately recorded absent/irrelevant cases. At the unchanged `.52` cutoff and
four passages, 54/56 factual cases retrieve all required sections. The two original
compound questions containing “supplied” miss address information in North Miami
Beach and Rheumatology. All eight direct address queries and all eight natural
address-plus-phone queries pass. These misses remain visible in
`docs/knowledge/semantic-evaluation.json`; absent cases are not counted as successful
retrieval or proof of answerability. Ranking uses unrounded scores and rounds only
diagnostic output; its boundary regression runs in the normal Agent test suite.

Actual local Product HTTP + PostgreSQL/pgvector + real Google + Gemma AgentSession
checks passed 16/16 hours/address-contact scenarios across all eight offices.
Every scenario invoked search and consumed its own route's revision. Missing
phone information and New Tampa's unsupplied hours produced honest limitations.
In this small sample, retrieval median was 315.5 ms and nearest-rank p95 972 ms;
complete text response median was 1,929.5 ms and p95 2,718 ms. No audio was tested.
See `all-offices-model-address-contact.jsonl` and `all-offices-model-hours.jsonl` in
Agent `docs/knowledge/`.

Structured logs retain bounded failure causes and timing, never raw queries or
provider bodies. Scoped redaction covers native logs, Google/LiveKit tool spans,
model-message copies, and native/Product reports. Similarity is ranking only;
answers must be supported by current returned passages. Corpus readiness,
authenticated deployed retrieval, and spoken-answer quality are separate evidence.


## Completed all-office migration (2026-09-09)

The seven new corpora were imported from Agent commit
`f66010e34a8f50605c8f72ef73b1062c95e0da6b` after independent source review, full
local checks, and real model evaluation. Spring Hill's existing revision and
hash were retained. Read-only Cloud SQL verification found all eight corpora,
123 sections, matching committed content hashes, the pinned model/dimensions,
and attribution to a bound Platform Operator. Portal still cannot replace corpus
pointers. The complete identifiers and hashes are recorded in
[the database readback](../evidence/office-knowledge-all-offices.json).

| Office | Sections | Current revision |
| --- | ---: | --- |
| `crystal-river` | 15 | `c4215887-2f13-4e13-a11b-ed74fa1f089c` |
| `hollywood` | 15 | `458e422b-e48d-4a62-af87-0d4ed741f341` |
| `new-tampa-demo` | 16 | `49dd842c-6bd3-4d3a-aa75-764326b6b35e` |
| `north-miami-beach-optical` | 15 | `b5a08439-0582-414a-a0c6-e5f265c5cd45` |
| `ophthalmology-demo` | 16 | `f584923d-8945-4c3c-a93c-cf4d6e1175b1` |
| `rheumatology-demo` | 16 | `f9d5faef-508f-4703-a9c8-44b352812064` |
| `spring-hill` | 15 | `2ceface2-6675-40a8-ac8c-86d22f57c610` |
| `sweetwater` | 15 | `26704947-e348-4a03-a540-2a9ff67394b9` |

Product's all-office authorization test is committed as `475fb33`. Both worktrees
remain on `codex/office-knowledge-search`; neither repository was pushed or
deployed. The Agent branch also awaits an unrelated upstream CI-only commit.
Production retrieval observations remain **zero**. The database migration is
complete; deployed authenticated retrieval and voice acceptance remain the
separate release steps above.

All actionable code-review findings were accepted and resolved, including
Product-compatible hash encoding, raw-score evaluation before display rounding,
normal test-runner integration for that regression, explicit speech-guard tool
classification, and interruption/cancellation policy for the read-only tool.
No review finding was rejected.
