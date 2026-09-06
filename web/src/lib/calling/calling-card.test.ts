import assert from "node:assert/strict"
import test from "node:test"

import {
  projectCallingCard,
  projectCallingFailure,
  type CallingCardCall,
  type CallingCardOffer,
  type CallingCardSnapshot,
} from "./calling-card.ts"

const now = Date.parse("2026-08-26T15:00:30Z")

const outboundCall: CallingCardCall = {
  id: "call-outbound",
  direction: "OUTBOUND",
  entryPoint: "STANDALONE",
  state: "PREPARING",
  displayName: "",
  phone: "+15551234567",
  locationName: "Downtown",
  transferReason: "",
  connectedAt: undefined,
  retryAllowed: false,
  endRequested: false,
}

function snapshot(
  call: CallingCardCall,
  overrides: Partial<CallingCardSnapshot> = {},
): CallingCardSnapshot {
  return {
    activeCall: call,
    offers: [],
    staffTransfers: [],
    transferCandidates: [],
    activeCallLegID: "",
    endingCallID: "",
    muted: false,
    controls: {
      canEnd: true,
      canMute: false,
      canKeypad: false,
      canRetry: false,
      canDispose: false,
    },
    ...overrides,
  }
}

test("outbound transient states use one Calling status and stable identity order", () => {
  const statuses = (["PREPARING", "RINGING", "CONNECTING"] as const).map(
    (state) =>
      projectCallingCard(snapshot({ ...outboundCall, state }), now),
  )

  assert.deepEqual(
    statuses.map((view) => ({
      shell: view?.shell,
      status: view?.kind === "call" ? view.status : undefined,
      identity: view?.kind === "call" ? view.identity : undefined,
    })),
    ["PREPARING", "RINGING", "CONNECTING"].map(() => ({
      shell: "calling-card",
      status: "Calling…",
      identity: {
        primary: "(555) 123-4567",
        details: ["Downtown"],
      },
    })),
  )
})

test("outbound Connected, Ending, and Outcome states retain the End control slot", () => {
  const connected = {
    ...outboundCall,
    state: "CONNECTED" as const,
    connectedAt: "2026-08-26T15:00:00Z",
  }
  const views = [
    projectCallingCard(snapshot(connected), now),
    projectCallingCard(
      snapshot(connected, { endingCallID: connected.id }),
      now,
    ),
    projectCallingCard(
      {
        ...snapshot(connected),
        activeCall: undefined,
        pendingDisposition: {
          ...connected,
          state: "NEEDS_DISPOSITION",
          endRequested: true,
        },
        controls: {
          canEnd: false,
          canMute: false,
          canKeypad: false,
          canRetry: false,
          canDispose: true,
        },
      },
      now,
    ),
  ]

  assert.deepEqual(
    views.map((view) =>
      view?.kind === "call"
        ? {
            status: view.status,
            endSlot: view.controls.slots.findIndex(
              (control) => control.kind === "end",
            ),
            end: view.controls.slots[1],
          }
        : undefined,
    ),
    [
      {
        status: "Connected",
        endSlot: 1,
        end: { kind: "end", visible: true, disabled: false, label: "End call" },
      },
      {
        status: "Connected",
        endSlot: 1,
        end: { kind: "end", visible: true, disabled: true, label: "Ending…" },
      },
      {
        status: "Call ended",
        endSlot: 1,
        end: { kind: "end", visible: false, disabled: true, label: "End call" },
      },
    ],
  )
})

test("durable endRequested restores the disabled Ending control after reload", () => {
  const view = projectCallingCard(
    snapshot({
      ...outboundCall,
      state: "CONNECTING",
      endRequested: true,
    }),
    now,
  )

  assert.deepEqual(
    view?.kind === "call"
      ? { status: view.status, end: view.controls.slots[1] }
      : undefined,
    {
      status: "Calling…",
      end: { kind: "end", visible: true, disabled: true, label: "Ending…" },
    },
  )
})

