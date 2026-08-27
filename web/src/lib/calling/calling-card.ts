export type CallingCardCall = {
  id: string
  direction: "INBOUND" | "OUTBOUND"
  entryPoint: "AI_HANDOFF" | "TASK" | "STANDALONE"
  state:
    | "PREPARING"
    | "RINGING"
    | "CONNECTING"
    | "CONNECTED"
    | "VOICEMAIL_GREETING"
    | "VOICEMAIL_RECORDING"
    | "UNANSWERED"
    | "VOICEMAIL"
    | "MISSED"
    | "NEEDS_DISPOSITION"
    | "RESOLVED"
    | "FOLLOW_UP_REQUIRED"
  displayName: string
  phone: string
  locationName: string
  transferReason: string
  connectedAt?: string
  retryAllowed: boolean
  endRequested: boolean
}

export type CallingCardOffer = {
  callId: string
  callLegId: string
  displayName: string
  phone: string
  locationName: string
  transferReason: string
  deadline: string
  answerReady: boolean
}

export type CallingCardSnapshot = {
  pendingCall?: CallingCardCall
  activeCall?: CallingCardCall
  pendingDisposition?: CallingCardCall
  offers: readonly CallingCardOffer[]
  endingCallID: string
  muted: boolean
  failure?: CallingCardFailure
  pending?: {
    retry?: boolean
    outbound?: boolean
    disposition?: boolean
  }
  controls: {
    canEnd: boolean
    canMute: boolean
    canKeypad: boolean
    canRetry: boolean
    canDispose: boolean
  }
}

export type CallingCardFailure = {
  kind:
    | "authentication"
    | "access"
    | "ownership"
    | "technical-readiness"
    | "media"
    | "conflict"
    | "temporary-request"
  message: string
  recoverable: boolean
}

export type CallingCardCallView = {
  shell: "calling-card"
  kind: "call"
  callId: string
  status: string
  identity: {
    primary: string
    details: string[]
  }
  controls: {
    slots: [CallingCardControl, CallingCardControl, CallingCardControl]
  }
  actions: CallingCardActions
  failure?: CallingCardFailureView
}

export type CallingCardOfferView = {
  shell: "calling-card"
  kind: "offers"
  status: "Incoming call"
  trayLabel: "Incoming calls"
  offers: Array<{
    callId: string
    callLegId: string
    identity: CallingCardIdentity
    countdown: string
    countdownLabel: string
    answer: {
      eligible: boolean
      label: string
    }
  }>
  failure?: CallingCardFailureView
}

export type CallingCardFailureOnlyView = {
  shell: "calling-card"
  kind: "failure"
  failure: CallingCardFailureView
}

export type CallingCardView =
  | CallingCardCallView
  | CallingCardOfferView
  | CallingCardFailureOnlyView

export type CallingCardIdentity = {
  primary: string
  details: string[]
}

export type CallingCardFailureView = {
  role: "alert"
  title: "Calling needs attention"
  message: string
  action?: {
    label: "Take over" | "Reconnect calling" | "Refresh calling"
  }
}

export type CallingDispositionOutcome =
  | "RESOLVED"
  | "FOLLOW_UP_REQUIRED"
  | "COMPLETE_TASK"
  | "KEEP_OPEN"
  | "CREATE_TASK"
  | "NO_FOLLOW_UP"

export type CallingCardActions = {
  dispositions: Array<{
    outcome: CallingDispositionOutcome
    label: string
    primary: boolean
    disabled: boolean
  }>
  retry?: {
    label: string
    disabled: boolean
  }
  close?: {
    label: "Close"
    disabled: boolean
  }
}

export type CallingCardControl = {
  kind: "mute" | "end" | "keypad"
  visible: boolean
  disabled: boolean
  label: string
}

