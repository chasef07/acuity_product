# Office knowledge

Each office's reusable, non-patient knowledge lives in PostgreSQL/pgvector as
immutable revisions. The office's active revision is the only source: Abita
searches it on every lookup, and staff edit it in the Portal. There are no
knowledge files in this repository.

Each entry has a stable ID, title, and complete text. Keep one coherent topic
per entry, including all qualifications and restrictions required to interpret
it correctly. Do not split by arbitrary character counts or duplicate the same
fact across entries. Never put patient information, transcripts, secrets, or
operator identities in entries. The Practice and Abita Office Route identify the
authorized retrieval scope.

Write the useful fact directly. Do not include `Status: available` labels,
document-export boilerplate, or instructions explaining when to read the entry.
Keep actual restrictions and explicit missing information in ordinary sentences.
The Abita tool renders selected facts as plain text. Revision IDs and source IDs
remain in the API/audit records, rather than the model's tool response. No extra
AI call rewrites the facts.

## Abita office standard

The Abita Eye Group offices use one entry standard, enforced on every publish,
from the Portal or the setup command, by
[`backend/internal/knowledge/publish.go`](../backend/internal/knowledge/publish.go).
Every office keeps the same required entry IDs (address, phone, fax, paperwork
email, directions, other offices, hours, holiday closures, after-hours,
providers, provider languages, services, appointment confirmation, new-patient
visit, what to bring, referrals, billing, payments, self-pay pricing, medical
records, prescription copies, and social follow-up). Staff may add other topics.

- When an office has no answer, the entry says so ("… is not supplied") so the
  agent does not borrow another office's fact or guess.
- Keep each entry under 800 characters and use plain sentences, not markdown.
- Title entries with the words callers use. Title words that appear in the
  question weigh most in ranking, so prefer "Broken glasses repair" over "Repairs".
- `other-offices` lists the practice's other offices, followed by any
  office-specific note.
- Remove holiday closures once they pass.

## Editing and publishing

Staff publish a complete, ordered entry list with a reason through
`POST /v1/knowledge/revisions`. Each publish creates a new revision recording
who changed it and why, and switches the office to it only if no one else
published first. The next Abita search uses it. Earlier revisions are never
changed.

Each revision has a version number, its position in the office's history
starting at 1. The Portal's History panel lists every version with its author,
reason, and when the agent last searched it, and shows what each changed from
the one before. Restoring publishes an earlier version's entries as a new
version with a reason; it never edits history.

## Search regression fixtures

[`backend/cmd/knowledge-import/testdata/`](../backend/cmd/knowledge-import/testdata/)
holds a frozen copy of the office entries and their retrieval cases. CI uses
them only to test search code. They are never published and do not track
production; editing them changes no office's knowledge.

## Operator commands

Set `KNOWLEDGE_IMPORT_DATABASE_URL` to an authorized database connection.

Export a revision as YAML, the active one by default, to read or diff it:

```sh
go run ./backend/cmd/knowledge-import --export \
  --practice PRACTICE_UUID --office OFFICE_KEY [--revision REVISION_UUID] > /tmp/office.yaml
```

YAML goes to stdout in saved entry order, and the exported revision ID goes to
stderr. Revisions published before entry order was saved export in entry ID
order.

Set up a new office that has no knowledge yet. Commit its YAML (same format as
the export) on a branch so the source is reviewable, then also set
`KNOWLEDGE_OPERATOR_EMAIL`, `KNOWLEDGE_GOOGLE_PROJECT`, and optionally
`KNOWLEDGE_GOOGLE_LOCATION` (defaults to `us-east1`), then:

```sh
go run ./backend/cmd/knowledge-import \
  --source office.yaml --commit FULL_REVIEWED_GIT_SHA --apply
```

The CLI refuses an office that already has an active revision; change that
office in the Portal. If a setup run fails after publishing, `--export` shows
whether it landed before you retry. It rejects a source that differs from its bytes at the
declared Git commit and resolves the supplied email to an existing bound
Platform Operator, so a source cannot forge attribution. Without `--apply` it
validates the source structure only. Legacy reviewed JSON imports remain
supported by `--file`, under the same refusal.