test("simultaneous inbound offers share one tray with exact Answer eligibility and countdowns", () => {
  const offers: CallingCardOffer[] = [
    {
      callId: "call-a",
      callLegId: "leg-a",
      displayName: "Jordan Lee",
      phone: "+15551110001",
      locationName: "Downtown",
      transferReason: "Needs help rescheduling",
      deadline: "2026-08-26T15:00:42Z",
      answerReady: true,
    },
    {
      callId: "call-b",
      callLegId: "leg-b",
      displayName: "Incoming caller",
      phone: "+15551110002",
      locationName: "Northside",
      transferReason: "",
      deadline: "2026-08-26T15:00:35Z",
      answerReady: false,
    },
  ]

  const view = projectCallingCard(
    {
      ...snapshot(outboundCall),
      activeCall: undefined,
      offers,
    },
    now,
  )

  assert.deepEqual(view, {
    shell: "calling-card",
    kind: "offers",
    status: "Incoming call",
    trayLabel: "Incoming calls",
    offers: [
      {
        callId: "call-a",
        callLegId: "leg-a",
        identity: {
          primary: "Jordan Lee",
          details: [
            "(555) 111-0001",
            "Downtown",
            "Needs help rescheduling",
          ],
        },
        countdown: "12s",
        countdownLabel: "Incoming offer countdown for (555) 111-0001",
        answer: {
          eligible: true,
          label: "Answer (555) 111-0001",
        },
      },
      {
        callId: "call-b",
        callLegId: "leg-b",
        identity: {
          primary: "(555) 111-0002",
          details: ["Northside"],
        },
        countdown: "5s",
        countdownLabel: "Incoming offer countdown for (555) 111-0002",
        answer: {
          eligible: false,
          label: "Answer (555) 111-0002",
        },
      },
    ],
  })
})

test("Staff transfer offers carry originator context and an exact Decline action", () => {
  const transferOffer: CallingCardOffer = {
    callId: "call-transfer",
    callLegId: "target-leg",
    displayName: "Taylor",
    phone: "+15551234567",
    locationName: "Downtown",
    transferReason: "",
    deadline: "2026-08-26T15:01:00Z",
    answerReady: true,
    offerKind: "STAFF_TRANSFER",
    staffTransferId: "transfer-1",
    originatorEmail: "source@abita.test",
    handoffNote: "Caller needs the secondary desk",
  }
  const view = projectCallingCard(
    {
      ...snapshot(outboundCall),
      activeCall: undefined,
      offers: [transferOffer],
    },
    now,
  )

  assert.equal(view?.kind, "offers")
  if (view?.kind !== "offers") return
  assert.equal(view.status, "Incoming transfer")
  assert.deepEqual(view.offers[0]?.identity.details, [
    "(555) 123-4567",
    "Downtown",
    "From source@abita.test",
    "Caller needs the secondary desk",
  ])
  assert.equal(
    view.offers[0]?.decline?.label,
    "Decline transfer from source@abita.test",
  )

  const pendingView = projectCallingCard(
    {
      ...snapshot(outboundCall),
      activeCall: undefined,
      offers: [transferOffer],
      pending: { transfer: true },
    },
    now,
  )
  assert.equal(
    pendingView?.kind === "offers"
      ? pendingView.offers[0]?.answer.eligible
      : undefined,
    false,
  )
  assert.equal(
    pendingView?.kind === "offers"
      ? pendingView.offers[0]?.decline?.disabled
      : undefined,
    true,
  )
})

