import assert from "node:assert/strict"
import test from "node:test"
import { renderToStaticMarkup } from "react-dom/server"

import { CallingCard, CallingFailureNotice } from "./calling-card.tsx"
import type { CallingCall } from "../../lib/api/generated/types.gen.ts"
import type { SoftphoneRuntimeSnapshot } from "../../lib/calling/softphone-runtime.ts"

test("connected controls stay ordered during hangup without claiming the call ended", () => {
  for (const endRequested of [false, true]) {
    const html = render(snapshot({ activeCall: call({
      state: "CONNECTED", connectedAt: "2026-08-26T15:00:00Z", endRequested,
    }) }))
    assert.equal(matches(html, 'role="status"'), 1)
    assert.match(html, /aria-label="Connected"/)
    assert.doesNotMatch(html, /Call ended|Outcome/)
    const controls = ["mute", "keypad", "transfer", "end"]
    for (let index = 1; index < controls.length; index++) {
      assert.ok(html.indexOf(`data-control-slot="${controls[index - 1]}"`) <
        html.indexOf(`data-control-slot="${controls[index]}"`))
    }
    if (endRequested) assert.match(html, /aria-label="Ending…"[^>]*disabled/)
  }
})

test("dialing shows just Calling and Cancel call", () => {
  const html = render(snapshot({ activeCall: call({ state: "PREPARING" }) }))
  assert.match(html, /aria-label="Calling…"/)
  assert.match(html, /aria-label="Cancel call"/)
  assert.doesNotMatch(html, /data-control-slot="(?:mute|keypad|transfer)"/)
})

test("no answer has an explicit result, redial and dismissal with no empty controls", () => {
  const html = render(snapshot({
    pendingDisposition: call({ state: "UNANSWERED", retryAllowed: true }),
    controls: { canEnd: false, canMute: false, canKeypad: false, canTransfer: false,
      canRetry: true, canDispose: false },
  }))
  assert.match(html, /aria-label="No answer"/)
  assert.match(html, />Call again</)
  assert.match(html, />Dismiss</)
  assert.doesNotMatch(html, /Outcome|Try again|data-slot="card-footer"|data-control-slot/)
})

