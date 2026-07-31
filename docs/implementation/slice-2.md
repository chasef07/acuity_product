# Slice 2 implementation note

Issue [#4](https://github.com/chasef07/acuity_product/issues/4) is the
controlling contract. This note records the implemented request path, owners,
failure rules, deterministic proof, and the evidence still required from the
controlled live provider run.

## Request path and ownership

`HumanCalling` owns the logical handoff, Call, offer deadline, softphone lease,
readiness, claimant, provider intent, provider evidence, disposition, and
recording readiness. `Access` remains the sole authority for the current User,
Membership role, Practice, and Location Scope.

The vertical path is:

1. Abita authenticates to `portal-api` with its scoped service credential and
   creates an idempotent handoff. The response contains a two-minute,
   single-use SIP destination whose opaque token is stored only as a digest.
2. A synthetic LiveKit caller transfers to that destination and forwards the
   same opaque token in `X-Acuity-Handoff-Token`. Telnyx normalizes the SIP
   request URI to the assigned application number, but preserves the custom
   header in the exact signed webhook body sent to `provider-ingress`.
3. `provider-ingress` verifies the Ed25519 signature and timestamp, commits the
   raw receipt, and only then acknowledges it. It does not project state or call
   Telnyx. Admission fails closed when no valid unique token can be correlated,
   or when the token is expired, consumed, or unknown.
4. `worker` projects receipts and executes previously committed commands.
   Admission consumes the handoff, creates one 20-second logical offer, answers
   the caller, and starts looping ringback.
5. Authorized Admin and Staff browsers maintain one renewable softphone lease
   per User. Available is derived from that lease, current Access, TelnyxRTC
   registration, microphone, audio unlock, and a fresh healthy-session report.
6. Accepting rechecks all authority and readiness under the PostgreSQL Call
   lock. One claimant commits one Dial intent. Other Users receive a committed
   claimed result and no provider leg.
7. The Dial targets the selected User's managed Telnyx credential at
   `sip.telnyx.com`; the Call Control application's custom SIP subdomain is
   reserved for inbound Abita handoff admission. The selected browser
   auto-answers only when the incoming invite carries the same HMAC-derived
   `X-Acuity-Media-Token` committed with its currently accepted Call. Telnyx
   creates distinct Call Control and WebRTC endpoint leg IDs, so those IDs are
   not compared across that boundary. Telnyx `client_state` correlates signed
   provider events, while the opaque custom header correlates the browser media
   invite without exposing Contact Context. The SDK attaches recovery media to
   a muted quarantine output with its microphone fenced; Acuity makes audio
   audible and restores the User's intended microphone state only after current
   lease, Call state, and token validation. A duplicate leg carrying the same
   attempt token is rejected rather than becoming a second media attachment.
   Ringing rejects end only that invite; active or recovering rejects purge the
   local attachment without sending a provider BYE. Signaling recovery starts
   muted and must pass the same authoritative validation before restoring the
   prior microphone intent.
8. Only the matching signed `call.bridged` fact marks the Call Connected.
   Slice 6 supersedes Slice 2's connected-recording behavior: new connected
   human Calls issue no recording command, while historical recording rows
   remain readable. Provider-confirmed termination moves the Call to Needs
   Disposition.

The browser and realtime stream are projections. PostgreSQL is the one source
of truth, and SSE messages are refetch hints.

## Invariants and recovery

- Contact Context never appears in the SIP token, Telnyx `client_state`,
  notifications, provider command identity, or ordinary logs. Before claim,
  Users receive only the sourced display name, short transfer reason, Location,
  and deadline. Expanded phone context is winner-only.
- Platform Operators cannot acquire a softphone lease or media credential.
  Their separate timeline is read-only and contains sanitized states and opaque
  references, not receipt bodies, secrets, recordings, or retry controls.
- One current softphone lease exists per User. A second session is view-only
  until explicit takeover; takeover clears the former session's readiness.
- Media JWTs are issued only to the current authorized lease owner and must
  contain a valid future expiry no more than 29 days away. Telnyx's token API
  exposes no requested TTL and its documented example is 28 days; the
  controlled live gate must verify the account's actual bounded lifetime and
  credential-revocation behavior before patient routing.
- One partial unique PostgreSQL constraint limits each User to one Connecting,
  Connected, Reconciling, or Needs Disposition Call.
- Provider commands have a stable identity and are committed before execution.
  A definitive pre-bridge failure may reopen the same offer only before its
  original deadline. A transport, timeout, conflict, throttling, interrupted
  send, or incomplete response is ambiguous. A returned ambiguous result enters
  Reconciling and is not retried. After a worker interruption while a Call
  Control request is in flight, recovery may resend only the identical durable
  command ID; [Telnyx documents](https://developers.telnyx.com/api-reference/call-commands/dial)
  that duplicate command IDs are ignored, so this
  repairs the pre-request crash point without creating a second provider effect.
- Duplicate receipts and projected facts are idempotent. Out-of-order terminal
  facts cannot regress a Call. Retried receipts are selected by their next
  eligible attempt time, so stale uncorrelated facts cannot starve a newly
  arrived handoff. Unknown signed events remain stored for diagnosis.
- Browser answer and Dial success are never connection proof. After bridge, the
  claimant is final; reconnect or reload may recover only that provider leg and
  cannot elect another User.
- Historical connected-recording data remains readable, but Slice 6 stops
  recording new connected human Calls. Voicemail capture is the sole new audio
  recording path, and no transcription command or schema exists.

## Deterministic proof

`TEST_DATABASE_URL=... go test ./...` exercises the module, authenticated HTTP
surface, exact-byte webhook verification, real PostgreSQL concurrency,
command/receipt recovery, Telnyx HTTP adapter, configuration, and migrations.

`E2E_DATABASE_URL=... ./scripts/run-e2e.sh` resets only a database whose name
ends in `_e2e`, builds the production web application and Go runtime, starts
every runtime role plus a deterministic Telnyx command adapter, and runs the
Slice 1 and Slice 2 Playwright journeys together. Slice 2 uses the real web
application, authenticated HTTP interfaces, SSE refetch path, PostgreSQL state,
provider ingress, receipt projector, and command worker. Only the external
Telnyx API and browser media device are deterministic adapters.

The Slice 2 journey creates two distinct authorized Staff Users, enables both
softphones, commits an authenticated Abita handoff, delivers signed webhook
bodies, and proves that both browsers see the sidebar queue while exactly one
PostgreSQL claimant and one Dial command exist. Only the winner's exact opaque
media token answers, even though the browser endpoint leg ID differs from the
Call Control leg ID. Under the Slice 6 contract, the same journey proves a
provider-confirmed bridge without a new connected-recording command,
same-User tab takeover with old-media fencing, provider-confirmed hangup, and
durable disposition. The browser journey delays the successful Accept response
until after the matching media invite to prove the committed softphone lease
recovers that ordering safely, and replays a second leg with the same token to
prove it is rejected. Integration tests additionally cover no-redial ambiguous
recovery, deadline expiry, invalid JWT boundaries, receipt reordering, and the
sanitized operator timeline.

These tests are deterministic product proof, not live Telnyx, LiveKit, audio, or
GCS proof.

## Controlled live gate

Real patient routing remains disabled. The issue cannot be closed as live-proven
until one explicitly approved synthetic run supplies all of this evidence:

- one synthetic Abita/LiveKit caller and two distinct authorized, ready browser
  Users;
- the opaque handoff token arriving intact in the signed custom header at the
  configured shared Telnyx Call Control Application despite Telnyx normalizing
  the request URI;
- signed public webhook delivery and committed receipt rows;
- both Users seeing the same offer and one PostgreSQL claimant after
  near-simultaneous acceptance;
- one Telnyx linked Dial and only the media-token-correlated browser invite
  auto-answering;
- matching provider bridge events, clear two-way audio, browser reconnect, and
  reload recovery without a second Dial or bridge;
- provider-confirmed hangup, committed disposition, and a sanitized Platform
  Operator timeline explaining the journey; and
- a post-bridge dual-channel WAV object in the dedicated private GCS bucket,
  with no Telnyx or application transcription artifact.

The run requires approved Telnyx, LiveKit, public HTTPS webhook, GCS, browser
audio, synthetic identities, and secret configuration. Health endpoints, mock
audio, successful command responses, or a Dial response alone do not satisfy
this gate.