test("the source card projects one active transfer with cancellation", () => {
  const connected = {
    ...outboundCall,
    id: "call-transfer-source",
    state: "CONNECTED" as const,
    connectedAt: "2026-08-26T15:00:00Z",
  }
  const view = projectCallingCard(
    snapshot(connected, {
      activeCallLegID: "source-leg",
      staffTransfers: [
        {
          id: "transfer-1",
          callId: connected.id,
          sourceCallLegId: "source-leg",
          recipientEmail: "target@abita.test",
          state: "ACCEPTED",
        },
      ],
    }),
    now,
  )

  assert.equal(view?.kind, "call")
  if (view?.kind !== "call") return
  assert.deepEqual(view.transfer.active, {
    id: "transfer-1",
    recipientEmail: "target@abita.test",
    status: "Staff answered · confirming transfer",
    canCancel: true,
  })
  assert.equal(view.transfer.canStart, false)
})

test("expired inbound offers are never visible or answerable", () => {
  const offers: CallingCardOffer[] = [
    {
      callId: "call-expired",
      callLegId: "leg-expired",
      displayName: "Expired caller",
      phone: "+15551110003",
      locationName: "Downtown",
      transferReason: "",
      deadline: "2026-08-26T15:00:30Z",
      answerReady: true,
    },
    {
      callId: "call-current",
      callLegId: "leg-current",
      displayName: "Current caller",
      phone: "+15551110004",
      locationName: "Northside",
      transferReason: "",
      deadline: "2026-08-26T15:00:31Z",
      answerReady: true,
    },
  ]

  const view = projectCallingCard(
    {
      ...snapshot(outboundCall),
      activeCall: undefined,
      offers,
    },
    now,
  )

  assert.deepEqual(
    view?.kind === "offers"
      ? view.offers.map((offer) => ({
          callLegId: offer.callLegId,
          countdown: offer.countdown,
          eligible: offer.answer.eligible,
        }))
      : [],
    [{ callLegId: "leg-current", countdown: "1s", eligible: true }],
  )
  assert.equal(
    projectCallingCard(
      {
        ...snapshot(outboundCall, {
          failure: {
            kind: "temporary-request",
            message: "refresh failed",
            recoverable: true,
          },
        }),
        activeCall: undefined,
        offers: [offers[0]],
      },
      now,
    ),
    undefined,
  )
})

test("inbound Connecting, Connected, Ending, and Outcome keep one identity and control hierarchy", () => {
  const inbound: CallingCardCall = {
    ...outboundCall,
    id: "call-inbound",
    direction: "INBOUND",
    entryPoint: "AI_HANDOFF",
    state: "CONNECTING",
    displayName: "Jordan Lee",
    phone: "+15551110001",
    locationName: "Downtown",
    transferReason: "Needs help rescheduling",
  }
  const connected = {
    ...inbound,
    state: "CONNECTED" as const,
    connectedAt: "2026-08-26T15:00:00Z",
  }
  const views = [
    projectCallingCard(
      snapshot(inbound, {
        controls: {
          canEnd: true,
          canMute: true,
          canKeypad: false,
          canRetry: false,
          canDispose: false,
        },
      }),
      now,
    ),
    projectCallingCard(
      snapshot(connected, {
        controls: {
          canEnd: true,
          canMute: true,
          canKeypad: true,
          canRetry: false,
          canDispose: false,
        },
      }),
      now,
    ),
    projectCallingCard(
      snapshot(connected, { endingCallID: connected.id }),
      now,
    ),
    projectCallingCard(
      {
        ...snapshot(connected),
        activeCall: undefined,
        pendingDisposition: {
          ...connected,
          state: "NEEDS_DISPOSITION",
        },
      },
      now,
    ),
  ]

  assert.deepEqual(
    views.map((view) =>
      view?.kind === "call"
        ? {
            status: view.status,
            identity: view.identity,
            slots: view.controls.slots.map((control) => control.kind),
          }
        : undefined,
    ),
    ["Connecting…", "Connected", "Connected", "Call ended"].map(
      (status) => ({
        status,
        identity: {
          primary: "Jordan Lee",
          details: [
            "(555) 111-0001",
            "Downtown",
            "Needs help rescheduling",
          ],
        },
        slots: ["mute", "end", "keypad"],
      }),
    ),
  )
})

