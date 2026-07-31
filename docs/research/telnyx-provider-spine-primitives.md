# Telnyx provider spine primitives

Status: research note, not a product specification  
Research date: 2026-07-27  
Scope: the smallest production-grade Telnyx spine for Acuity Portal Slice 2  
Runtime shape: the existing Go modular monolith, PostgreSQL as source of truth, Next.js staff browser

## Recommendation

Use Telnyx Call Control as a narrow provider adapter, not as the owner of the
product workflow.

The clean Slice 2 path is:

1. The AI call requests an authenticated, idempotent, one-time handoff route
   from the portal.
2. LiveKit transfers the caller by SIP to one non-production Telnyx Call
   Control Application.
3. Telnyx sends the inbound `call.initiated` webhook. The ingress verifies and
   durably receipts the raw event before acknowledging it.
4. `HumanCalling` correlates the one-time handoff token. A worker issues an
   idempotent Answer command because Telnyx requires an inbound call to be
   answered before subsequent commands.
5. `call.answered` proves the caller leg is established. `HumanCalling`
   publishes the 20-second offer and atomically chooses the first eligible
   staff acceptance in PostgreSQL.
6. After that database commit, a worker issues one Telnyx Dial command to the
   winning staff member's WebRTC SIP URI. The command links the new staff leg
   to the caller leg and asks Telnyx to bridge it automatically on answer.
7. The staff browser, already connected through one authenticated `TelnyxRTC`
   client, receives the incoming call and answers it.
8. `call.bridged` provider events—not a browser animation or API response—prove
   that the caller and staff member are connected.
9. Recording starts only after bridge evidence. `call.hangup` moves the product
   call to disposition-required state.

This keeps one owner for each concern:

| Concern | Owner |
|---|---|
| Offer, eligibility, winner, logical call, disposition | `HumanCalling` + PostgreSQL |
| Provider commands and provider identifiers | Telnyx adapter |
| Provider event authenticity and durable receipt | `provider-ingress` |
| Call projection and recovery | Worker |
| Browser microphone, speaker, and WebRTC call object | One browser media owner |
| Durable recording and transcript evidence | Worker + protected object storage |

Telnyx supplies call legs, WebRTC signaling/media, bridging, recordings, and
signed events. It must not become the source of truth for staffing, offers,
winner selection, or workflow state.

## Minimal Telnyx primitive set

### 1. One Call Control Application

Create an isolated non-production Call Control Application with:

- API v2 webhooks;
- a public HTTPS primary webhook URL;
- a failover webhook URL if it can fail independently;
- an inbound SIP subdomain for the LiveKit transfer;
- an Outbound Voice Profile for the winning staff leg;
- known concurrent-call and channel limits;
- DTMF redaction enabled where appropriate.

Call Control is a better fit than TeXML because Slice 2 is command/event driven:
the application already owns the workflow and needs explicit Dial, recording,
hangup, and recovery behavior.

### 2. Answer the authenticated inbound caller

Telnyx requires an inbound call to be answered before subsequent commands run.
After the one-time SIP handoff token is correlated and authorized, persist an
Answer command with:

- the inbound `call_control_id`;
- a stable `command_id`;
- the small, versioned `client_state`;
- no record-from-answer setting.

`call.answered` is the provider evidence that the caller leg is established.
Only then should the Call enter the 20-second staff-offer state.

The caller's waiting experience during that window must be decided before the
specification. A short, fixed Telnyx Playback is the simplest acceptable
waiting-message primitive if silence after the AI's “please hold” message is
not acceptable. It does not require media streaming. Playback is a UX
primitive, not a workflow owner, and must stop before or on bridge.

### 3. A linked Dial to the winning WebRTC user

After PostgreSQL has committed the winner, Dial exactly one SIP destination:

```text
sip:<staff-telephony-credential-sip-username>@sip.telnyx.com
```

The minimal Dial request should use:

- `link_to`: the parked caller's `call_control_id`;
- `bridge_intent: true`;
- `bridge_on_answer: true`;
- `prevent_double_bridge: true`;
- a bounded `timeout_secs`;
- a stable, persisted UUID `command_id`;
- a small, versioned, base64-encoded `client_state`.

`link_to` puts the related legs in the same call session.
`bridge_on_answer` removes an avoidable application-side Bridge race.
`prevent_double_bridge` is a provider-side guard if the target has already
been bridged.

An explicit Bridge command should remain a recovery/fallback seam until the
linked Dial behavior is proven in the target Telnyx account. It should not be
the default happy path.