test("muting is a pressed control and the timer is outside the live status", () => {
  const html = render(snapshot({ muted: true, activeCall: call({ state: "CONNECTED" }) }))
  assert.match(html, /aria-label="Unmute" aria-pressed="true"/)
  assert.match(html, /role="status" aria-label="Connected">Connected<\/span>/)
  assert.match(html, /aria-label="Call duration/)
})

test("rendered ownership and media failures expose their exact recovery actions", () => {
  const ownership = render(
    snapshot({
      activeCall: call({ state: "CONNECTED" }),
      lease: { ...lease(), owner: false, sessionId: "other-session" },
      failure: {
        kind: "ownership",
        message: "technical lease detail",
        recoverable: true,
      },
    }),
  )
  assert.match(ownership, /role="alert"/)
  assert.match(ownership, />Use this browser</)
  assert.doesNotMatch(ownership, /technical lease detail/)

  const activeOwnership = renderToStaticMarkup(
    <CallingFailureNotice
      failure={{
        kind: "ownership",
        message: "technical active Call detail",
        recoverable: false,
      }}
      onRecover={() => undefined}
    />,
  )
  assert.match(activeOwnership, /Finish it there/)
  assert.doesNotMatch(activeOwnership, />Use this browser</)

  const media = render(
    snapshot({
      activeCall: call({ state: "CONNECTED" }),
      failure: {
        kind: "media",
        message: "provider implementation detail",
        recoverable: true,
      },
    }),
  )
  assert.match(media, />Calling disconnected</)
  assert.match(media, /Calls are paused until then/)
  assert.match(media, />Refresh page</)
  assert.match(media, /border-warning\/35/)
  assert.doesNotMatch(media, /provider implementation detail/)
})

test("a failure without a Call or incoming offer does not render a Calling Card", () => {
  const failure = {
    kind: "ownership" as const,
    message: "technical lease detail",
    recoverable: true,
  }
  const html = render(snapshot({ failure }))
  const recovery = renderToStaticMarkup(
    <CallingFailureNotice failure={failure} onRecover={() => undefined} />,
  )

  assert.equal(html, "")
  assert.match(recovery, /role="alert"/)
  assert.match(recovery, />Use this browser</)
  assert.doesNotMatch(recovery, /technical lease detail/)
})

test("live call warnings distinguish delayed requests and automatic reconnects without offering reload", () => {
  const failures = [
    {
      kind: "temporary-request" as const,
      source: "refresh" as const,
      title: "Call updates delayed",
    },
    {
      kind: "temporary-request" as const,
      source: "readiness" as const,
      title: "Calling service unavailable",
    },
    {
      kind: "media" as const,
      source: "media-reconnect" as const,
      title: "Calling reconnecting",
    },
  ]

  for (const { kind, source, title } of failures) {
    const html = render(
      snapshot({
        activeCall: call({ state: "CONNECTED" }),
        failure: { kind, source, message: "technical request detail", recoverable: true },
      }),
    )

    assert.match(html, new RegExp(`>${title}<`))
    assert.match(html, /role="alert"/)
    assert.match(html, /aria-label="Connected/)
    assert.match(html, /aria-label="End call"/)
    assert.doesNotMatch(html, /Calling disconnected|Calls are paused|Refresh page/)
    assert.doesNotMatch(html, /technical request detail/)
  }
})

test("rendered simultaneous offers stay in one tray with exact CallLeg actions", () => {
  const html = render(
    snapshot({
      offers: [
        offer("call-1", "leg-1", "+15551110001"),
        offer("call-2", "leg-2", "+15551110002"),
      ],
    }),
  )

  assert.equal(matches(html, 'aria-label="Incoming calls"'), 1)
  assert.equal(matches(html, 'role="status"'), 1)
  assert.match(html, /data-call-leg-id="leg-1"/)
  assert.match(html, /data-call-leg-id="leg-2"/)
  assert.match(html, /aria-label="Answer \(555\) 111-0001"/)
  assert.match(html, /aria-label="Answer \(555\) 111-0002"/)
})

test("rendered Staff transfer exposes handoff context, Answer, and Decline", () => {
  const transferOffer = {
    ...offer("call-transfer", "target-leg", "+15551110001"),
    offerKind: "STAFF_TRANSFER" as const,
    staffTransferId: "transfer-1",
    originatorEmail: "source@abita.test",
    handoffNote: "Caller needs the secondary desk",
  }
  const html = render(snapshot({ offers: [transferOffer] }))

  assert.match(html, /Incoming transfer/)
  assert.match(html, /From source@abita.test/)
  assert.match(html, /Caller needs the secondary desk/)
  assert.match(html, /aria-label="Decline transfer from source@abita.test"/)
  assert.match(html, /aria-label="Answer \(555\) 111-0001"/)
})

function render(snapshot: SoftphoneRuntimeSnapshot) {
  const noop = () => undefined
  return renderToStaticMarkup(
    <CallingCard
      snapshot={snapshot}
      onAnswer={noop}
      onDecline={noop}
      onLoadTransferCandidates={noop}
      onRequestTransfer={noop}
      onCancelTransfer={noop}
      onEnd={noop}
      onMute={noop}
      onDTMF={noop}
      onDisposition={noop}
      onRetry={noop}
      onRecover={noop}
      onClose={noop}
    />,
  )
}

function snapshot(
  overrides: Partial<SoftphoneRuntimeSnapshot> = {},
): SoftphoneRuntimeSnapshot {
  return {
    phase: "running",
    lease: lease(),
    availabilityIntent: false,
    readiness: {
      mediaState: "ready",
      microphoneReady: true,
      audioReady: true,
      sessionHealthy: true,
    },
    offers: [],
    staffTransfers: [],
    transferCandidates: [],
    expectedCallID: "",
    activeCallLegID: "",
    terminalVersions: {},
    muted: false,
    endingCallID: "",
    committedOwner: true,
    pending: {
      availability: false,
      retry: false,
      disposition: false,
      transfer: false,
    },
    occupied: false,
    controls: {
      canEnd: true,
      canMute: true,
      canKeypad: true,
      canRetry: false,
      canDispose: false,
      canTransfer: false,
    },
    ...overrides,
  }
}

function call(overrides: Partial<CallingCall>): CallingCall {
  return {
    id: "call-1",
    practiceId: "practice-1",
    locationId: "location-1",
    locationName: "Downtown",
    direction: "OUTBOUND",
    entryPoint: "STANDALONE",
    state: "PREPARING",
    phone: "+15551234567",
    phoneSource: "STAFF",
    displayName: "Taylor",
    nameSource: "STAFF",
    transferReason: "",
    reasonSource: "",
    providerTermination: "",
    endRequested: false,
    callerId: "+15557654321",
    retryAllowed: false,
    version: 1,
    ...overrides,
  }
}

function offer(callId: string, callLegId: string, phone: string) {
  return {
    callId,
    callLegId,
    mediaToken: `${callLegId}-media-token`,
    practiceId: "practice-1",
    locationId: "location-1",
    locationName: "Downtown",
    displayName: "Incoming caller",
    phone,
    transferReason: "Scheduling help",
    state: "RINGING" as const,
    version: 1,
    createdAt: "2026-08-26T15:00:00Z",
    deadline: "2099-08-26T15:01:00Z",
    offerKind: "INBOUND_OFFER" as const,
    staffTransferId: "",
    originatorEmail: "",
    handoffNote: "",
    answerReady: true,
  }
}

function lease() {
  return {
    sessionId: "session-1",
    leaseExpiresAt: "2026-08-26T15:01:00Z",
    owner: true,
    available: false,
    activeCallId: "",
    pendingOutcomeCallId: "",
  }
}

function matches(value: string, pattern: string) {
  return value.split(pattern).length - 1
}