export function projectCallingCard(
  snapshot: CallingCardSnapshot,
  _now: number,
): CallingCardView | undefined {
  const call =
    snapshot.activeCall ?? snapshot.pendingCall ?? snapshot.pendingDisposition
  if (!call) {
    const activeOffers = snapshot.offers.filter(
      (offer) => new Date(offer.deadline).getTime() > _now,
    )
    if (activeOffers.length === 0) {
      if (!snapshot.failure) return undefined
      return {
        shell: "calling-card",
        kind: "failure",
        failure: projectFailure(snapshot.failure),
      }
    }
    return {
      shell: "calling-card",
      kind: "offers",
      status: "Incoming call",
      trayLabel: "Incoming calls",
      ...(snapshot.failure ? { failure: projectFailure(snapshot.failure) } : {}),
      offers: activeOffers.map((offer) => {
        const phone = formatPhone(offer.phone) || "Phone unavailable"
        return {
          callId: offer.callId,
          callLegId: offer.callLegId,
          identity: callIdentity(offer, "INBOUND"),
          countdown: `${secondsRemaining(offer.deadline, _now)}s`,
          countdownLabel: `Incoming offer countdown for ${phone}`,
          answer: {
            eligible: offer.answerReady,
            label: `Answer ${phone}`,
          },
        }
      }),
    }
  }

  const ending =
    !isOutcomeState(call.state) &&
    (snapshot.endingCallID === call.id || call.endRequested)

  return {
    shell: "calling-card",
    kind: "call",
    callId: call.id,
    status: callStatus(call, ending, _now),
    identity: callIdentity(call, call.direction),
    controls: {
      slots: [
        {
          kind: "mute",
          visible: snapshot.controls.canMute,
          disabled: ending,
          label: snapshot.muted ? "Unmute" : "Mute",
        },
        {
          kind: "end",
          visible: snapshot.controls.canEnd || ending,
          disabled: ending || !snapshot.controls.canEnd,
          label: ending ? "Ending…" : "End",
        },
        {
          kind: "keypad",
          visible: snapshot.controls.canKeypad,
          disabled: ending,
          label: "Keypad",
        },
      ],
    },
    actions: projectActions(call, snapshot),
    ...(snapshot.failure ? { failure: projectFailure(snapshot.failure) } : {}),
  }
}

function projectActions(
  call: CallingCardCall,
  snapshot: CallingCardSnapshot,
): CallingCardActions {
  const dispositionPending = Boolean(snapshot.pending?.disposition)
  const dispositions =
    snapshot.controls.canDispose ||
    (call.state === "NEEDS_DISPOSITION" && dispositionPending)
    ? dispositionChoices(call).map((choice) => ({
        ...choice,
        disabled: dispositionPending,
      }))
    : []
  const retry = snapshot.controls.canRetry && call.retryAllowed
    ? {
        label:
          snapshot.pending?.retry || snapshot.pending?.outbound
            ? "Preparing…"
            : "Try again",
        disabled: Boolean(snapshot.pending?.retry || snapshot.pending?.outbound),
      }
    : undefined
  const close =
    settledOutcomeCanClose(call.state) &&
    !snapshot.pending?.retry &&
    !snapshot.pending?.outbound &&
    !snapshot.pending?.disposition
      ? { label: "Close" as const, disabled: false }
      : undefined
  return {
    dispositions,
    ...(retry ? { retry } : {}),
    ...(close ? { close } : {}),
  }
}

function settledOutcomeCanClose(state: CallingCardCall["state"]) {
  return [
    "UNANSWERED",
    "VOICEMAIL",
    "MISSED",
    "RESOLVED",
    "FOLLOW_UP_REQUIRED",
  ].includes(state)
}

