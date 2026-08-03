# Slice 6 implementation note

Issue [#19](https://github.com/chasef07/acuity_product/issues/19) is the
controlling contract. This note records the implemented request paths, owners,
failure rules, deterministic proof, and the separate live Telnyx gate.

## Request paths and owners

PostgreSQL remains authoritative. `Access` owns current Practice and Location
authorization. `HumanCalling` owns Calls, softphone leases, provider commands
and facts, voicemail source state, retry links, and dispositions. `Work` owns
Tasks. `Messaging` owns SMS and Message delivery state.

Inbound recovery follows one authenticated AI handoff:

1. The existing single-use handoff admits one inbound Call and offers it for 20
   seconds.
2. Offer expiry commits one Location greeting command. An unusable or absent
   Location greeting resolves to the required safe HTTPS fallback; control
   never returns to the AI receptionist.
3. Provider-confirmed greeting completion commits one inbound-only, single
   channel MP3 recording command with a 120-second cap and no transcription.
   An accepted command arms a 30-second callback grace after that cap, so a
   missing saved-or-error callback still becomes one missed-call recovery.
4. A provider-confirmed positive-duration artifact atomically creates one OPEN
   `Review voicemail` Task and a Ready voicemail source identified by the
   durable Telnyx recording ID. A recording error,
   a non-positive artifact, or a caller hangup before recording begins creates
   one OPEN `Return missed call` Task and no audio placeholder. Once recording
   has begun, hangup waits for the delayed saved-or-error callback so receipt
   ordering cannot misclassify a voicemail.
5. No voicemail copy is scheduled. Callback download URLs are irrelevant to
   durable state and may expire without affecting playback.
6. Portal playback first issues a five-minute capability, then rechecks current
   Practice and Location access before calling `GET /v2/recordings/{id}` with
   the server-side Telnyx key. It follows the fresh provider download URL and
   streams complete or ranged audio without exposing the key or raw URL.

Outbound calling uses one state machine for both entry points:

1. Task starts resolve immutable Location and destination from the open Task.
   Standalone starts require an explicit currently authorized Location and a
   supported US `+1` destination; phone equality never attaches a Task.
2. The server resolves exactly one enabled Location voice number, verifies the
   current softphone lease, readiness, and SIP credential, then commits a
   Preparing Call and durable staff-media Dial before provider contact.
3. Provider answer alone does not dial the destination. The owning browser must
   attach the exact current TelnyxRTC invite, start remote audio playback, and
   explicitly confirm its HMAC-bound media token and Telnyx session. The shared
   session correlates the WebRTC/SIP leg with the distinct Call Control leg.
   The confirmation is idempotent only while the same browser lease, claim,
   provider session, answer, and recorded readiness remain authoritative. Only
   that confirmation commits the destination Dial. It uses the
   server-derived caller ID, disables answering-machine detection, bridges only
   on answer, and caps ringing at 30 seconds.
4. Only provider bridge facts mark the Call Connected. New connected human
   Calls issue no recording command. A browser reload or lease takeover
   reattaches to the durable Call and cannot replay the destination Dial.
   Durable media readiness distinguishes a pre-confirmation reconciliation
   from a post-dial unknown state.
5. A browser or transport timeout never invents a terminal result. Ring expiry
   becomes Status unknown with durable hangup intents. Provider facts establish
   No answer, Busy, Declined, Failed, or the connected outcome.
6. Retry creates one new linked attempt only from a safe terminal state or an
   explicitly visible unknown state. It never mutates or automatically redials
   the prior attempt.
7. Connected Task Calls require Complete task or Keep open. Unsuccessful Task
   Calls leave the existing Task open. Standalone Calls require the
   context-specific Resolved, No follow-up, or Create task disposition; the
   Create task path is idempotent.

After a provider-confirmed bridge, the initiating User's workspace shows
Contact Context and one chronological exact-phone Engagement History composed
from authorized Messages, Calls, and compact Tasks. Every item retains its
source Location. Its cursor is the immutable event time plus a type-qualified
record ID, and older pages preserve the same ordering. This is a phone match,
not a verified Patient identity. The composer remains outside the scrolling
timeline and sends only through the active Call's fixed Location and
destination.

## Invariants and recovery

- One Call and one softphone lease own media control for a User. Starting
  outbound calling suppresses inbound availability until terminal cleanup, then
  restores the User's prior availability intent.
- Durable provider commands are claimed once, survive worker restart, and use
  stable command identity. Ambiguous destination writes enter reconciliation
  and never auto-redial.
- Duplicate and reordered provider facts cannot create a second Call, Task,
  destination Dial, disposition Task, or voicemail lifecycle row.
- Voicemail playback and recording facts must match the Call's exact provider
  control ID, leg ID, and session ID. Outbound idempotency keys are serialized
  per initiating User before the first lookup, including simultaneous starts.
- Voicemail playback always refreshes through the durable Telnyx recording ID.
  Provider not-found, authentication, rate-limit, timeout, invalid-response,
  expired-download-URL, and 5xx outcomes remain bounded and observable.
- DTMF is a transient browser-media action. It is accepted only on the current
  connected owned TelnyxRTC leg and is never sent to the server, persisted,
  queued, retried, logged, or emitted as a domain event.
- Contact names, message bodies, full phone numbers, provider audio URLs, and
  DTMF digits stay out of application logs and operational evidence.
- Authorization is rechecked for every Call, Task, history, messaging, and
  playback request. Completing a Task does not remove authorized Call or
  voicemail visibility; revoking Location access does.

## Deterministic proof

The focused PostgreSQL suite proves one-Task voicemail and missed-call outcomes,
safe greeting fallback, the two-minute recording boundary, reordered
saved-versus-error fact handling, silent-callback and definitive command-failure
recovery, exact provider-leg correlation, duplicate fact idempotency,
fresh provider playback, cross-tenant denial before provider contact, and Range responses,
server-derived Task outbound routing, cross-leg Telnyx session correlation,
browser-media-before-destination ordering,
simultaneous outbound idempotency, provider-confirmed connection, absence of
connected recording, atomic Task disposition, interrupted-command recovery,
ring-timeout reconciliation, and provider-backed No answer, Busy, Declined, and
Failed normalization.

Adapter and browser tests prove Telnyx command normalization and direct,
current-leg-only DTMF. The real PostgreSQL browser harness additionally crosses
authenticated handoff HTTP, signed provider ingress, Telnyx-owned recording
identity, authorized backend-proxied playback, explicit staff-media confirmation,
second-tab visibility, reload recovery, and a no-server-write DTMF assertion.
For outbound connection latency, that harness holds workspace, Task, and
Message reconciliation open and requires the signed bridge receipt to produce
an authoritative Connected Call response within 750 milliseconds. This target
beats the browser's one-second recovery poll without treating the realtime
version as Call state. The same receipt records must show less than 500
milliseconds from durable receipt commit to the worker's processing attempt.

The receipt worker starts each drain immediately, processes up to eight durable
receipts, and otherwise waits at most 250 milliseconds. Existing bounded
`acuity_call_center_receipt_processing` queue and processing observations are
the production measurement seam. The deterministic fast path meets its target
with that fallback interval, so this slice does not add a PostgreSQL
notification connection. Production percentiles remain part of the controlled
live gate.

Generated OpenAPI clients, Go tests and vet, web unit tests, lint, typecheck,
production build, and deployment-contract tests complete the deterministic
release gate.

Deterministic fixtures prove product behavior, not live Telnyx delivery, real
audio quality, provider retention, or browser-device acceptance.

## Controlled live gate

Issue #19 remains not live-accepted until an explicitly approved run with
non-sensitive test numbers records PHI-safe evidence for:

- one authenticated handoff answered by Staff;
- one no-answer voicemail saved by Telnyx and replayed through Acuity's
  authenticated streaming route;
- one missed-call recovery with no recording;
- one Task-originated connected Call and disposition;
- one standalone unsuccessful Call and disposition;
- keypad navigation against a controlled IVR;
- browser reload or explicit takeover without a second destination Dial; and
- provider receipts reconciling to exactly one Call attempt and the expected
  Task state.

Record only Call and Task identifiers, timestamps, opaque provider receipt
identifiers, command counts, state transitions, and observed UI outcomes.
Deterministic success must not be described as live provider proof.