test("typed failures replace provider details with concise Staff recovery copy", () => {
  const kinds = [
    "authentication",
    "access",
    "ownership",
    "technical-readiness",
    "media",
    "conflict",
    "temporary-request",
  ] as const

  const failures = kinds.map((kind) => {
    const view = projectCallingCard(
      snapshot(outboundCall, {
        failure: {
          kind,
          message: "Provider media failed while waiting for exact leg.",
          recoverable: kind !== "authentication" && kind !== "access",
        },
      }),
      now,
    )
    return view?.failure
  })

  assert.deepEqual(failures, [
    {
      title: "Calling session expired",
      message: "Refresh the page to reconnect. You may need to sign in again.",
    },
    {
      title: "Calling unavailable",
      message: "Your account doesn’t have access to calling for this practice.",
    },
    {
      title: "Calling is open elsewhere",
      message:
        "Calling is connected in another browser. Use this browser instead.",
      action: { kind: "recover", label: "Use this browser" },
    },
    {
      title: "Calling disconnected",
      message: "Refresh the page to reconnect. Calls are paused until then.",
      action: { kind: "reload-page", label: "Refresh page" },
    },
    {
      title: "Calling disconnected",
      message: "Refresh the page to reconnect. Calls are paused until then.",
      action: { kind: "reload-page", label: "Refresh page" },
    },
    {
      title: "Calling request failed",
      message: "The call or calling session changed. Try the action again.",
    },
    {
      title: "Calling request failed",
      message: "Your request could not be confirmed. Try the action again.",
    },
  ])
})

test("active Call ownership stays visible without offering an unsafe takeover", () => {
  const view = projectCallingCard(
    snapshot(outboundCall, {
      failure: {
        kind: "ownership",
        message: "technical active Call detail",
        recoverable: false,
      },
    }),
    now,
  )

  assert.deepEqual(view?.failure, {
    title: "Calling is open elsewhere",
    message:
      "A call is active in another browser. Finish it there before using this browser.",
  })
})

test("failed stop cleanup reports an unconfirmed request without a false action", () => {
  const message =
    "Calling stopped locally, but backend readiness could not be cleared. Calling could not reach the service."
  assert.deepEqual(
    projectCallingFailure({
      kind: "temporary-request",
      message,
      recoverable: false,
    }),
    {
      title: "Calling request failed",
      message: "Your request could not be confirmed.",
    },
  )
})

test("Outcome exposes only the Staff actions supported by the committed Call", () => {
  const calls: CallingCardCall[] = [
    {
      ...outboundCall,
      direction: "INBOUND",
      entryPoint: "AI_HANDOFF",
      state: "NEEDS_DISPOSITION",
    },
    {
      ...outboundCall,
      entryPoint: "TASK",
      state: "NEEDS_DISPOSITION",
      connectedAt: "2026-08-26T15:00:00Z",
    },
    {
      ...outboundCall,
      state: "UNANSWERED",
      retryAllowed: true,
    },
  ]

  const actions = calls.map((call, index) => {
    const view = projectCallingCard(
      {
        ...snapshot(call),
        controls: {
          canEnd: false,
          canMute: false,
          canKeypad: false,
          canRetry: index === 2,
          canDispose: index !== 2,
        },
      },
      now,
    )
    return view?.kind === "call" ? view.actions : undefined
  })

  assert.deepEqual(actions, [
    {
      dispositions: [
        {
          outcome: "RESOLVED",
          label: "Resolved on call",
          primary: true,
          disabled: false,
        },
        {
          outcome: "FOLLOW_UP_REQUIRED",
          label: "Create follow-up task",
          primary: false,
          disabled: false,
        },
      ],
    },
    {
      dispositions: [
        {
          outcome: "COMPLETE_TASK",
          label: "Complete task",
          primary: true,
          disabled: false,
        },
        {
          outcome: "KEEP_OPEN",
          label: "Keep task open",
          primary: false,
          disabled: false,
        },
      ],
    },
    {
      dispositions: [],
      retry: { label: "Call again", disabled: false },
      close: { label: "Dismiss" },
    },
  ])
})