function dispositionChoices(
  call: CallingCardCall,
): Array<Omit<CallingCardActions["dispositions"][number], "disabled">> {
  if (call.direction === "INBOUND") {
    return [
      { outcome: "RESOLVED", label: "Resolved", primary: true },
      {
        outcome: "FOLLOW_UP_REQUIRED",
        label: "Follow-up needed",
        primary: false,
      },
    ]
  }
  if (call.entryPoint === "TASK") {
    return [
      { outcome: "COMPLETE_TASK", label: "Resolved", primary: true },
      { outcome: "KEEP_OPEN", label: "Follow-up needed", primary: false },
    ]
  }
  if (call.connectedAt) {
    return [
      { outcome: "RESOLVED", label: "Resolved", primary: true },
      { outcome: "CREATE_TASK", label: "Follow-up needed", primary: false },
    ]
  }
  return [
    { outcome: "NO_FOLLOW_UP", label: "No follow-up", primary: true },
    { outcome: "CREATE_TASK", label: "Create task", primary: false },
  ]
}

function projectFailure(
  failure: CallingCardFailure,
): CallingCardFailureView {
  return {
    role: "alert",
    title: "Calling needs attention",
    message: failureCopy(failure.kind),
    ...(failure.recoverable
      ? {
          action: {
            label:
              failure.kind === "ownership"
                ? ("Take over" as const)
                : failure.kind === "conflict" ||
                    failure.kind === "temporary-request"
                  ? ("Refresh calling" as const)
                  : ("Reconnect calling" as const),
          },
        }
      : {}),
  }
}

function failureCopy(kind: CallingCardFailure["kind"]) {
  switch (kind) {
    case "authentication":
      return "Sign in again to keep calling."
    case "access":
      return "You do not have calling access for this practice."
    case "ownership":
      return "Calling is active in another browser. Take over here to use this device."
    case "technical-readiness":
      return "Allow microphone access, then reconnect calling."
    case "media":
      return "Audio disconnected. Reconnect calling to continue."
    case "conflict":
      return "This call changed elsewhere. Refresh to see the latest state."
    case "temporary-request":
      return "Calling could not refresh. Check your connection and try again."
  }
}

function callIdentity(
  call: Pick<
    CallingCardCall | CallingCardOffer,
    "displayName" | "phone" | "locationName" | "transferReason"
  >,
  direction: CallingCardCall["direction"],
): CallingCardIdentity {
  const phone = formatPhone(call.phone)
  const name = meaningfulName(call.displayName)
  const primary = name || phone || (direction === "INBOUND" ? "Caller" : "Destination")
  return {
    primary,
    details: [name ? phone : "", call.locationName, call.transferReason].filter(
      Boolean,
    ),
  }
}

function meaningfulName(displayName: string) {
  const name = displayName.trim()
  return name.toLowerCase() === "incoming caller" ? "" : name
}

function callStatus(call: CallingCardCall, ending: boolean, now: number) {
  if (ending) return "Ending…"
  if (call.state === "CONNECTED") {
    return `Connected ${elapsedTime(call.connectedAt, now)}`
  }
  if (isOutcomeState(call.state)) return "Outcome"
  return call.direction === "OUTBOUND" ? "Calling…" : "Connecting…"
}

function isOutcomeState(state: CallingCardCall["state"]) {
  return [
    "NEEDS_DISPOSITION",
    "UNANSWERED",
    "VOICEMAIL",
    "MISSED",
    "RESOLVED",
    "FOLLOW_UP_REQUIRED",
  ].includes(state)
}

function elapsedTime(connectedAt: string | undefined, now: number) {
  if (!connectedAt) return "00:00"
  const elapsedSeconds = Math.max(
    0,
    Math.floor((now - new Date(connectedAt).getTime()) / 1_000),
  )
  const minutes = String(Math.floor(elapsedSeconds / 60)).padStart(2, "0")
  const seconds = String(elapsedSeconds % 60).padStart(2, "0")
  return `${minutes}:${seconds}`
}

function secondsRemaining(deadline: string, now: number) {
  return Math.max(0, Math.ceil((new Date(deadline).getTime() - now) / 1_000))
}

function formatPhone(phone: string) {
  const match = phone.match(/^\+1(\d{3})(\d{3})(\d{4})$/)
  if (!match) return phone
  return `(${match[1]}) ${match[2]}-${match[3]}`
}
