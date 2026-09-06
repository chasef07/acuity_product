import { formatUSPhone } from "../phone.ts"

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
  providerTermination?: string
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
  offerKind?: "INBOUND_OFFER" | "STAFF_TRANSFER"
  staffTransferId?: string
  originatorEmail?: string
  handoffNote?: string
}

export type CallingCardSnapshot = {
  pendingCall?: CallingCardCall
  activeCall?: CallingCardCall
  pendingDisposition?: CallingCardCall
  offers: readonly CallingCardOffer[]
  staffTransfers: ReadonlyArray<{
    id: string
    callId: string
    sourceCallLegId: string
    recipientEmail: string
    state: "REQUESTED" | "ACCEPTED" | "COMPLETED" | "DECLINED" | "EXPIRED" | "CANCELED" | "FAILED"
  }>
  transferCandidates: ReadonlyArray<{ subject: string; email: string }>
  activeCallLegID: string
  endingCallID: string
  mediaAttachment?: unknown
  muted: boolean
  failure?: CallingCardFailure
  pending?: {
    retry?: boolean
    disposition?: boolean
    transfer?: boolean
  }
  controls: {
    canEnd: boolean
    canMute: boolean
    canKeypad: boolean
    canRetry: boolean
    canDispose: boolean
    canTransfer?: boolean
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
  source?: "refresh" | "readiness" | "media-reconnect"
}

export type CallingCardCallView = {
  shell: "calling-card"
  kind: "call"
  callId: string
  phase: "calling" | "connected" | "ended"
  status: string
  elapsed?: string
  identity: {
    primary: string
    details: string[]
  }
  controls: {
    slots: [CallingCardControl, CallingCardControl, CallingCardControl]
  }
  actions: CallingCardActions
  transfer: {
    pending: boolean
    canStart: boolean
    candidates: Array<{ subject: string; email: string }>
    active?: {
      id: string
      recipientEmail: string
      status: string
      canCancel: boolean
    }
  }
  failure?: CallingCardFailureView
}

export type CallingCardOfferView = {
  shell: "calling-card"
  kind: "offers"
  status: "Incoming call" | "Incoming transfer"
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
    decline?: {
      disabled: boolean
      label: string
    }
  }>
  failure?: CallingCardFailureView
}

export type CallingCardView =
  | CallingCardCallView
  | CallingCardOfferView

export type CallingCardIdentity = {
  primary: string
  details: string[]
}

export type CallingCardFailureView = {
  title:
    | "Calling disconnected"
    | "Calling session expired"
    | "Calling unavailable"
    | "Calling is open elsewhere"
    | "Call updates delayed"
    | "Calling service unavailable"
    | "Calling request failed"
    | "Calling reconnecting"
  message: string
  action?: {
    kind: "reload-page" | "recover"
    label: "Refresh page" | "Use this browser"
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
    label: "Dismiss"
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
    if (activeOffers.length === 0) return undefined
    return {
      shell: "calling-card",
      kind: "offers",
      status: activeOffers.some((offer) => offer.offerKind === "STAFF_TRANSFER")
        ? "Incoming transfer"
        : "Incoming call",
      trayLabel: "Incoming calls",
      ...(snapshot.failure
        ? { failure: projectCallingFailure(snapshot.failure) }
        : {}),
      offers: activeOffers.map((offer) => {
        const phone = formatUSPhone(offer.phone) || "Phone unavailable"
        const identity = callIdentity(offer, "INBOUND")
        if (offer.offerKind === "STAFF_TRANSFER") {
          if (offer.originatorEmail) {
            identity.details.push(`From ${offer.originatorEmail}`)
          }
          if (offer.handoffNote) identity.details.push(offer.handoffNote)
        }
        return {
          callId: offer.callId,
          callLegId: offer.callLegId,
          identity,
          countdown: `${secondsRemaining(offer.deadline, _now)}s`,
          countdownLabel: `Incoming offer countdown for ${phone}`,
          answer: {
            eligible:
              offer.answerReady && !Boolean(snapshot.pending?.transfer),
            label: `Answer ${phone}`,
          },
          ...(offer.offerKind === "STAFF_TRANSFER"
            ? {
                decline: {
                  disabled: Boolean(snapshot.pending?.transfer),
                  label: `Decline transfer from ${offer.originatorEmail || "staff"}`,
                },
              }
            : {}),
        }
      }),
    }
  }

  const ending =
    !isOutcomeState(call.state) &&
    (snapshot.endingCallID === call.id || call.endRequested)

  const activeTransfer = snapshot.staffTransfers.find(
    (transfer) =>
      transfer.callId === call.id &&
      transfer.sourceCallLegId === snapshot.activeCallLegID &&
      (transfer.state === "REQUESTED" || transfer.state === "ACCEPTED"),
  )
  return {
    shell: "calling-card",
    kind: "call",
    callId: call.id,
    phase: isOutcomeState(call.state)
      ? "ended"
      : call.state === "CONNECTED" ? "connected" : "calling",
    status: callStatus(call),
    ...(call.state === "CONNECTED"
      ? { elapsed: elapsedTime(call.connectedAt, _now) }
      : {}),
    identity: callIdentity(call, call.direction),
    controls: {
      slots: [
        {
          kind: "mute",
          visible: call.state === "CONNECTED",
          disabled: ending || !snapshot.controls.canMute,
          label: snapshot.muted ? "Unmute" : "Mute",
        },
        {
          kind: "end",
          visible: snapshot.controls.canEnd || ending,
          disabled: ending || !snapshot.controls.canEnd,
          label: ending ? "Ending…" : (call.state === "CONNECTED" || call.connectedAt) ? "End call" : "Cancel call",
        },
        {
          kind: "keypad",
          visible: call.state === "CONNECTED",
          disabled: ending || !snapshot.controls.canKeypad,
          label: "Keypad",
        },
      ],
    },
    actions: projectActions(call, snapshot),
    transfer: {
      pending: Boolean(snapshot.pending?.transfer),
      canStart: Boolean(snapshot.controls.canTransfer) && !ending,
      candidates: [...snapshot.transferCandidates],
      ...(activeTransfer
        ? {
            active: {
              id: activeTransfer.id,
              recipientEmail: activeTransfer.recipientEmail,
              status:
                activeTransfer.state === "ACCEPTED"
                  ? "Staff answered · confirming transfer"
                  : `Ringing ${activeTransfer.recipientEmail}`,
              canCancel: !ending,
            },
          }
        : {}),
    },
    ...(snapshot.failure
      ? { failure: projectCallingFailure(snapshot.failure) }
      : {}),
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
        label: snapshot.pending?.retry ? "Calling…" : "Call again",
        disabled: Boolean(snapshot.pending?.retry),
      }
    : undefined
  const close =
    settledOutcomeCanClose(call.state) &&
    !snapshot.mediaAttachment &&
    !snapshot.pending?.retry &&
    !snapshot.pending?.disposition
      ? { label: "Dismiss" as const }
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
      { outcome: "RESOLVED", label: "Resolved on call", primary: true },
      {
        outcome: "FOLLOW_UP_REQUIRED",
        label: "Create follow-up task",
        primary: false,
      },
    ]
  }
  if (call.entryPoint === "TASK") {
    return [
      { outcome: "COMPLETE_TASK", label: "Complete task", primary: true },
      { outcome: "KEEP_OPEN", label: "Keep task open", primary: false },
    ]
  }
  if (call.connectedAt) {
    return [
      { outcome: "RESOLVED", label: "Resolved on call", primary: true },
      { outcome: "CREATE_TASK", label: "Create follow-up task", primary: false },
    ]
  }
  return [
    { outcome: "NO_FOLLOW_UP", label: "No follow-up", primary: true },
    { outcome: "CREATE_TASK", label: "Create task", primary: false },
  ]
}