Do not Dial several staff destinations and ask Telnyx to decide the winner.
The application offer and PostgreSQL transaction are the durable arbitration
boundary; Telnyx should see only the winning Dial.

### 4. One Telephony Credential per staff user

Each enabled staff user receives a distinct Telnyx Telephony Credential.
The backend creates short-lived WebRTC JWTs through:

```text
POST /telephony_credentials/{id}/token
```

The browser receives only the JWT. It never receives the Telnyx API key or the
credential password. Telnyx documents a JWT lifetime of up to 24 hours, bounded
by the parent credential's expiry.

The product must store the Telnyx credential identifier and SIP username on the
user's telephony identity record. Creation, disabling, and deletion should
follow the user's lifecycle and be auditable.

### 5. One `TelnyxRTC` media owner

The browser uses the Telnyx JavaScript WebRTC SDK and a token login. The exact
surface needed by Slice 2 is small:

- create `TelnyxRTC({ login_token })`;
- subscribe to `telnyx.ready`, `telnyx.notification`, and error/socket state;
- receive the incoming call object through a notification;
- `call.answer()`;
- `call.hangup()`;
- `call.hold()` / `call.unhold()` if hold is in the slice;
- `call.dtmf()`;
- `call.muteAudio()` / `call.unmuteAudio()`;
- select input/output devices;
- observe call state, including `ringing`, `active`, `held`,
  `reconnecting`, and `destroyed`.

Pin and review an exact SDK version. On 2026-07-27, the inspected upstream
package is version `2.27.8`; method names above were verified in that source,
not inferred from older prose examples.

There should be one active media-owning client per staff user. A simple
PostgreSQL browser lease can designate one active tab; other tabs remain
view-only. This is a product decision that should be confirmed in the grill.

The browser call object is not durable truth. Provider webhooks and the logical
Call row remain authoritative across reloads, reconnects, and tab loss.

### 6. Signed webhook ingress and a raw event receipt

Telnyx explicitly says webhooks may be duplicated, delivered concurrently, and
arrive out of order. It expects a successful response within two seconds and
retries failures.

The ingress path should:

1. Bound the request body size and read the raw body once.
2. Verify `telnyx-signature-ed25519` and `telnyx-timestamp` against those exact
   raw bytes using the Call Control Application public key.
3. In a short database transaction, insert a raw provider-event receipt and
   enqueue/project pending work.
4. Treat a uniqueness conflict on `(provider, provider_event_id)` as an
   already-accepted duplicate.
5. Return `2xx` only after the receipt transaction commits.
6. Return a failure if the durable receipt cannot be committed.

Do not parse or normalize the body before signature verification. Do not make
provider calls, project the product call, download media, or publish SSE inside
the acknowledgment path.

The current Go SDK's `client.Webhooks.Verify(raw, headers)` verifies the raw
payload with Ed25519 and enforces a five-minute timestamp tolerance. Prefer it
to `UnsafeUnwrap`. Prefer `Verify` over `Unwrap` at ingress so a new or unknown
event schema can still be stored durably and examined instead of being lost
because a typed parser is stale.

The five-minute replay check makes synchronized server clocks an operational
dependency.

### 7. Recording after provider-confirmed bridge

Issue `record_start` only after bridge evidence if the invariant is “record
the human conversation, not the preceding AI segment.”

The minimal request should use:

- dual-channel recording;
- the selected recording format;
- a stable `command_id`;
- transcription only if consent and retention policy permit it.

Handle:

- `call.recording.saved`;
- `call.recording.transcription.saved`;
- `call.recording.transcription.error`.

Telnyx says its recording download URL in the webhook remains active for ten
minutes. A webhook row containing that URL is therefore not durable recording
evidence. The worker must promptly copy the asset to protected storage, or the
Call Control Application must be configured for the approved GCS/AWS storage
path. Persist the provider recording and transcription IDs for retrieval and
diagnosis.

## Event and command discipline

### Provider identifiers to preserve

Persist these fields without overloading their meanings:

| Identifier | Use |
|---|---|
| `data.id` | Provider event id and durable deduplication key |
| `occurred_at` | Provider event time; ordering evidence, not arrival order |
| `meta.attempt` | Delivery diagnostic |
| `call_control_id` | Target for commands on a live provider call leg |
| `call_leg_id` | Stable provider leg correlation |
| `call_session_id` | Correlation across related legs in the same session |
| `connection_id` | Call Control Application/connection diagnostic |
| `client_state` | Small opaque application correlation echoed in events |
| `command_id` | Application-generated provider-command idempotency key |
| `recording_id` | Provider recording retrieval/correlation |
| `recording_transcription_id` | Provider transcription correlation |

