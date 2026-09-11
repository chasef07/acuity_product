# Spring Hill retrieval evidence

On September 11, 2026, the existing production knowledge API was queried with
synthetic questions. The proposed source corpus was embedded with the configured
Vertex AI `text-multilingual-embedding-002` model, using `RETRIEVAL_DOCUMENT` for
entries and `RETRIEVAL_QUERY` for questions. Cached provider vectors were then
queried through the proposed SQL and selection code in disposable local pgvector.
This compares live baseline responses with local retrieval using real provider
embeddings; it does not establish deployed application behavior or call outcomes.

## Same-query comparison

Characters below count returned evidence text, excluding titles and JSON.

| Query | Original entries | Proposed entries | Original characters | Proposed characters |
| --- | ---: | ---: | ---: | ---: |
| Is the Medical Drive office open? | 4 | 1 | 498 | 365 |
| Can I move my appointment to Medical Dr? | 4 | 1 | 465 | 365 |
| What is your current address? | 4 | 1 | 1000 | 84 |
| Is Medical Drive closed, and where is your current office? | 4 | 2 | 928 | 449 |
| What are your hours? | 4 | 1 | 599 | 58 |
| Who sees cataract patients? | 4 | 1 | 1869 | 154 |
| Can I park my spaceship on the moon? | 0 | 0 | 0 | 0 |

The original corpus did not contain the Medical Drive closure. The proposed
results include that closure and its booking restriction. The address response
excludes unrelated billing, social follow-up, and office hours. Both parts of the
closure/address and optician/adjustments questions remain present.

## Review cases and acceptance boundary

All 18 English acceptance queries returned their intended entries without filler.
Three additional review cases preserve required evidence using conservative
bounded retrieval, giving 21 passing cases in `evals/spring-hill.json`:

- Spanish Medical Drive closure plus current address: both required entries,
  plus two other entries. The original live API missed the closure and address.
- Spanish office address plus hours: both required entries, plus fax and optician
  hours. The original live API included the address and hours. Clarifying the
  source title to “Current office address and directions” prevented a regression
  introduced by the first focused-content draft.
- Six-year-old eye exam: the under-seven restriction is included among four
  entries. The original live API also included this restriction.

One existing failure remains explicitly recorded in `evals/known-gaps.json`:
“Can I get my glasses prescription renewed for my toddler?” omits the under-seven
restriction in both the original live API and the proposed retrieval. This is a
known failure, not a passing test or a resolved patient workflow. Implicit-age
interpretation needs further retrieval and agent/scheduling validation.

## Selection limits

Retrieval remains Practice/office/revision scoped and keeps the existing semantic
similarity floor. Exact multiword titles can rescue named entries. Lexical and
semantic ranks produce candidates; distinctive query-term coverage selects
focused evidence. Common office vocabulary does not add entries merely to fill
four result slots. A new entry must cover an uncovered title token or at least
two uncovered text terms. Normalized lexical score breaks equal-coverage ties.

When fewer than half of query lexemes occur anywhere in the office corpus, or
no usable lexical evidence is present, bounded candidate retrieval is preserved.
This protects unfamiliar wording and multilingual questions from an unsupported
claim that English lexical coverage answers every part. Such results can still
contain extra information. Returned entries remain whole under a 3,000-character
text/title budget; an oversized legacy entry is returned alone without truncating
its restrictions. The budget is not a guarantee of relevance or completeness.

These heuristics were evaluated against this bounded set, not a broad recall or
multilingual benchmark. Source edits should continue to expand the fixtures,
including difficult paraphrases and exceptions. Publishing still requires live
verification against the active database revision.