export function projectCallingFailure(
  failure: CallingCardFailure,
): CallingCardFailureView {
  const copy = failureCopy(failure)
  return {
    ...copy,
    ...(failure.recoverable &&
      failure.kind !== "access" &&
      failure.kind !== "temporary-request" &&
      failure.kind !== "conflict" &&
      failure.source !== "media-reconnect"
      ? {
          action: {
            kind:
              failure.kind === "ownership"
                ? ("recover" as const)
                : ("reload-page" as const),
            label:
              failure.kind === "ownership"
                ? ("Use this browser" as const)
                : ("Refresh page" as const),
          },
        }
      : {}),
  }
}

function failureCopy(failure: CallingCardFailure) {
  switch (failure.kind) {
    case "authentication":
      return {
        title: "Calling session expired" as const,
        message: "Refresh the page to reconnect. You may need to sign in again.",
      }
    case "access":
      return {
        title: "Calling unavailable" as const,
        message: "Your account doesn’t have access to calling for this practice.",
      }
    case "ownership":
      return {
        title: "Calling is open elsewhere" as const,
        message: failure.recoverable
          ? "Calling is connected in another browser. Use this browser instead."
          : "A call is active in another browser. Finish it there before using this browser.",
      }
    case "technical-readiness":
    case "media":
      if (failure.source === "media-reconnect") {
        return {
          title: "Calling reconnecting" as const,
          message: "Calling is reconnecting automatically. Keep this page open.",
        }
      }
      return {
        title: "Calling disconnected" as const,
        message: "Refresh the page to reconnect. Calls are paused until then.",
      }
    case "temporary-request":
      if (failure.source === "refresh") {
        return {
          title: "Call updates delayed" as const,
          message: "The latest call status could not be loaded. Retrying automatically.",
        }
      }
      if (failure.source === "readiness") {
        return {
          title: "Calling service unavailable" as const,
          message: "Calling availability could not be confirmed. Retrying automatically.",
        }
      }
      return {
        title: "Calling request failed" as const,
        message: failure.recoverable
          ? "Your request could not be confirmed. Try the action again."
          : "Your request could not be confirmed.",
      }
    case "conflict":
      return {
        title: "Calling request failed" as const,
        message: "The call or calling session changed. Try the action again.",
      }
  }
}

function callIdentity(
  call: Pick<
    CallingCardCall | CallingCardOffer,
    "displayName" | "phone" | "locationName" | "transferReason"
  >,
  direction: CallingCardCall["direction"],
): CallingCardIdentity {
  const phone = formatUSPhone(call.phone)
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

function callStatus(call: CallingCardCall) {
  switch (call.state) {
    case "CONNECTED": return "Connected"
    case "UNANSWERED":
      if (call.endRequested) return "Call ended"
      switch (call.providerTermination) {
        case "BUSY": return "Line busy"
        case "DECLINED": return "Call declined"
        case "FAILED":
        case "MEDIA_READINESS_FAILED": return "Call couldn’t connect"
        default: return "No answer"
      }
    case "MISSED": return "Missed call"
    case "VOICEMAIL": return "Voicemail"
    case "VOICEMAIL_GREETING": return "Voicemail greeting"
    case "VOICEMAIL_RECORDING": return "Recording voicemail"
    case "NEEDS_DISPOSITION":
    case "RESOLVED":
    case "FOLLOW_UP_REQUIRED": return "Call ended"
    default: return call.direction === "OUTBOUND" ? "Calling…" : "Connecting…"
  }
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