The browser SDK's `call.telnyxIDs` and `recoveredCallId` are useful diagnostic
signals, but they should not create or authorize product state.

Keep `client_state` deliberately small: a version plus opaque internal
correlation IDs. Do not put patient names, phone numbers, transcript text, or
authorization decisions in it.

### Idempotency

Before a provider command:

1. Commit the product transition and an outbox/provider-command row.
2. Generate and persist one UUID `command_id`.
3. Send the command outside the product transaction.
4. Retry the same logical command with the same `command_id`.
5. Reconcile provider events and provider call state before deciding that an
   ambiguous command requires a new logical attempt.

Telnyx documents duplicate command suppression for 60 seconds. That is a useful
provider guard, not durable application idempotency. The database must preserve
the command identity beyond that window.

Telnyx recommends retrying the same command ID after a `5xx` or unusually slow
response. A successful HTTP command response means Telnyx accepted the command;
the corresponding provider event proves the resulting call transition.

### Ordering and projection

Workers should be idempotent under replay and tolerant of event reorder.
Per-call-session serialization is a reasonable simple implementation, but it
does not remove the need for transition guards.

Useful invariants include:

- a terminal logical state cannot regress because an older event arrives late;
- a winner is selected once;
- one accepted offer creates at most one provider Dial command;
- browser readiness does not imply a bridged call;
- a Dial API response does not imply a bridged call;
- bridge is proven by the expected `call.bridged` events;
- recording readiness is proven by durable storage capture, not by a temporary
  provider URL;
- duplicates are acknowledged without repeating product side effects.

Use `occurred_at` and the event type as evidence, not a universal total order.
Where webhook evidence is missing or contradictory, query Telnyx's active-call
or call-events resources as a repair path and keep the state visibly
indeterminate until reconciled.

### Acknowledgment and recovery

The webhook delivery contract should be observable:

- signature result;
- receipt commit latency;
- event ID and type;
- attempt number;
- projection status and last error;
- age of the oldest unprojected receipt.

Logs should contain opaque identifiers, not raw webhook payloads, phone
numbers, recordings, or transcripts.

Telnyx's Webhook Deliveries and Call Events APIs are diagnostic/reconciliation
tools. They should not replace the local event receipt or normal webhook path.

## Exact browser media and control boundary

The browser owns:

- microphone permission and capture;
- speaker/audio element and output device choice;
- the in-memory `TelnyxRTC` client and incoming call object;
- answer, hangup, mute, DTMF, and optional hold controls;
- presenting connection/reconnection state.

The backend owns:

- whether the user is eligible and available;
- whether this tab holds the active softphone lease;
- offer creation and expiry;
- the atomic winning acceptance;
- JWT issuance after product authorization;
- Telnyx credential lifecycle;
- provider Dial commands;
- logical call state and recovery.

Answer should be enabled only for the exact accepted offer and expected
incoming call. If automatic browser answer is used, it must be gated by that
server-confirmed winner state and the expected call correlation. A random
incoming SDK call must never be auto-answered.

`call.answer()` may trigger `getUserMedia`; the browser therefore needs an
explicit permission and readiness experience before the user is made
available. Device choice and permission failure are first-class availability
states, not toast-only errors.

The SDK's WebSocket is signaling. WebRTC media follows the negotiated ICE/TURN
path. Production verification must include real network/firewall conditions,
not only localhost.

## What not to use in Slice 2

Do not introduce:

- TeXML;
- Telnyx queues as the staff-offer owner;
- Conference as the two-party bridge primitive;
- provider multi-destination dialing for offer fanout;
- SIPREC;
- media streaming/forking;
- Telnyx AI Assistants;
- Redis, Kafka, or a second durable state system;
- a custom general-purpose WebSocket channel;
- SMS;
- supervisor/barge/whisper primitives;
- advanced noise suppression as a correctness dependency;
- automated Telnyx trunk/account provisioning unless repeated environments
  make it necessary.

These primitives may become useful later, but none is needed to prove the
smallest reliable human-transfer slice.

## Smallest live proof

### Required non-production setup

- one isolated Call Control Application;
- one inbound SIP subdomain;
- one Outbound Voice Profile;
- one Credential Connection;
- two real invited staff users with distinct Telephony Credentials;
- backend JWT issuance;
- public HTTPS provider ingress;
- Telnyx API key, webhook public key, and credential identifiers in approved
  secret storage;
- two browser contexts, with real microphone/speaker permissions and preferably
  two headsets;
