import assert from "node:assert/strict"
import test from "node:test"
import { renderToStaticMarkup } from "react-dom/server"

import { CallingCard, CallingFailureNotice } from "./calling-card.tsx"
import type { CallingCall } from "../../lib/api/generated/types.gen.ts"
import type { SoftphoneRuntimeSnapshot } from "../../lib/calling/softphone-runtime.ts"

test("rendered Call states keep one accessible status and stable control order", () => {
  const states = [
    call({ state: "PREPARING" }),
    call({ state: "CONNECTED", connectedAt: "2026-08-26T15:00:00Z" }),
    call({ state: "CONNECTED", endRequested: true }),
  ]

  for (const activeCall of states) {
    const html = render(snapshot({ activeCall }))
    assert.equal(matches(html, 'role="status"'), 1)
    assert.equal(matches(html, 'data-calling-card-shell="calling-card"'), 1)
    assert.ok(
      html.indexOf('data-control-slot="mute"') <
        html.indexOf('data-control-slot="end"'),
    )
    assert.ok(
      html.indexOf('data-control-slot="end"') <
        html.indexOf('data-control-slot="keypad"'),
    )
  }
  assert.match(render(snapshot({ activeCall: states[0] })), /aria-label="Calling…"/)
  assert.match(
    render(snapshot({ activeCall: states[1] })),
    /aria-label="Connected \d+:\d{2}"/,
  )
  assert.match(render(snapshot({ activeCall: states[2] })), /aria-label="Ending…"/)
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

function render(snapshot: SoftphoneRuntimeSnapshot) {
  const noop = () => undefined
  return renderToStaticMarkup(
    <CallingCard
      snapshot={snapshot}
      onAnswer={noop}
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
    expectedCallID: "",
    activeCallLegID: "",
    terminalVersions: {},
    muted: false,
    endingCallID: "",
    pending: {
      availability: false,
      retry: false,
      disposition: false,
    },
    occupied: false,
    controls: {
      canEnd: true,
      canMute: true,
      canKeypad: true,
      canRetry: false,
      canDispose: false,
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
