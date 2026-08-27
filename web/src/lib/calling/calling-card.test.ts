import assert from "node:assert/strict"
import test from "node:test"

import {
  projectCallingCard,
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
        status: "Connected 00:30",
        endSlot: 1,
        end: { kind: "end", visible: true, disabled: false, label: "End" },
      },
      {
        status: "Ending…",
        endSlot: 1,
        end: { kind: "end", visible: true, disabled: true, label: "Ending…" },
      },
      {
        status: "Outcome",
        endSlot: 1,
        end: { kind: "end", visible: false, disabled: true, label: "End" },
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
      status: "Ending…",
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
    ["Connecting…", "Connected 00:30", "Ending…", "Outcome"].map(
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
      role: "alert",
      title: "Calling needs attention",
      message: "Sign in again to keep calling.",
    },
    {
      role: "alert",
      title: "Calling needs attention",
      message: "You do not have calling access for this practice.",
    },
    {
      role: "alert",
      title: "Calling needs attention",
      message:
        "Calling is active in another browser. Take over here to use this device.",
      action: { label: "Take over" },
    },
    {
      role: "alert",
      title: "Calling needs attention",
      message: "Allow microphone access, then reconnect calling.",
      action: { label: "Reconnect calling" },
    },
    {
      role: "alert",
      title: "Calling needs attention",
      message: "Audio disconnected. Reconnect calling to continue.",
      action: { label: "Reconnect calling" },
    },
    {
      role: "alert",
      title: "Calling needs attention",
      message: "This call changed elsewhere. Refresh to see the latest state.",
      action: { label: "Refresh calling" },
    },
    {
      role: "alert",
      title: "Calling needs attention",
      message: "Calling could not refresh. Check your connection and try again.",
      action: { label: "Refresh calling" },
    },
  ])
})

test("a recoverable failure remains visible without a Call or incoming offer", () => {
  const view = projectCallingCard(
    {
      ...snapshot(outboundCall),
      activeCall: undefined,
      offers: [],
      failure: {
        kind: "ownership",
        message: "Lease conflict from backend implementation detail.",
        recoverable: true,
      },
    },
    now,
  )

  assert.deepEqual(view, {
    shell: "calling-card",
    kind: "failure",
    failure: {
      role: "alert",
      title: "Calling needs attention",
      message:
        "Calling is active in another browser. Take over here to use this device.",
      action: { label: "Take over" },
    },
  })
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
          label: "Resolved",
          primary: true,
          disabled: false,
        },
        {
          outcome: "FOLLOW_UP_REQUIRED",
          label: "Follow-up needed",
          primary: false,
          disabled: false,
        },
      ],
    },
    {
      dispositions: [
        {
          outcome: "COMPLETE_TASK",
          label: "Resolved",
          primary: true,
          disabled: false,
        },
        {
          outcome: "KEEP_OPEN",
          label: "Follow-up needed",
          primary: false,
          disabled: false,
        },
      ],
    },
    {
      dispositions: [],
      retry: { label: "Try again", disabled: false },
      close: { label: "Close", disabled: false },
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
      label: "Resolved",
      primary: true,
      disabled: true,
    },
    {
      outcome: "FOLLOW_UP_REQUIRED",
      label: "Follow-up needed",
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
      close: { label: "Close", disabled: false },
    })),
  )
})

test("Close waits while a retry is pending", () => {
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
    retry: { label: "Preparing…", disabled: true },
  })
})