- one LiveKit AI call capable of SIP transfer;
- a real caller;
- approved protected recording storage;
- a Telnyx number only if the selected voicemail or PSTN test needs one.

### Happy-path proof

1. Start a real call with the AI agent.
2. Transfer it through the authenticated one-time SIP handoff route.
3. Observe a verified, durably receipted inbound Telnyx event.
4. Offer the Call to two eligible staff users for 20 seconds.
5. Accept nearly simultaneously from both browsers.
6. Prove exactly one PostgreSQL winner and exactly one Telnyx Dial command.
7. Prove the winning browser rings and the loser sees “claimed.”
8. Answer and prove `call.bridged` evidence for the two legs.
9. Prove clear two-way audio.
10. Prove recording begins only after the bridge.
11. Hang up and prove disposition-required state.
12. Prove the recording/transcript reaches protected storage while the
    temporary provider URL is still valid.

### Failure and recovery proof

- replay a duplicate event;
- deliver known events out of order;
- cause webhook receipt failure and observe Telnyx retry;
- reject a bad signature and stale timestamp;
- kill the API after the winner commits but before the Dial sends;
- interrupt after the Dial sends but before its response is recorded;
- disconnect and reconnect the winning browser network;
- reload or close the media-owning tab;
- let the offer expire and the winning leg time out/no-answer;
- receive hangup before bridge;
- exercise recording/transcription error;
- observe provider rate-limit or invalid-command responses;
- reconcile a missing webhook with Call Events/Webhook Deliveries.

The slice is not proven by a green health endpoint. It is proven by the
PostgreSQL winner, the provider event rows, Telnyx call events, real two-way
audio, and durable recording evidence.

## Decisions and unknowns for the grill

1. **One softphone tab:** Is one active media-owning tab per user an explicit
   product rule? Recommendation: yes, enforced by a short renewable lease.
2. **AI ingress:** Will Slice 2 reuse the authenticated, idempotent, one-time
   SIP route pattern already used by the current handoff? Recommendation: yes.
3. **SIP correlation:** Can the opaque token remain in the SIP user portion
   end-to-end through LiveKit and the target Telnyx application? This needs a
   live proof; do not depend solely on arbitrary custom SIP headers.
4. **Automatic bridge:** Does linked Dial with `bridge_on_answer` and
   `prevent_double_bridge` behave exactly as documented for a Call
   Control-to-Telnyx WebRTC credential call in the target account? Prove it
   before the specification locks the happy path.
5. **Recording policy:** What consent/announcement, jurisdiction, retention,
   access, deletion, BAA, and GCS requirements apply?
6. **Voicemail:** Is voicemail part of Slice 2, and is it recorded by Telnyx
   after the 20-second staff window, or deferred to the next slice?
7. **Browser recovery:** On reload, should a still-active provider call be
   reattached, or should another explicit recovery policy apply?
8. **Failover:** Can the failover URL actually fail independently of the
   primary service and database? If not, label it provider redelivery routing,
   not disaster recovery.
9. **Limits:** What are the account's outbound profile, concurrent-call,
   WebRTC, and rate limits?
10. **Retry ceiling:** The exact default retry duration for every inbound
    voice webhook is not stated consistently enough to make it a product
    guarantee. Recovery cannot depend on a guessed ceiling.

## `team-telnyx/ai` skills

The repository is useful as an implementation index, but its skills are
generated snapshots. Official API documentation and pinned runtime SDK source
remain authoritative.

When implementation begins, the smallest useful project-local skill set is:

- `telnyx-voice-go` — Call Control Dial, events, answer/hangup/bridge reference;
- `telnyx-webrtc-client-js` — browser client, call states, media, and controls;
- `telnyx-webrtc-go` — Telephony Credential lifecycle;
- `telnyx-voice-media-go` — recording and transcription commands/events.

Supplement `telnyx-webrtc-go` with the actual Go SDK token endpoint because the
generated skill does not currently expose `TelephonyCredentials.NewToken`.

Reference without adding to the core project skill set:

- `telnyx-cli` for non-production setup and live diagnosis;
- `telnyx-sip-go` only if environment provisioning is later automated.

Do not add the TeXML, Conference, Voice Streaming, SIPREC/advanced voice,
AI-assistant, SMS, or mobile SDK skills for Slice 2.

No skills were copied or installed during this research.

## Primary sources

### Telnyx documentation