test("Outcome actions stay visible and disabled while disposition commits", () => {
  const pendingDisposition: CallingCardCall = {
    ...outboundCall,
    direction: "INBOUND",
    entryPoint: "AI_HANDOFF",
    state: "NEEDS_DISPOSITION",
  }
  const view = projectCallingCard(
    {
      ...snapshot(pendingDisposition),
      activeCall: undefined,
      pendingDisposition,
      pending: { disposition: true },
      controls: {
        canEnd: false,
        canMute: false,
        canKeypad: false,
        canRetry: false,
        canDispose: false,
      },
    },
    now,
  )

  assert.deepEqual(view?.kind === "call" ? view.actions.dispositions : [], [
    {
      outcome: "RESOLVED",
      label: "Resolved on call",
      primary: true,
      disabled: true,
    },
    {
      outcome: "FOLLOW_UP_REQUIRED",
      label: "Create follow-up task",
      primary: false,
      disabled: true,
    },
  ])
})

test("settled non-disposition Outcomes always offer Close", () => {
  const states = [
    "UNANSWERED",
    "VOICEMAIL",
    "MISSED",
    "RESOLVED",
    "FOLLOW_UP_REQUIRED",
  ] as const

  const actions = states.map((state) => {
    const view = projectCallingCard(
      {
        ...snapshot({ ...outboundCall, state, retryAllowed: false }),
        controls: {
          canEnd: false,
          canMute: false,
          canKeypad: false,
          canRetry: false,
          canDispose: false,
        },
      },
      now,
    )
    return view?.kind === "call" ? view.actions : undefined
  })

  assert.deepEqual(
    actions,
    states.map(() => ({
      dispositions: [],
      close: { label: "Dismiss" },
    })),
  )
})

test("Close waits while a retry or terminal media purge is pending", () => {
  const view = projectCallingCard(
    {
      ...snapshot({
        ...outboundCall,
        state: "UNANSWERED",
        retryAllowed: true,
      }),
      pending: { retry: true },
      controls: {
        canEnd: false,
        canMute: false,
        canKeypad: false,
        canRetry: true,
        canDispose: false,
      },
    },
    now,
  )

  assert.deepEqual(view?.kind === "call" ? view.actions : undefined, {
    dispositions: [],
    retry: { label: "Calling…", disabled: true },
  })

  const purging = projectCallingCard(
    {
      ...snapshot({ ...outboundCall, state: "UNANSWERED" }),
      mediaAttachment: {},
      controls: {
        canEnd: false,
        canMute: false,
        canKeypad: false,
        canRetry: false,
        canDispose: false,
      },
    },
    now,
  )
  assert.deepEqual(purging?.kind === "call" ? purging.actions : undefined, {
    dispositions: [],
  })
})

test("an unanswered destination, cancellation and failed connection have distinct results", () => {
  const attempts = [
    { providerTermination: "NO_ANSWER", endRequested: false, expected: "No answer" },
    { providerTermination: "BUSY", endRequested: false, expected: "Line busy" },
    { providerTermination: "DECLINED", endRequested: false, expected: "Call declined" },
    { providerTermination: "FAILED", endRequested: false, expected: "Call couldn’t connect" },
    { providerTermination: "MEDIA_READINESS_FAILED", endRequested: false, expected: "Call couldn’t connect" },
    { providerTermination: "COMPLETED", endRequested: true, expected: "Call ended" },
    // A provider originator_cancel is normalized to FAILED; Staff intent remains durable.
    { providerTermination: "FAILED", endRequested: true, expected: "Call ended" },
  ]
  for (const { expected, ...attempt } of attempts) {
    const view = projectCallingCard(snapshot({ ...outboundCall, ...attempt, state: "UNANSWERED" }), now)
    assert.equal(view?.kind === "call" ? view.status : undefined, expected)
  }
})
