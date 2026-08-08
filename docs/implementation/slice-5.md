# Slice 5 implementation note

Issue [#14](https://github.com/chasef07/acuity_product/issues/14) is the
controlling product contract. This slice adds Location-scoped SMS/MMS
conversations and provider-backed delivery state without expanding the settled
Slice 3 Task lifecycle.

## Request path and ownership

1. The authenticated portal accepts a Message query, durable send command,
   attachment upload, read marker, explicit send-again attempt, or explicit
   Message-to-Task action.
2. `Access` resolves the actor's Practice and Location authority. Platform
   Operators mutate directly under their real identity; the mutation and audit
   entry share one PostgreSQL transaction.
3. `Messaging` owns the exact conversation key: Practice, Location, configured
   office sender, and normalized external phone. The browser never chooses the
   sender or messaging profile.
4. A send transaction creates one immutable `Sending` Message and one provider
   command before acknowledging the browser. The worker performs the Telnyx
   write; signed raw webhooks are durably receipted before projection.
5. PostgreSQL remains authoritative. SSE carries only a disposable change hint,
   and browsers refetch the thread list, selected timeline, and linked Task.

## Invariants

- Messages and content are immutable. `Send again` creates a separately
  identifiable attempt linked to the original, including a fresh private
  attachment record for MMS.
- Visible delivery state is exactly `Sending`, `Sent`, `Delivered`, `Failed`,
  or `Status unknown`.
- A provider timeout or interrupted write is never interpreted as failure and
  never causes a blind retry. Read-only reconciliation runs only when a
  provider Message identity is known.
- A process loss while projecting a receipt leaves the raw evidence durable.
  The worker reclaims stale `PROCESSING` receipts and repeats the idempotent
  projection.
- Signed inbound SMS/MMS creates or appends to the exact conversation. It does
  not create, reopen, or complete a Task.
- A human may explicitly create at most one `OPEN` follow-up Task from one
  Message. The link is durable in both directions.
- The conversation timeline unions Messages with Calls matching the same
  Practice, Location, and exact phone, plus explicitly linked or exact-phone
  Tasks. Phone history is context, not verified patient identity.
- `STOP` blocks future writes at the conversation boundary. Pending commands
  fail before a provider write; already-writing commands remain
  evidence-driven. `START` restores outbound eligibility.
- Unread state is per user. Opening the conversation clears that user's marker
  and its projection on linked `OPEN` Tasks only.

## Attachments

One outbound JPEG, PNG, GIF, WebP, or PDF up to 600 KB is accepted after
server-side byte detection. The concrete runtime adapter stores bytes in a
private directory with restrictive permissions; all portal, worker, and ingress
roles must mount the same persistent private directory. Telnyx receives only a
short-lived signed media URL. Inbound media is copied asynchronously, is shown
as unavailable on failure, and can be retried in place without changing the
Message.

The storage interface is intentionally replaceable by a managed object-store
adapter. The deployment contract mounts one private Cloud Storage bucket into
the portal, provider-ingress, and worker runtimes at the same absolute path.
Production release remains gated on least-privilege bucket IAM, retention and
backup policy, and a cross-runtime read/write smoke test.

## Browser behavior

The existing rail switches between Tasks and Messages. Message search is
phone-only and requires one selected Location. A phone-led correspondence
ledger renders Message bubbles together with narrow Call and Task rule cards.
The composer shows one optional attachment, a 1,600-character counter near the
limit, the locked office route, visible send state, and no aggregate unread
count, sound, or notification.

A linked `OPEN` Task embeds the same compact conversation and composer. A
completed Task keeps its history visible but disables sending until a human
reopens it. If new activity arrives while the operator is reading older
history, the timeline preserves the scroll position and exposes a `New message`
control.

## Deterministic proof and live gate

PostgreSQL integration tests cover Location isolation, strict phone rejection
without partial state, durable idempotent send, provider acceptance and
delivery evidence, unknown outcomes without blind retry, read-only
reconciliation, text and MMS attempts, interrupted receipt recovery, signed
inbound receipt, STOP/START races, unread projection, explicit Task creation,
operator audit atomicity, exact-phone Call/Task chronology, cursor
pagination, and attachment lifecycle.

The generated authenticated HTTP test exercises send, worker projection,
delivery, inbound receipt, conversation query, explicit Task creation, and
private attachment retrieval. Playwright drives the real built portal, Go
roles, PostgreSQL, signed webhook fixture, the visible
`Sending` → `Sent` → `Delivered` transition, per-user unread clearing, inbound
Message, explicit Task creation, a Task-linked reply, completed-Task composer
gating without closing the Message Thread, attachment preview, and STOP/START
behavior.

These deterministic adapters prove Acuity's request path and invariants. A
controlled live Telnyx run is still required to prove production credential,
sender/profile, carrier delivery, MMS media fetch, and webhook configuration.