- [Receiving webhooks and signature verification](https://developers.telnyx.com/development/api-fundamentals/webhooks/receiving-webhooks)
- [Programmable Voice webhook envelope and identifiers](https://developers.telnyx.com/docs/voice/programmable-voice/voice-api-webhooks)
- [Voice webhook order, duplicates, and command IDs](https://developers.telnyx.com/docs/voice/programmable-voice/receiving-webhooks)
- [Command retries](https://developers.telnyx.com/docs/voice/programmable-voice/command-retries)
- [Sending Call Control commands and expected events](https://developers.telnyx.com/docs/voice/programmable-voice/sending-commands)
- [Dial command](https://developers.telnyx.com/api-reference/call-commands/dial)
- [Bridge calls](https://developers.telnyx.com/api-reference/call-commands/bridge-calls)
- [Start recording](https://developers.telnyx.com/api-reference/call-commands/recording-start)
- [Storing call recordings](https://developers.telnyx.com/docs/voice/programmable-voice/storing-call-recordings)
- [Retrieve a recording](https://developers.telnyx.com/api-reference/call-recordings/retrieve-a-call-recording)
- [Retrieve a recording transcription](https://developers.telnyx.com/api-reference/call-recordings/retrieve-a-recording-transcription)
- [Call Control Application fundamentals](https://developers.telnyx.com/docs/voice/programmable-voice/voice-api-fundamentals)
- [Update a Call Control Application](https://developers.telnyx.com/api-reference/call-control-applications/update-a-call-control-application)
- [LiveKit SIP configuration](https://developers.telnyx.com/docs/voice/sip-trunking/livekit-configuration-guide)
- [SIP URI calling](https://developers.telnyx.com/docs/voice/sip-trunking/features/sip-uri-calling)
- [WebRTC JavaScript quickstart](https://developers.telnyx.com/development/webrtc/js-sdk/tutorials/make-your-first-call)
- [WebRTC JWT authentication](https://developers.telnyx.com/development/webrtc/auth/jwt)
- [WebRTC production practices](https://developers.telnyx.com/development/webrtc/js-sdk/how-to/production-best-practices/index)
- [`TelnyxRTC` reference](https://developers.telnyx.com/development/webrtc/js-sdk/reference/telnyxrtc/index)
- [WebRTC call-state lifecycle](https://developers.telnyx.com/development/webrtc/js-sdk/explanation/call-state-lifecycle)
- [WebRTC signaling and call control](https://developers.telnyx.com/development/webrtc/js-sdk/explanation/webrtc-signaling)
- [WebRTC browser demo and SIP username dialing](https://developers.telnyx.com/docs/voice/webrtc/js-sdk/demo-app)
- [ICE and TURN](https://developers.telnyx.com/development/webrtc/js-sdk/explanation/ice-and-turn)
- [WebRTC error handling](https://developers.telnyx.com/development/webrtc/js-sdk/how-to/error-handling/index)
- [List Call Events](https://developers.telnyx.com/api-reference/debugging/list-call-events)
- [List Webhook Deliveries](https://developers.telnyx.com/api-reference/webhooks/list-webhook-deliveries)
- [Call Control commands and resources](https://developers.telnyx.com/docs/voice/programmable-voice/voice-api-commands-and-resources)

### Pinned source inspected

- [`team-telnyx/ai` at `f3b0e85`](https://github.com/team-telnyx/ai/tree/f3b0e85a347c3a316b3f40bf29ede1316001913c/skills)
- [Go SDK webhook verification at `f4bd523`](https://github.com/team-telnyx/telnyx-go/blob/f4bd5238fbf45e1fd75c148726c2c50232c846a3/webhook_custom.go)
- [Go SDK Ed25519 and timestamp verification at `f4bd523`](https://github.com/team-telnyx/telnyx-go/blob/f4bd5238fbf45e1fd75c148726c2c50232c846a3/lib/webhook_verification.go)
- [Go SDK Telephony Credential token creation at `f4bd523`](https://github.com/team-telnyx/telnyx-go/blob/f4bd5238fbf45e1fd75c148726c2c50232c846a3/telephonycredential.go)
- [WebRTC JavaScript package version at `ee08736`](https://github.com/team-telnyx/webrtc/blob/ee087360723ce7e917968dcab41cc57fa3142f7a/packages/js/package.json)
- [WebRTC browser Call implementation at `ee08736`](https://github.com/team-telnyx/webrtc/blob/ee087360723ce7e917968dcab41cc57fa3142f7a/packages/js/src/Modules/Verto/webrtc/BaseCall.ts)
- [WebRTC JavaScript changelog at `ee08736`](https://github.com/team-telnyx/webrtc/blob/ee087360723ce7e917968dcab41cc57fa3142f7a/packages/js/CHANGELOG.md)
