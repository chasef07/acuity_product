import { isSettledCallState } from "./call-state.ts"
import type {
  CallingCall,
  CallingDispositionRequest,
  CallingDispositionResult,
  CallingReadinessRequest,
  CallingState,
  RingingCallLeg,
  SoftphoneState,
  StaffTransfer,
  StaffTransferCandidate,
  StartOutboundCallRequest,
} from "../api/generated/types.gen.ts"
import {
  browserCallingHints,
  type CallingHintSignal,
  type CallingHintSource,
} from "./calling-hints.ts"
import { createCallingChangeMetric } from "./calling-change-metric.ts"
import {
  mediaKey,
  type CallingMediaAdapter,
  type IncomingMediaLeg,
  type MediaFailure,
  type MediaKey,
  type MediaState,
} from "./media-adapter.ts"

export type SoftphoneFailureKind =
  | "authentication"
  | "access"
  | "ownership"
  | "technical-readiness"
  | "media"
  | "conflict"
  | "temporary-request"

type SoftphoneFailure = {
  kind: SoftphoneFailureKind
  message: string
  recoverable: boolean
  source?: "refresh" | "readiness" | "media-reconnect"
}

export type RuntimeOffer = RingingCallLeg & { answerReady: boolean }

type RuntimePendingCall = Pick<
  CallingCall,
  | "id"
  | "direction"
  | "entryPoint"
  | "state"
  | "displayName"
  | "phone"
  | "locationName"
  | "transferReason"
  | "connectedAt"
  | "retryAllowed"
  | "endRequested"
> & Partial<Pick<CallingCall, "practiceId" | "locationId">>

export type RuntimeMediaCorrelation = {
  callID: string
  callLegID?: string
  providerLegID: string
  mediaToken: string
}

type AnsweredLeg = {
  callID: string
  callLegID: string
  media: MediaKey
}

type CallPointer =
  | { stage: "idle" }
  | { stage: "dialing"; pending: RuntimePendingCall }
  | { stage: "answering"; pending?: RuntimePendingCall; answered?: AnsweredLeg }
  | {
      stage: "tracking"
      callID: string
      legID: string
      call?: CallingCall
      pending?: RuntimePendingCall
      answered?: AnsweredLeg
      ending: boolean
      committed: boolean
    }
  | { stage: "outcome"; call: CallingCall }
  | { stage: "disposition"; call: CallingCall }

type TrackingCall = Extract<CallPointer, { stage: "tracking" }>

const idleCall: CallPointer = { stage: "idle" }

export type SoftphoneRuntimeSnapshot = {
  phase: "stopped" | "starting" | "running"
  lease?: SoftphoneState
  availabilityIntent: boolean
  readiness: {
    mediaState: MediaState
    microphoneReady: boolean
    audioReady: boolean
    sessionHealthy: boolean
  }
  offers: RuntimeOffer[]
  staffTransfers: StaffTransfer[]
  transferCandidates: StaffTransferCandidate[]
  pendingCall?: RuntimePendingCall
  activeCall?: CallingCall
  pendingDisposition?: CallingCall
  expectedCallID: string
  activeCallLegID: string
  expectedMedia?: Pick<RingingCallLeg, "callId" | "callLegId" | "mediaToken">
  terminalVersions: Readonly<Record<string, number>>
  recoveryMedia?: RuntimeMediaCorrelation
  mediaAttachment?: RuntimeMediaCorrelation
  muted: boolean
  endingCallID: string
  committedOwner: boolean
  pending: {
    availability: boolean
    retry: boolean
    disposition: boolean
    transfer: boolean
  }
  occupied: boolean
  controls: {
    canEnd: boolean
    canMute: boolean
    canKeypad: boolean
    canRetry: boolean
    canDispose: boolean
    canTransfer: boolean
  }
  failure?: SoftphoneFailure
}

type SoftphoneModel = Omit<
  SoftphoneRuntimeSnapshot,
  | "pending"
  | "occupied"
  | "controls"
  | "failure"
  | keyof ReturnType<typeof callView>
> & { call: CallPointer }

type RuntimeSnapshot = SoftphoneRuntimeSnapshot & { call: CallPointer }

type OperationSlot =
  | "session"
  | "lease"
  | "ownership"
  | "access"
  | "refresh"
  | "readiness"
  | "media"
  | "attach"
  | "answer"
  | "detail"
  | "outbound"
  | "hangup"
  | "retry"
  | "disposition"
  | "transfer"

const operationFences = {
  session: "session",
  lease: "session",
  ownership: "session",
  access: "session",
  refresh: "session",
  readiness: "session",
  media: "lease",
  attach: "call",
  answer: "answer",
  detail: "call",
  outbound: "lease",
  hangup: "lease",
  retry: "lease",
  disposition: "lease",
  transfer: "leg",
} as const satisfies Record<
  OperationSlot,
  "session" | "lease" | "call" | "leg" | "answer"
>

const failurePriority: readonly OperationSlot[] = [
  "access",
  "lease",
  "ownership",
  "media",
  "attach",
  "answer",
  "outbound",
  "hangup",
  "retry",
  "disposition",
  "transfer",
  "readiness",
  "refresh",
]

const resolvedByHealthyReadiness: readonly OperationSlot[] = [
  "media",
  "attach",
  "answer",
  "outbound",
  "hangup",
  "retry",
  "disposition",
  "transfer",
]

type Operation = {
  readonly slot: OperationSlot
  identity: string
  pending: boolean
  closed: boolean
  failure?: SoftphoneFailure
}

type StateRead =
  | { status: "not-modified"; etag?: string }
  | { status: "modified"; state: CallingState; etag?: string }

export interface SoftphoneBackend {
  acquireLease(input: {
    sessionID: string
    takeover: boolean
  }, signal?: AbortSignal): Promise<SoftphoneState>
  writeReadiness(
    input: CallingReadinessRequest,
    signal?: AbortSignal,
  ): Promise<SoftphoneState>
  issueMediaToken(
    input: { sessionID: string },
    signal?: AbortSignal,
  ): Promise<string>
  readState(
    input: { etag?: string; changeReport?: string },
    signal?: AbortSignal,
  ): Promise<StateRead>
  readCall(callID: string, signal?: AbortSignal): Promise<CallingCall>
  confirmMedia(input: {
    callID: string
    sessionID: string
    mediaToken: string
  }, signal?: AbortSignal): Promise<CallingCall>
  startOutbound(
    input: StartOutboundCallRequest,
    signal?: AbortSignal,
  ): Promise<CallingCall>
  hangup(
    input: { callID: string; sessionID: string },
    signal?: AbortSignal,
  ): Promise<CallingCall>
  retry(input: {
    callID: string
    sessionID: string
    idempotencyKey: string
  }, signal?: AbortSignal): Promise<CallingCall>
  dispose(input: {
    callID: string
    sessionID: string
    outcome: CallingDispositionRequest["outcome"]
  }, signal?: AbortSignal): Promise<CallingDispositionResult>
  listTransferCandidates(input: {
    callID: string
    sessionID: string
  }, signal?: AbortSignal): Promise<StaffTransferCandidate[]>
  requestTransfer(input: {
    callID: string
    sessionID: string
    recipientSubject: string
    idempotencyKey: string
    expectedVersion: number
    handoffNote?: string
  }, signal?: AbortSignal): Promise<StaffTransfer>
  cancelTransfer(input: {
    transferID: string
    sessionID: string
  }, signal?: AbortSignal): Promise<StaffTransfer>
  declineTransfer(input: {
    transferID: string
    sessionID: string
  }, signal?: AbortSignal): Promise<StaffTransfer>
}

export interface SoftphoneClock {
  now: number
  setTimeout(callback: () => void, milliseconds: number): number
  clearTimeout(id: number): void
}

export interface SoftphoneMicrophone {
  start(
    onUnavailable: () => void,
    signal?: AbortSignal,
  ): Promise<{ stop(): void }>
}

interface SoftphoneVisibility {
  isHidden(): boolean
  subscribe(listener: () => void): () => void
}

export interface SoftphoneAttention {
  startRingtone(): () => void
  showIncomingNotification(offer: RuntimeOffer): () => void
}

export type SoftphoneRuntime = {
  getSnapshot(): SoftphoneRuntimeSnapshot
  subscribe(listener: () => void): () => void
  start(): Promise<void>
  stop(): Promise<void>
  signalRefresh(): Promise<void>
  setAvailability(available: boolean): Promise<void>
  startOutbound(
    input: Omit<StartOutboundCallRequest, "sessionId">,
  ): Promise<SoftphoneFailure | undefined>
  answer(callLegID: string): Promise<void>
  loadTransferCandidates(): Promise<void>
  requestTransfer(
    recipientSubject: string,
    handoffNote: string,
    idempotencyKey: string,
  ): Promise<void>
  cancelTransfer(): Promise<void>
  declineTransfer(callLegID: string): Promise<void>
  hangup(): Promise<void>
  retry(idempotencyKey: string): Promise<void>
  recover(): Promise<void>
  dismissOutcome(): void
  dispose(outcome: CallingDispositionRequest["outcome"]): Promise<CallingDispositionResult | undefined>
  toggleMute(): void
  sendDTMF(digit: string): boolean
}

type RuntimeOptions = {
  sessionID: string
  backend: SoftphoneBackend
  media: CallingMediaAdapter
  microphone: SoftphoneMicrophone
  remoteElementID?: string
  availabilityIntent?: boolean
  persistAvailabilityIntent?: (available: boolean) => void
  loadMediaCorrelation?: () => RuntimeMediaCorrelation | undefined
  persistMediaCorrelation?: (
    correlation: RuntimeMediaCorrelation | undefined,
  ) => void
  loadCallRecoveryID?: () => string | undefined
  persistCallRecoveryID?: (callID: string | undefined) => void
  attention?: SoftphoneAttention
  clock?: SoftphoneClock
  visibility?: SoftphoneVisibility
  callingHints?: CallingHintSource
}

const defaultClock: SoftphoneClock = {
  get now() {
    return Date.now()
  },
  setTimeout: (callback, milliseconds) => window.setTimeout(callback, milliseconds),
  clearTimeout: (id) => window.clearTimeout(id),
}

const backendRequestTimeoutMilliseconds = 5_000
const mediaOperationTimeoutMilliseconds = 10_000
const mediaConfirmationRetryMilliseconds = 250
const mediaCorrelationWindowMilliseconds = 5_000
const idleRefreshMilliseconds = 4_000
const hiddenRefreshMilliseconds = 8_000
const hintedIdleRefreshMilliseconds = 10_000
const callingHintSpacingMilliseconds = 500
const fastRefreshMilliseconds = 250
const heartbeatMinimumMilliseconds = 3_500
const heartbeatMaximumMilliseconds = 4_000
const mediaEffectTimeoutMessage =
  "Call audio did not respond. Reconnect calling and try again."

const browserVisibility: SoftphoneVisibility = {
  isHidden: () => document.hidden,
  subscribe(listener) {
    document.addEventListener("visibilitychange", listener)
    return () => document.removeEventListener("visibilitychange", listener)
  },
}

export class SoftphoneAdapterError extends Error {
  readonly kind: SoftphoneFailureKind
  readonly retryable: boolean

  constructor(
    kind: SoftphoneFailureKind,
    message: string,
    retryable = kind === "temporary-request",
  ) {
    super(message)
    this.kind = kind
    this.retryable = retryable
  }
}

export function createSoftphoneRuntime(options: RuntimeOptions): SoftphoneRuntime {
  const clock = options.clock ?? defaultClock
  const visibility = options.visibility ?? browserVisibility
  const callingHints = options.callingHints ?? browserCallingHints
  const callingChanges = createCallingChangeMetric()
  const heartbeatDelayMilliseconds = heartbeatDelay(options.sessionID)
  const listeners = new Set<() => void>()
  const incomingMedia = new Map<string, IncomingMediaLeg>()
  const incomingMediaDeadlines = new WeakMap<IncomingMediaLeg, number>()
  const authoritativeRingingMedia = new Map<string, number>()
  const confirmationWaits = new Map<number, () => void>()
  const rejectedMedia = new WeakMap<IncomingMediaLeg, Promise<boolean>>()
  const backendRequests = new Set<AbortController>()
  const mediaEffectControllers = new Set<AbortController>()
  const notificationStops = new Map<string, () => void>()
  let stopRingtone: (() => void) | undefined
  let microphone: { stop(): void } | undefined
  let refreshTimer: number | undefined
  let refreshDueAt = 0
  let lastRefreshStartedAt = Number.NEGATIVE_INFINITY
  let callingHintPending = false
  let callingPracticeIDs: string[] | undefined
  let heartbeatTimer: number | undefined
  let leaseReclaim: Promise<void> | undefined
  let refreshInFlight: Promise<void> | undefined
  let refreshQueued = false
  let stopped = true
  let stopInFlight: Promise<void> | undefined
  let etag: string | undefined
  let temporaryFailures = 0
  let heartbeatFailures = 0
  let readinessInFlight: Promise<void> | undefined
  let requestedReadiness:
    | { body: CallingReadinessRequest; operation: Operation }
    | undefined
  const readinessCommits = new Set<{ confirmed: boolean }>()
  let readinessFailClosed = false
  let accessFailureClosing = false
  let accessBlocked = false
  let unsubscribeVisibility: (() => void) | undefined
  let unsubscribeCallingHints: (() => void) | undefined
  let mediaConnectInFlight: Promise<void> | undefined
  let mediaConnectController: AbortController | undefined
  let mediaReleaseInFlight: Promise<void> | undefined
  let mediaFailureInFlight: Promise<void> | undefined
  let attachedLeg: IncomingMediaLeg | undefined
  const ignoredCallIDs = new Set<string>()
  const restoredMedia = options.loadMediaCorrelation?.()
  let recoveryCallID = options.loadCallRecoveryID?.() ?? ""
  const operations = new Map<OperationSlot, Operation>()
  let snapshot: RuntimeSnapshot = derive({
    phase: "stopped",
    availabilityIntent: options.availabilityIntent ?? false,
    readiness: {
      mediaState: "unavailable",
      microphoneReady: false,
      audioReady: false,
      sessionHealthy: false,
    },
    offers: [],
    staffTransfers: [],
    transferCandidates: [],
    call: recoveryCallID
      ? trackCall(
          idleCall,
          recoveryCallID,
          restoredMedia?.callID === recoveryCallID
            ? (restoredMedia.callLegID ?? "")
            : "",
        )
      : idleCall,
    terminalVersions: {},
    recoveryMedia: restoredMedia,
    muted: false,
  }, operations)

  function publish(update: Partial<SoftphoneModel>, anchor?: Operation) {
    const model = { ...snapshot, ...update }
    if (anchor && operations.get(anchor.slot) === anchor) {
      anchor.identity = fenceIdentity(anchor.slot, model)
    }
    for (const operation of operations.values()) {
      if (operation.identity !== fenceIdentity(operation.slot, model)) {
        operations.delete(operation.slot)
      }
    }
    snapshot = derive(model, operations)
    for (const listener of listeners) listener()
  }

  function fenceIdentity(slot: OperationSlot, model: SoftphoneModel) {
    const fence = operationFences[slot]
    if (fence === "session") return ""
    const owner = model.lease?.owner === true
    if (fence === "lease") return JSON.stringify([owner])
    const call = model.call
    if (fence === "answer") {
      const answered = answeredLeg(call)
      return JSON.stringify([owner, answered?.callID, answered?.media])
    }
    const subject =
      call.stage === "tracking"
        ? [call.stage, call.callID]
        : call.stage === "outcome" || call.stage === "disposition"
          ? [call.stage, call.call.id]
          : call.stage === "idle"
            ? []
            : ["pending", call.pending?.id]
    if (fence === "call") return JSON.stringify([owner, ...subject])
    return JSON.stringify([
      owner,
      ...subject,
      call.stage === "tracking" ? call.legID : "",
    ])
  }

  function open(
    slot: OperationSlot,
    options: { pending?: boolean; keepFailure?: boolean } = {},
    update: Partial<SoftphoneModel> = {},
  ) {
    const previous = operations.get(slot)
    const operation: Operation = {
      slot,
      identity: fenceIdentity(slot, snapshot),
      pending: options.pending ?? false,
      closed: false,
      failure: options.keepFailure ? previous?.failure : undefined,
    }
    operations.delete(slot)
    operations.set(slot, operation)
    if (
      Object.keys(update).length > 0 ||
      previous?.pending !== operation.pending ||
      previous?.failure !== operation.failure
    ) {
      publish(update, operation)
    }
    return operation
  }

  function current(operation: Operation | undefined): operation is Operation {
    return Boolean(operation && operations.get(operation.slot) === operation)
  }

  function live(operation: Operation | undefined): operation is Operation {
    return current(operation) && !operation.closed
  }

  function close(operation: Operation | undefined) {
    if (!operation) return
    operation.closed = true
    operation.pending = false
  }

  function settle(operation: Operation, update: Partial<SoftphoneModel> = {}) {
    const visible =
      current(operation) && (operation.pending || Boolean(operation.failure))
    if (current(operation)) operations.delete(operation.slot)
    if (visible || Object.keys(update).length > 0) publish(update)
  }

  function fail(
    operation: Operation,
    failure: SoftphoneFailure,
    update: Partial<SoftphoneModel> = {},
  ) {
    if (!current(operation)) return
    operation.failure = failure
    operation.pending = false
    publish(update, operation)
  }

  function report(
    slot: OperationSlot,
    failure: SoftphoneFailure | undefined,
    update: Partial<SoftphoneModel> = {},
  ) {
    operations.delete(slot)
    if (failure) {
      operations.set(slot, {
        slot,
        identity: fenceIdentity(slot, { ...snapshot, ...update }),
        pending: false,
        closed: true,
        failure,
      })
    }
    publish(update)
  }

  function reportLease(
    lease: SoftphoneState,
    ownership: SoftphoneFailure | undefined,
    update: Partial<SoftphoneModel>,
  ) {
    if (operations.get("lease")?.failure) operations.delete("lease")
    report(
      "ownership",
      accessBlocked ? operations.get("ownership")?.failure : ownership,
      { ...update, lease },
    )
  }

  function clearFailures(slots: readonly OperationSlot[] = [...operations.keys()]) {
    for (const slot of slots) {
      const operation = operations.get(slot)
      if (!operation) continue
      operation.failure = undefined
      if (operation.closed) operations.delete(slot)
    }
  }

  function backendRequest<T>(
    request: (signal: AbortSignal) => Promise<T>,
    timeoutMilliseconds = backendRequestTimeoutMilliseconds,
  ) {
    const controller = new AbortController()
    backendRequests.add(controller)
    let timeoutID: number | undefined
    const result = new Promise<T>((resolve, reject) => {
      const rejectUnavailable = () =>
        reject(
          new SoftphoneAdapterError(
            "temporary-request",
            "Calling could not reach the service. Check your connection and try again.",
            true,
          ),
        )
      controller.signal.addEventListener("abort", rejectUnavailable, {
        once: true,
      })
      timeoutID = clock.setTimeout(() => {
        controller.abort()
      }, timeoutMilliseconds)
      void Promise.resolve()
        .then(() => request(controller.signal))
        .then(resolve, reject)
    })
    return result.finally(() => {
      backendRequests.delete(controller)
      if (timeoutID !== undefined) clock.clearTimeout(timeoutID)
    })
  }

  function abortableOperation<T>(
    operation: Promise<T>,
    signal: AbortSignal,
    onLateValue?: (value: T) => void,
    timeoutFailure = new SoftphoneAdapterError(
      "technical-readiness",
      "Calling media did not respond. Reconnect calling and try again.",
      true,
    ),
  ) {
    return new Promise<T>((resolve, reject) => {
      let settled = false
      const cleanup = () => signal.removeEventListener("abort", onAbort)
      const onAbort = () => {
        if (settled) return
        settled = true
        cleanup()
        reject(timeoutFailure)
      }
      signal.addEventListener("abort", onAbort, { once: true })
      if (signal.aborted) {
        onAbort()
        void operation.then(onLateValue, () => undefined)
        return
      }
      void operation.then(
        (value) => {
          if (settled) {
            try {
              onLateValue?.(value)
            } catch {
            }
            return
          }
          settled = true
          cleanup()
          resolve(value)
        },
        (error) => {
          if (settled) return
          settled = true
          cleanup()
          reject(error)
        },
      )
    })
  }

  async function disconnectMedia() {
    const controller = new AbortController()
    const timeoutID = clock.setTimeout(
      () => controller.abort(),
      mediaOperationTimeoutMilliseconds,
    )
    try {
      await abortableOperation(
        options.media.disconnect(controller.signal),
        controller.signal,
      )
    } catch {
    } finally {
      clock.clearTimeout(timeoutID)
    }
  }

  async function boundedMediaEffect<T>(
    operation: Promise<T>,
    onLateValue?: (value: T) => void,
  ) {
    const controller = new AbortController()
    mediaEffectControllers.add(controller)
    const timeoutID = clock.setTimeout(
      () => controller.abort(),
      mediaOperationTimeoutMilliseconds,
    )
    try {
      return await abortableOperation(
        operation,
        controller.signal,
        onLateValue,
        new SoftphoneAdapterError(
          "media",
          mediaEffectTimeoutMessage,
          true,
        ),
      )
    } finally {
      mediaEffectControllers.delete(controller)
      clock.clearTimeout(timeoutID)
    }
  }

  function wait(milliseconds: number) {
    return new Promise<void>((resolve) => {
      const id = clock.setTimeout(() => {
        confirmationWaits.delete(id)
        resolve()
      }, milliseconds)
      confirmationWaits.set(id, resolve)
    })
  }

  function clearConfirmationWaits() {
    for (const [id, resolve] of confirmationWaits) {
      clock.clearTimeout(id)
      resolve()
    }
    confirmationWaits.clear()
  }

  function rejectSafely(leg: IncomingMediaLeg) {
    const existing = rejectedMedia.get(leg)
    if (existing) return existing
    const operation = (async () => {
      try {
        await boundedMediaEffect(leg.reject())
        return true
      } catch {
        return false
      }
    })()
    rejectedMedia.set(leg, operation)
    void operation.then((released) => {
      if (!released && rejectedMedia.get(leg) === operation) {
        rejectedMedia.delete(leg)
      }
    })
    return operation
  }

  function answerMediaLeg(leg: IncomingMediaLeg) {
    return boundedMediaEffect(leg.answer(), (lateOutcome) => {
      if (lateOutcome === "attached") void rejectSafely(leg)
    })
  }

  function mediaEffectTimedOut(error: unknown) {
    return (
      error instanceof SoftphoneAdapterError &&
      error.kind === "media" &&
      error.message === mediaEffectTimeoutMessage
    )
  }


  function persistCallRecoveryID(callID: string | undefined) {
    recoveryCallID = callID ?? ""
    options.persistCallRecoveryID?.(callID)
  }

  function hideLeaseAfterAccessFailure(lease: SoftphoneState | undefined) {
    return lease
      ? {
          ...lease,
          owner: false,
          available: false,
          activeCallId: "",
          pendingOutcomeCallId: "",
        }
      : undefined
  }

  async function failClosedAccess(failure: SoftphoneFailure) {
    etag = undefined
    authoritativeRingingMedia.clear()
    persistCallRecoveryID(undefined)
    accessBlocked = true
    readinessFailClosed = true
    if (!accessFailureClosing) {
      accessFailureClosing = true
      queueReadiness(false, true, "silent")
      const closing = readinessInFlight ?? drainReadiness()
      void closing.finally(() => {
        accessFailureClosing = false
      })
    }
    report("access", failure, {
      lease: hideLeaseAfterAccessFailure(snapshot.lease),
      offers: [],
      staffTransfers: [],
      transferCandidates: [],
      call: idleCall,
      expectedMedia: undefined,
      recoveryMedia: persistMediaCorrelation(undefined),
      mediaAttachment: undefined,
      muted: false,
      readiness: {
        mediaState: "unavailable",
        microphoneReady: false,
        audioReady: false,
        sessionHealthy: false,
      },
    })
    await releaseLocalMedia()
  }

  async function failRequest(
    operation: Operation,
    error: unknown,
    fallback: SoftphoneFailureKind = "temporary-request",
    update: Partial<SoftphoneModel> = {},
  ) {
    const failure = failureFrom(error, fallback)
    if (failure.kind === "access") {
      await failClosedAccess(failure)
      return failure
    }
    const source =
      operation.slot === "refresh" || operation.slot === "readiness"
        ? operation.slot
        : undefined
    fail(
      operation,
      failure.kind === "temporary-request" && source
        ? { ...failure, source }
        : failure,
      update,
    )
    return failure
  }

  function ownershipFailure(lease: SoftphoneState, moved = false): SoftphoneFailure {
    if (lease.activeCallId) {
      return {
        kind: "ownership",
        message: "An active Call is using calling in another browser.",
        recoverable: false,
      }
    }
    return {
      kind: "ownership",
      message: moved
        ? "Calling ownership moved to another browser. Reconnect to continue."
        : "Calling is active in another browser. Take over to continue.",
      recoverable: true,
    }
  }

  function syncAttention(offers: RuntimeOffer[]) {
    const activeOfferIDs = new Set(offers.map((offer) => offer.callLegId))
    for (const [callLegID, stop] of notificationStops) {
      if (activeOfferIDs.has(callLegID)) continue
      stop()
      notificationStops.delete(callLegID)
    }
    if (offers.length === 0) {
      stopRingtone?.()
      stopRingtone = undefined
      return
    }
    if (!stopRingtone) stopRingtone = options.attention?.startRingtone()
    if (!visibility.isHidden()) return
    for (const offer of offers) {
      if (notificationStops.has(offer.callLegId)) continue
      const stop = options.attention?.showIncomingNotification(offer)
      if (stop) notificationStops.set(offer.callLegId, stop)
    }
  }

  function purgeAttachedMedia(
    attachment: RuntimeMediaCorrelation,
    leg: IncomingMediaLeg,
  ) {
    const key = mediaKey(leg)
    if (!attachedLeg || mediaKey(attachedLeg) !== key) return
    incomingMedia.delete(attachment.mediaToken)
    const purge = rejectSafely(leg)
    void purge.then((released) => {
      if (!attachedLeg || mediaKey(attachedLeg) !== key) return
      if (!released) {
        void handleMediaFailure("provider")
        return
      }
      attachedLeg = undefined
      const current = snapshot.mediaAttachment
      const detached =
        current?.callID === attachment.callID && mediaKey(current) === key
      const answered =
        answeredLeg(snapshot.call)?.callID === attachment.callID
      if (!detached && !answered) return
      publish({
        call: answered ? withoutAnswered(snapshot.call) : snapshot.call,
        ...(detached ? { mediaAttachment: undefined, muted: false } : {}),
      })
    })
  }

  function applyCall(call: CallingCall) {
    const pointer = snapshot.call
    const current = snapshot.activeCall
    const known = current ?? snapshot.pendingDisposition
    const observed = known?.id === call.id ? known : undefined
    if (ignoredCallIDs.has(call.id)) return false
    if (snapshot.expectedCallID && call.id !== snapshot.expectedCallID) return false
    if ((snapshot.terminalVersions[call.id] ?? -1) >= call.version) return false
    if (
      observed &&
      (call.version < observed.version ||
        (call.version === observed.version &&
          callSettled(observed) &&
          !callSettled(call)))
    ) {
      return false
    }
    if (current && current.id !== call.id && !callSettled(current)) return false
    const settled = callSettled(call)
    const needsDisposition = call.state === "NEEDS_DISPOSITION"
    const closed =
      call.state === "RESOLVED" || call.state === "FOLLOW_UP_REQUIRED"
    const settledMedia =
      settled && snapshot.mediaAttachment?.callID === call.id
        ? snapshot.mediaAttachment
        : undefined
    const settledLeg =
      settledMedia && attachedLeg && mediaKey(attachedLeg) === mediaKey(settledMedia)
        ? attachedLeg
        : undefined
    if (settledMedia && settledLeg) purgeAttachedMedia(settledMedia, settledLeg)
    persistCallRecoveryID(closed ? undefined : call.id)
    const terminalVersions =
      settled
        ? {
            ...snapshot.terminalVersions,
            [call.id]: Math.max(
              snapshot.terminalVersions[call.id] ?? -1,
              call.version,
            ),
          }
        : snapshot.terminalVersions
    const recoveryMedia = settled
      ? persistMediaCorrelation(undefined)
      : snapshot.recoveryMedia
    publish({
      call: needsDisposition
        ? { stage: "disposition", call }
        : closed
          ? pointer.stage === "disposition" && pointer.call.id !== call.id
            ? pointer
            : idleCall
          : settled
            ? { stage: "outcome", call }
            : {
                ...trackCall(
                  pointer,
                  call.id,
                  snapshot.activeCallLegID,
                  snapshot.committedOwner,
                ),
                call,
                pending: undefined,
              },
      terminalVersions,
      recoveryMedia,
      expectedMedia: settled ? undefined : snapshot.expectedMedia,
      mediaAttachment:
        settledMedia && !settledLeg ? undefined : snapshot.mediaAttachment,
      muted: settledMedia && !settledLeg ? false : snapshot.muted,
    })
    return true
  }

  async function discardUnprojectedCall(callID: string) {
    ignoredCallIDs.add(callID)
    persistCallRecoveryID(undefined)
    const losingLegs = new Set<IncomingMediaLeg>()
    if (attachedLeg) losingLegs.add(attachedLeg)
    attachedLeg = undefined
    const mediaTokens = [
      snapshot.expectedMedia?.callId === callID
        ? snapshot.expectedMedia.mediaToken
        : undefined,
      snapshot.recoveryMedia?.callID === callID
        ? snapshot.recoveryMedia.mediaToken
        : undefined,
    ]
    for (const mediaToken of mediaTokens) {
      if (!mediaToken) continue
      const leg = incomingMedia.get(mediaToken)
      if (leg) losingLegs.add(leg)
      incomingMedia.delete(mediaToken)
    }
    const offers = snapshot.offers.filter((offer) => offer.callId !== callID)
    publish({
      offers,
      call: idleCall,
      expectedMedia: undefined,
      recoveryMedia: persistMediaCorrelation(undefined),
      mediaAttachment: undefined,
      muted: false,
    })
    syncAttention(offers)
    await Promise.all([
      releaseLocalMedia(),
      ...[...losingLegs].map(rejectSafely),
    ])
  }

  function expireOffers() {
    const offers = snapshot.offers.filter(
      (offer) => new Date(offer.deadline).getTime() > clock.now,
    )
    if (offers.length === snapshot.offers.length) return
    publish({ offers })
    syncAttention(offers)
  }

  function rejectExpiredUnmatchedMedia(
    correlatedMediaTokens: ReadonlySet<string>,
  ) {
    const expired = [...incomingMedia.values()].filter(
      (leg) =>
        !correlatedMediaTokens.has(leg.mediaToken) &&
        (incomingMediaDeadlines.get(leg) ?? 0) <= clock.now,
    )
    for (const leg of expired) incomingMedia.delete(leg.mediaToken)
    for (const leg of expired) void rejectSafely(leg)
  }

  function rememberAuthoritativeRingingMedia(
    owner: boolean,
    ringing: CallingState["ringing"],
  ) {
    authoritativeRingingMedia.clear()
    if (!owner) return
    for (const leg of ringing) {
      const deadline = new Date(leg.deadline).getTime()
      if (deadline > clock.now) {
        authoritativeRingingMedia.set(leg.mediaToken, deadline)
      }
    }
  }

  function correlatedMediaTokens() {
    const tokens = new Set<string>()
    for (const [mediaToken, deadline] of authoritativeRingingMedia) {
      if (deadline > clock.now) tokens.add(mediaToken)
      else authoritativeRingingMedia.delete(mediaToken)
    }
    if (snapshot.expectedMedia) tokens.add(snapshot.expectedMedia.mediaToken)
    return tokens
  }

  async function refreshOnce() {
    const refresh = open("refresh", { keepFailure: true })
    callingHintPending = false
    lastRefreshStartedAt = clock.now
    expireOffers()
    const changeRead = callingChanges.beginRead(clock.now, {
      hidden: visibility.isHidden(),
      covered: callingHints.covers(callingPracticeIDs),
    })
    let stateRead = false
    try {
      const result = await backendRequest((signal) =>
        options.backend.readState(
          { etag, changeReport: changeRead.report },
          signal,
        ),
      )
      stateRead = true
      if (result.etag) etag = result.etag
      if (!live(refresh)) return
      if (result.status === "not-modified") {
        if (snapshot.expectedCallID) {
          const detail = open("detail")
          const call = await backendRequest((signal) =>
            options.backend.readCall(snapshot.expectedCallID, signal),
          )
          if (live(detail)) applyCall(call)
          if (!live(refresh)) return
        }
        rejectExpiredUnmatchedMedia(correlatedMediaTokens())
        temporaryFailures = 0
        reclaimLapsedLease()
        settle(refresh)
        return
      }
      const state = result.state
      callingChanges.observe(changeRead, state)
      callingPracticeIDs = state.callingPracticeIds
      const authoritativeLease = leaseForSession(
        state.softphone,
        options.sessionID,
      )
      const lease = accessBlocked
        ? hideLeaseAfterAccessFailure(authoritativeLease)!
        : authoritativeLease
      rememberAuthoritativeRingingMedia(lease.owner, state.ringing)
      const ownershipAcquired = snapshot.lease?.owner !== true && lease.owner
      const ownershipLost = snapshot.lease?.owner && !lease.owner
      const ownLeaseLapsed = leaseLapsedForSession(lease, options.sessionID)
      const authoritativeCallID =
        state.bridged?.callId || lease.activeCallId
      let expectedCallID =
        lease.owner
          ? authoritativeCallID || snapshot.expectedCallID
          : ""
      if (ignoredCallIDs.has(expectedCallID)) expectedCallID = ""
      if (
        expectedCallID &&
        snapshot.terminalVersions[expectedCallID] !== undefined
      ) {
        expectedCallID = ""
      }
      const expectedMedia = expectedCallID
        ? state.ringing.find((leg) => leg.callId === expectedCallID)
        : undefined
      const expectedCallProjected = Boolean(
        expectedCallID &&
          (authoritativeCallID === expectedCallID ||
            expectedMedia ||
            state.disposition?.callId === expectedCallID ||
            lease.pendingOutcomeCallId === expectedCallID),
      )
      const retainedInboundCall = [
        snapshot.activeCall,
        snapshot.pendingCall,
        snapshot.pendingDisposition,
      ].some(
        (call) =>
          call?.id === expectedCallID && call.direction === "INBOUND",
      )
      if (
        expectedCallID &&
        !expectedCallProjected &&
        retainedInboundCall &&
        (!answeredLeg(snapshot.call) || state.ringing.length === 0)
      ) {
        await discardUnprojectedCall(expectedCallID)
        if (!stopped && snapshot.lease?.owner) {
          await commitReadiness(false, true)
        }
        if (!live(refresh)) return
        expectedCallID = ""
      }
      let activeCallLegID =
        expectedCallID && state.bridged?.callId === expectedCallID
          ? state.bridged.callLegId
          : snapshot.activeCallLegID
      let answeredLegLost = false
      const answered = answeredLeg(snapshot.call)
      if (answered) {
        const exactBridge =
          state.bridged?.callId === answered.callID &&
          state.bridged.callLegId === answered.callLegID
        const exactDisposition =
          state.disposition?.callId === answered.callID &&
          state.disposition.callLegId === answered.callLegID
        const stillRinging = state.ringing.some(
          (leg) =>
            leg.callId === answered.callID &&
            leg.callLegId === answered.callLegID,
        )
        answeredLegLost = !exactBridge && !exactDisposition && !stillRinging
        if ((exactBridge || exactDisposition) && !stillRinging) {
          const losingLegs = [...incomingMedia.values()].filter(
            (leg) => !exactBridge || mediaKey(leg) !== answered.media,
          )
          incomingMedia.clear()
          void Promise.all(losingLegs.map(rejectSafely))
        }
        if (answeredLegLost) {
          ignoredCallIDs.add(answered.callID)
          const losingLeg = attachedLeg
          const losingAttachment = snapshot.mediaAttachment
          const losingMedia =
            losingLeg &&
            losingAttachment &&
            mediaKey(losingLeg) === mediaKey(losingAttachment)
              ? { attachment: losingAttachment, leg: losingLeg }
              : undefined
          if (!losingMedia) attachedLeg = undefined
          expectedCallID = ""
          publish({
            call: idleCall,
            expectedMedia: undefined,
            recoveryMedia: persistMediaCorrelation(undefined),
            mediaAttachment: losingMedia?.attachment,
            muted: losingMedia ? snapshot.muted : false,
          })
          if (losingMedia) {
            purgeAttachedMedia(losingMedia.attachment, losingMedia.leg)
          } else if (losingLeg) {
            void rejectSafely(losingLeg)
          }
        } else if (exactDisposition) {
          const dispositionAttachment = snapshot.mediaAttachment
          const dispositionLeg = attachedLeg
          if (
            dispositionLeg &&
            dispositionAttachment &&
            mediaKey(dispositionLeg) === mediaKey(dispositionAttachment)
          ) {
            purgeAttachedMedia(dispositionAttachment, dispositionLeg)
          } else {
            attachedLeg = undefined
            publish({
              call: withoutAnswered(snapshot.call),
              mediaAttachment: undefined,
              muted: false,
            })
          }
        }
      }
      const ownershipMoved = Boolean(
        lease.owner &&
          snapshot.committedOwner &&
          snapshot.activeCall?.id === expectedCallID &&
          snapshot.staffTransfers.some(
            (transfer) =>
              transfer.callId === expectedCallID &&
              transfer.sourceCallLegId === snapshot.activeCallLegID,
          ) &&
          !expectedCallProjected,
      )
      if (ownershipMoved) {
        expectedCallID = ""
        activeCallLegID = ""
        persistCallRecoveryID(undefined)
      }
      const offers =
        lease.owner &&
        !expectedCallID &&
        !snapshot.pendingCall &&
        !snapshot.activeCall &&
        !snapshot.pendingDisposition
        ? offersWithMedia(
            state.ringing.filter(
              (offer) => new Date(offer.deadline).getTime() > clock.now,
            ),
          )
        : []
      reportLease(lease, !lease.owner && !ownLeaseLapsed
        ? ownershipFailure(lease, ownershipLost)
        : undefined, {
        offers,
        staffTransfers: lease.owner ? state.staffTransfers : [],
        transferCandidates:
          lease.owner && !ownershipMoved ? snapshot.transferCandidates : [],
        call:
          !lease.owner || ownershipMoved
            ? idleCall
            : expectedCallID
              ? trackCall(
                  snapshot.call,
                  expectedCallID,
                  activeCallLegID,
                  state.bridged?.callId === expectedCallID &&
                    state.bridged.callLegId === activeCallLegID,
                )
              : snapshot.call.stage === "tracking"
                ? idleCall
                : snapshot.call,
        expectedMedia: lease.owner ? expectedMedia : undefined,
        recoveryMedia: lease.owner
          ? snapshot.recoveryMedia
          : persistMediaCorrelation(undefined),
      })
      syncAttention(offers)
      if (ownershipLost || ownershipMoved) await releaseLocalMedia()
      if (!live(refresh)) return
      reclaimLapsedLease()
      if (ownershipMoved && !stopped && lease.owner) void connectMedia()
      const pendingID = lease.owner
        ? lease.pendingOutcomeCallId || state.disposition?.callId
        : ""
      if (pendingID) {
        const detail = open("detail")
        const pending = await backendRequest((signal) =>
          options.backend.readCall(pendingID, signal),
        )
        if (live(detail) && pending.state === "NEEDS_DISPOSITION") {
          applyCall(pending)
        }
        if (!live(refresh)) return
      } else if (snapshot.pendingDisposition) {
        publish({ call: idleCall })
      }
      if (expectedCallID && !answeredLegLost) {
        const detail = open("detail")
        const call = await backendRequest((signal) =>
          options.backend.readCall(expectedCallID, signal),
        )
        if (!live(refresh)) return
        if (live(detail)) {
          if (
            !expectedCallProjected &&
            (call.direction === "INBOUND" || call.state === "CONNECTED")
          ) {
            await discardUnprojectedCall(call.id)
            if (!stopped && snapshot.lease?.owner) {
              if (call.direction === "INBOUND") {
                await commitReadiness(false, true)
              } else {
                void connectMedia()
              }
            }
          } else {
            applyCall(call)
          }
        }
      }
      const queuedExpectedLeg = snapshot.expectedMedia
        ? incomingMedia.get(snapshot.expectedMedia.mediaToken)
        : undefined
      if (queuedExpectedLeg && !attachedLeg) {
        await attachExpectedMedia(queuedExpectedLeg)
      }
      rejectExpiredUnmatchedMedia(correlatedMediaTokens())
      if (ownershipAcquired) {
        void connectMedia().then(() => scheduleHeartbeat())
      }
      temporaryFailures = 0
      settle(refresh)
    } catch (error) {
      if (!stateRead) callingChanges.readFailed(changeRead)
      if (!live(refresh)) return
      temporaryFailures += 1
      await failRequest(refresh, error)
    }
  }

  function requestRefresh(queueIfBusy: boolean): Promise<void> {
    if (stopped) return Promise.resolve()
    if (refreshInFlight) {
      if (queueIfBusy) refreshQueued = true
      return refreshInFlight.then(() => {
        if (!stopped && refreshQueued) return requestRefresh(false)
      })
    }
    const operation = (async () => {
      do {
        refreshQueued = false
        await refreshOnce()
      } while (!stopped && refreshQueued)
    })()
    const inFlight = operation.finally(() => {
      if (refreshInFlight === inFlight) refreshInFlight = undefined
    })
    refreshInFlight = inFlight
    return inFlight
  }

  function currentRefreshDelay() {
    if (temporaryFailures) {
      return temporaryRetryDelay(
        temporaryFailures,
        visibility.isHidden() ? 12_000 : 8_000,
      )
    }
    if (incomingMedia.size > 0) return mediaConfirmationRetryMilliseconds
    return refreshDelay(
      snapshot,
      visibility.isHidden(),
      callingHints.covers(callingPracticeIDs),
    )
  }

  function callingHintDelay() {
    return Math.max(
      0,
      lastRefreshStartedAt + callingHintSpacingMilliseconds - clock.now,
    )
  }

  function callingHintsCanAdvance(delay: number) {
    return !temporaryFailures && delay > fastRefreshMilliseconds
  }

  function scheduleRefresh() {
    if (stopped) return
    const delay = currentRefreshDelay()
    armRefresh(
      callingHintPending && callingHintsCanAdvance(delay)
        ? Math.min(delay, callingHintDelay())
        : delay,
    )
  }

  function armRefresh(delay: number) {
    if (refreshTimer !== undefined) clock.clearTimeout(refreshTimer)
    refreshDueAt = clock.now + delay
    refreshTimer = clock.setTimeout(() => {
      refreshTimer = undefined
      void requestRefresh(false).finally(scheduleRefresh)
    }, delay)
  }

  function advanceRefresh(delay: number) {
    if (stopped || refreshTimer === undefined) return
    if (refreshDueAt <= clock.now + delay) return
    armRefresh(delay)
  }

  function handleCallingHint(signal: CallingHintSignal) {
    if (stopped) return
    callingChanges.signal(signal, clock.now)
    if (signal === "disconnected") {
      advanceRefresh(currentRefreshDelay())
      return
    }
    if (!callingHintsCanAdvance(currentRefreshDelay())) return
    callingHintPending = true
    advanceRefresh(callingHintDelay())
  }

  function scheduleHeartbeat(delay = heartbeatDelayMilliseconds) {
    if (stopped || accessBlocked || !snapshot.lease?.owner) return
    if (heartbeatTimer !== undefined) clock.clearTimeout(heartbeatTimer)
    heartbeatTimer = clock.setTimeout(() => {
      heartbeatTimer = undefined
      void (async () => {
        if (
          snapshot.availabilityIntent &&
          snapshot.readiness.mediaState === "unavailable"
        ) {
          await connectMedia()
        }
        await commitReadiness(true, false, "silent")
      })().finally(() => {
        heartbeatFailures =
          operations.get("readiness")?.failure?.kind === "temporary-request"
            ? heartbeatFailures + 1
            : 0
        scheduleHeartbeat(
          heartbeatFailures
            ? temporaryRetryDelay(heartbeatFailures, 8_000)
            : heartbeatDelayMilliseconds,
        )
      })
    }, delay)
  }

  function queueReadiness(
    permitAvailability: boolean,
    forceUnregistered = false,
    pendingVisibility: "visible" | "silent" = "visible",
  ) {
    const previous = operations.get("readiness")
    const technicallyReady =
      !forceUnregistered &&
      snapshot.lease?.owner === true &&
      snapshot.readiness.mediaState === "ready" &&
      snapshot.readiness.microphoneReady &&
      snapshot.readiness.audioReady &&
      snapshot.readiness.sessionHealthy
    const occupied = Boolean(
      snapshot.pendingCall ||
        snapshot.activeCall ||
        snapshot.expectedCallID ||
        snapshot.mediaAttachment ||
        snapshot.pendingDisposition,
    )
    const body = {
      sessionId: options.sessionID,
      registered: technicallyReady,
      microphoneReady: technicallyReady,
      audioReady: technicallyReady,
      sessionHealthy: technicallyReady,
      available:
        permitAvailability &&
        !readinessFailClosed &&
        snapshot.availabilityIntent &&
        technicallyReady &&
        !occupied,
    }
    const operation = open("readiness", {
      pending:
        pendingVisibility === "visible" ||
        Boolean(live(previous) && previous.pending),
      keepFailure: true,
    })
    requestedReadiness = { body, operation }
  }

  async function commitReadiness(
    permitAvailability = true,
    forceUnregistered = false,
    pendingVisibility: "visible" | "silent" = "visible",
  ) {
    const commit = { confirmed: false }
    readinessCommits.add(commit)
    queueReadiness(permitAvailability, forceUnregistered, pendingVisibility)
    await drainReadiness()
    readinessCommits.delete(commit)
    return commit.confirmed
  }

  async function drainReadiness(): Promise<void> {
    if (readinessInFlight) {
      await readinessInFlight
      if (!stopped && requestedReadiness) await drainReadiness()
      return
    }
    const operation = (async () => {
      while (!stopped && requestedReadiness) {
        const { body, operation: readiness } = requestedReadiness
        const covered = [...readinessCommits]
        requestedReadiness = undefined
        try {
          const lease = leaseForSession(
            await backendRequest((signal) =>
              options.backend.writeReadiness(body, signal),
            ),
            options.sessionID,
          )
          for (const commit of covered) commit.confirmed = true
          if (!live(readiness)) continue
          const visibleLease = accessBlocked
            ? hideLeaseAfterAccessFailure(lease)!
            : lease
          if (visibleLease.owner && body.registered && !readinessFailClosed) {
            clearFailures(resolvedByHealthyReadiness)
          }
          settle(readiness)
          reportLease(visibleLease, visibleLease.owner
            ? undefined
            : ownershipFailure(visibleLease), {
            call: visibleLease.owner ? snapshot.call : idleCall,
            expectedMedia: visibleLease.owner ? snapshot.expectedMedia : undefined,
            recoveryMedia: visibleLease.owner
              ? snapshot.recoveryMedia
              : persistMediaCorrelation(undefined),
          })
          if (!visibleLease.owner) await releaseLocalMedia()
        } catch (error) {
          if (!live(readiness)) continue
          const failure = await failRequest(readiness, error)
          if (failure.kind === "conflict") void signalRefresh()
        }
      }
    })()
    const inFlight = operation.finally(() => {
      if (readinessInFlight === inFlight) readinessInFlight = undefined
    })
    readinessInFlight = inFlight
    await inFlight
    if (!stopped && requestedReadiness) await drainReadiness()
  }

  function connectMedia(): Promise<void> {
    if (mediaReleaseInFlight) {
      return mediaReleaseInFlight.then(() => connectMedia())
    }
    if (mediaConnectInFlight) return mediaConnectInFlight
    const controller = new AbortController()
    const timeoutID = clock.setTimeout(
      () => controller.abort(),
      mediaOperationTimeoutMilliseconds,
    )
    mediaConnectController = controller
    const operation = connectMediaOnce(controller.signal)
    const inFlight = operation.finally(() => {
      clock.clearTimeout(timeoutID)
      if (mediaConnectController === controller) {
        mediaConnectController = undefined
      }
      if (mediaConnectInFlight === inFlight) mediaConnectInFlight = undefined
    })
    mediaConnectInFlight = inFlight
    return inFlight
  }

  async function connectMediaOnce(signal: AbortSignal) {
    if (
      accessBlocked ||
      !snapshot.lease?.owner ||
      snapshot.readiness.mediaState !== "unavailable"
    ) {
      return
    }
    const media = open("media", { keepFailure: true })
    try {
      publish({
        readiness: {
          ...snapshot.readiness,
          mediaState: "registering",
          microphoneReady: false,
          audioReady: false,
          sessionHealthy: false,
        },
      })
      const acquiredMicrophone = await abortableOperation(
        options.microphone.start(() => {
          if (!live(media)) return
          fail(media, {
            kind: "technical-readiness",
            message: "Your microphone became unavailable. Reconnect it and try again.",
            recoverable: true,
          }, {
            readiness: {
              ...snapshot.readiness,
              microphoneReady: false,
            },
          })
          void releaseLocalMedia()
            .then(() => commitReadiness(false))
            .then(() => signalRefresh())
        }, signal),
        signal,
        (lateMicrophone) => lateMicrophone.stop(),
      )
      if (!live(media)) {
        acquiredMicrophone.stop()
        return
      }
      microphone = acquiredMicrophone
      publish({
        readiness: {
          ...snapshot.readiness,
          microphoneReady: true,
          audioReady: true,
          sessionHealthy: true,
          mediaState: "registering",
        },
      })
      const token = await backendRequest((signal) =>
        options.backend.issueMediaToken(
          { sessionID: options.sessionID },
          signal,
        ),
      )
      if (!live(media)) return
      const connectionCurrent = () => live(media)
      let mediaStateSynchronization = Promise.resolve()
      await abortableOperation(
        options.media.connect(
          token,
          options.remoteElementID ?? "calling-remote-audio",
          {
            onState: (mediaState) => {
              if (!connectionCurrent()) return
              const readiness = {
                ...snapshot.readiness,
                mediaState,
                audioReady: mediaState === "ready",
                sessionHealthy: mediaState === "ready",
              }
              if (mediaState === "reconnecting") {
                fail(media, {
                  kind: "media",
                  source: "media-reconnect",
                  message: "Call audio is reconnecting. Reconnect calling if it does not recover.",
                  recoverable: true,
                }, { readiness })
              } else {
                if (mediaState === "ready") media.failure = undefined
                publish({ readiness })
              }
              mediaStateSynchronization = commitReadiness(
                mediaState === "ready",
              ).then(() => signalRefresh())
            },
            onIncoming: (leg) => {
              if (connectionCurrent()) void handleIncomingMedia(leg, media)
              else void rejectSafely(leg)
            },
            onEnded: (leg) => {
              if (!connectionCurrent()) return
              const key = mediaKey(leg)
              const remembered = incomingMedia.get(leg.mediaToken)
              if (remembered && mediaKey(remembered) === key) {
                incomingMedia.delete(leg.mediaToken)
                publishOffers(snapshot.offers)
              }
              if (attachedLeg && mediaKey(attachedLeg) === key) {
                attachedLeg = undefined
                publish({ mediaAttachment: undefined, muted: false })
              }
              void signalRefresh()
            },
            onAudioIssue: () => {
              if (!connectionCurrent()) return
              fail(media, {
                kind: "media",
                message: "Call audio was interrupted. Reconnect calling and try again.",
                recoverable: true,
              })
            },
            onFailure: (failure) => {
              if (connectionCurrent()) void handleMediaFailure(failure)
            },
            refreshToken: async () => {
              if (!connectionCurrent()) return undefined
              try {
                const token = await backendRequest((signal) =>
                  options.backend.issueMediaToken(
                    { sessionID: options.sessionID },
                    signal,
                  ),
                )
                return connectionCurrent() ? token : undefined
              } catch (error) {
                if (connectionCurrent()) {
                  await failRequest(media, error, "authentication")
                }
                return undefined
              }
            },
          },
          signal,
        ),
        signal,
      )
      await mediaStateSynchronization
      if (!connectionCurrent()) return
      scheduleHeartbeat()
    } catch (error) {
      if (!live(media)) return
      if (failureFrom(error, "technical-readiness").kind === "access") {
        await failRequest(media, error, "technical-readiness")
        return
      }
      close(media)
      await disconnectMedia()
      microphone?.stop()
      microphone = undefined
      fail(media, failureFrom(error, "technical-readiness"), {
        readiness: {
          mediaState: "unavailable",
          microphoneReady: false,
          audioReady: false,
          sessionHealthy: false,
        },
      })
    }
  }

  async function reconcileStaleCommand() {
    if (!stopped) await signalRefresh()
  }

  function signalStaffIntent() {
    void signalRefresh()
  }

  async function commandFailed(
    operation: Operation,
    error: unknown,
    committed: () => boolean,
  ) {
    const failure = failureFrom(error, "temporary-request")
    if (failure.kind === "conflict" || failure.kind === "temporary-request") {
      await signalRefresh()
      if (!live(operation) || committed()) return false
    }
    return true
  }

  async function discardStaleMedia(leg: IncomingMediaLeg) {
    if (incomingMedia.get(leg.mediaToken) === leg) {
      incomingMedia.delete(leg.mediaToken)
    }
    if (attachedLeg === leg) attachedLeg = undefined
    await rejectSafely(leg)
  }

  async function handleIncomingMedia(
    leg: IncomingMediaLeg,
    media: Operation,
  ) {
    if (!live(media) || snapshot.readiness.mediaState !== "ready") {
      await rejectSafely(leg)
      return
    }
    const key = mediaKey(leg)
    if (attachedLeg) {
      const recoversCurrent = leg.recovery && mediaKey(attachedLeg) === key
      if (!recoversCurrent) {
        await rejectSafely(leg)
        return
      }
      const recovery = open("attach")
      const lostAudio: SoftphoneFailure = {
        kind: "media",
        message: "Call audio could not reconnect. Reconnect calling to restore audio.",
        recoverable: true,
      }
      let outcome: "attached" | "ended"
      try {
        outcome = await answerMediaLeg(leg)
      } catch {
        if (!live(media)) {
          await discardStaleMedia(leg)
          return
        }
        attachedLeg = undefined
        await rejectSafely(leg)
        fail(recovery, lostAudio, { mediaAttachment: undefined, muted: false })
        await signalRefresh()
        return
      }
      if (!live(media)) {
        await discardStaleMedia(leg)
        return
      }
      if (outcome !== "attached") {
        attachedLeg = undefined
        fail(recovery, lostAudio, { mediaAttachment: undefined, muted: false })
        await signalRefresh()
        return
      }
      attachedLeg = leg
      if (media.failure?.kind === "media") media.failure = undefined
      settle(recovery, { mediaAttachment: snapshot.mediaAttachment })
      await signalRefresh()
      if (!live(media)) {
        await discardStaleMedia(leg)
      }
      return
    }
    const existing = incomingMedia.get(leg.mediaToken)
    if (existing && mediaKey(existing) !== key) {
      await rejectSafely(leg)
      return
    }
    incomingMedia.set(leg.mediaToken, leg)
    incomingMediaDeadlines.set(
      leg,
      clock.now + mediaCorrelationWindowMilliseconds,
    )
    await signalRefresh()
    if (!live(media)) {
      await discardStaleMedia(leg)
      return
    }
    const answered = answeredLeg(snapshot.call)
    if (answered && answered.media !== key) return
    if (attachedLeg) {
      if (mediaKey(attachedLeg) === key && incomingMedia.get(leg.mediaToken) === leg) {
        incomingMedia.delete(leg.mediaToken)
      }
      return
    }
    if (
      snapshot.expectedCallID &&
      (snapshot.expectedMedia || leg.recovery)
    ) {
      await attachExpectedMedia(leg, media)
      return
    }
    publishOffers(snapshot.offers)
  }

  async function attachExpectedMedia(
    leg: IncomingMediaLeg,
    media = operations.get("media"),
  ) {
    if (!live(media)) {
      await discardStaleMedia(leg)
      return
    }
    const callID = snapshot.expectedCallID
    const expectedMedia = snapshot.expectedMedia
    const exactTransientLeg =
      expectedMedia?.callId === callID && expectedMedia.mediaToken === leg.mediaToken
    const restoredRecovery =
      leg.recovery &&
      snapshot.activeCall?.id === callID &&
      snapshot.recoveryMedia?.callID === callID &&
      Boolean(snapshot.activeCallLegID) &&
      snapshot.recoveryMedia.callLegID === snapshot.activeCallLegID &&
      mediaKey(snapshot.recoveryMedia) === mediaKey(leg)
    if (!callID) {
      incomingMedia.delete(leg.mediaToken)
      await rejectSafely(leg)
      return
    }
    if (!expectedMedia && !restoredRecovery) return
    if (!exactTransientLeg && !restoredRecovery) {
      incomingMedia.delete(leg.mediaToken)
      await rejectSafely(leg)
      return
    }
    if (incomingMedia.get(leg.mediaToken) !== leg) return
    incomingMedia.delete(leg.mediaToken)
    const attach = open("attach")
    let outcome: "attached" | "ended"
    try {
      outcome = await answerMediaLeg(leg)
    } catch (error) {
      if (!live(media)) {
        await discardStaleMedia(leg)
        return
      }
      await rejectSafely(leg)
      fail(attach, failureFrom(error, "media"))
      await signalRefresh()
      return
    }
    if (!live(media)) {
      await discardStaleMedia(leg)
      return
    }
    if (outcome !== "attached") {
      await signalRefresh()
      return
    }
    try {
      const call = restoredRecovery
        ? snapshot.activeCall!
        : await confirmExpectedMedia(callID, leg, media, attach)
      if (!call) {
        await discardStaleMedia(leg)
        return
      }
      if (!live(media) || !live(attach)) {
        await discardStaleMedia(leg)
        return
      }
      const mediaAttachment: RuntimeMediaCorrelation = {
        callID: call.id,
        ...(expectedMedia
          ? { callLegID: expectedMedia.callLegId }
          : snapshot.recoveryMedia?.callLegID
            ? { callLegID: snapshot.recoveryMedia.callLegID }
            : {}),
        providerLegID: leg.providerLegID,
        mediaToken: leg.mediaToken,
      }
      if (callSettled(call)) {
        attachedLeg = leg
        settle(attach, { mediaAttachment })
        if (!applyCall(call)) {
          purgeAttachedMedia(mediaAttachment, leg)
        }
        return
      }
      if (!applyCall(call)) {
        await rejectSafely(leg)
        return
      }
      attachedLeg = leg
      const confirmedOwnerLegID =
        call.state === "CONNECTED" && mediaAttachment.callLegID
          ? mediaAttachment.callLegID
          : snapshot.activeCallLegID
      settle(attach, {
        recoveryMedia: persistMediaCorrelation(mediaAttachment),
        mediaAttachment,
        call: trackCall(
          snapshot.call,
          call.id,
          confirmedOwnerLegID,
          Boolean(
            call.state === "CONNECTED" &&
              mediaAttachment.callLegID &&
              mediaAttachment.callLegID === confirmedOwnerLegID,
          ),
        ),
      })
    } catch (error) {
      await rejectSafely(leg)
      if (live(media)) await failRequest(attach, error, "conflict")
    }
  }

  async function confirmExpectedMedia(
    callID: string,
    leg: IncomingMediaLeg,
    media: Operation,
    attach: Operation,
  ) {
    const deadline = clock.now + backendRequestTimeoutMilliseconds
    let lastError: unknown
    while (live(media) && live(attach) && clock.now < deadline) {
      try {
        return await backendRequest(
          (signal) =>
            options.backend.confirmMedia(
              {
                callID,
                sessionID: options.sessionID,
                mediaToken: leg.mediaToken,
              },
              signal,
            ),
          Math.max(1, deadline - clock.now),
        )
      } catch (error) {
        lastError = error
        const failure = failureFrom(error, "conflict")
        if (
          failure.kind !== "conflict" &&
          failure.kind !== "temporary-request"
        ) {
          throw error
        }
      }
      if (!live(media) || !live(attach)) break
      if (clock.now + mediaConfirmationRetryMilliseconds >= deadline) break

      await wait(mediaConfirmationRetryMilliseconds)
    }
    if (lastError) throw lastError
  }

  function handleMediaFailure(failure: MediaFailure) {
    if (mediaFailureInFlight) return mediaFailureInFlight
    const operation = handleMediaFailureOnce(failure)
    const inFlight = operation.finally(() => {
      if (mediaFailureInFlight === inFlight) mediaFailureInFlight = undefined
    })
    mediaFailureInFlight = inFlight
    return inFlight
  }

  async function handleMediaFailureOnce(failure: MediaFailure) {
    const visibleFailure: SoftphoneFailure = {
      kind: failure === "authentication" ? "authentication" : "media",
      message:
        failure === "network"
          ? "Call audio lost its connection. Check your network and reconnect calling."
          : failure === "authentication"
            ? "Calling authentication expired. Refresh access and reconnect calling."
            : "Call audio is unavailable. Reconnect calling and try again.",
      recoverable: true,
    }
    attachedLeg = undefined
    report("media", visibleFailure, {
      call: withoutAnswered(snapshot.call),
      readiness: {
        ...snapshot.readiness,
        mediaState: "unavailable",
        audioReady: false,
        sessionHealthy: false,
      },
      mediaAttachment: undefined,
      muted: false,
    })
    await releaseLocalMedia()
    if (stopped) return
    await commitReadiness(false)
    if (!stopped && snapshot.lease?.owner) scheduleHeartbeat(500)
  }

  function releaseLocalMedia() {
    if (mediaReleaseInFlight) return mediaReleaseInFlight
    const operation = releaseLocalMediaOnce()
    const inFlight = operation.finally(() => {
      if (mediaReleaseInFlight === inFlight) mediaReleaseInFlight = undefined
    })
    mediaReleaseInFlight = inFlight
    return inFlight
  }

  async function releaseLocalMediaOnce() {
    if (heartbeatTimer !== undefined) clock.clearTimeout(heartbeatTimer)
    heartbeatTimer = undefined
    mediaConnectController?.abort()
    mediaConnectController = undefined
    mediaConnectInFlight = undefined
    close(operations.get("readiness"))
    close(operations.get("media"))
    clearConfirmationWaits()
    for (const controller of mediaEffectControllers) controller.abort()
    incomingMedia.clear()
    attachedLeg = undefined
    const disconnecting = disconnectMedia()
    microphone?.stop()
    microphone = undefined
    publish({
      call: withoutAnswered(snapshot.call),
      offers: [],
      mediaAttachment: undefined,
      muted: false,
      readiness: {
        mediaState: "unavailable",
        microphoneReady: false,
        audioReady: false,
        sessionHealthy: false,
      },
    })
    syncAttention([])
    await disconnecting
  }

  function activeSourceTransfer() {
    const callID = snapshot.activeCall?.id
    if (!callID || !snapshot.activeCallLegID) return undefined
    return snapshot.staffTransfers.find(
      (transfer) =>
        transfer.callId === callID &&
        transfer.sourceCallLegId === snapshot.activeCallLegID &&
        (transfer.state === "REQUESTED" || transfer.state === "ACCEPTED"),
    )
  }

  function beginTransfer() {
    return open("transfer", { pending: true })
  }

  async function reconcileSettledTransfer(transferID: string) {
    await requestRefresh(true)
    return !snapshot.staffTransfers.some(
      (transfer) => transfer.id === transferID,
    )
  }

  async function removeTransferOffer(offer: RuntimeOffer) {
    const leg = exactIncomingLeg(offer)
    incomingMedia.delete(offer.mediaToken)
    publish({
      offers: snapshot.offers.filter(
        (candidate) => candidate.callLegId !== offer.callLegId,
      ),
      staffTransfers: snapshot.staffTransfers.filter(
        (transfer) => transfer.id !== offer.staffTransferId,
      ),
    })
    syncAttention(snapshot.offers)
    if (leg) await rejectSafely(leg)
  }

  function reclaimLapsedLease() {
    if (
      stopped ||
      leaseReclaim ||
      !snapshot.lease ||
      !leaseLapsedForSession(snapshot.lease, options.sessionID)
    ) {
      return
    }
    leaseReclaim = takeOver(false).finally(() => {
      leaseReclaim = undefined
    })
  }

  function claimedCall(lease: SoftphoneState) {
    if (!lease.owner) {
      return snapshot.call.stage === "tracking" ? idleCall : snapshot.call
    }
    const callID =
      lease.activeCallId || snapshot.expectedCallID || recoveryCallID
    if (!callID) return snapshot.call
    const restored = snapshot.recoveryMedia
    return trackCall(
      snapshot.call,
      callID,
      snapshot.call.stage !== "tracking" && restored?.callID === callID
        ? (restored.callLegID ?? "")
        : undefined,
    )
  }

  async function signalRefresh() {
    if (refreshTimer !== undefined) clock.clearTimeout(refreshTimer)
    refreshTimer = undefined
    await requestRefresh(true)
    scheduleRefresh()
  }

  async function takeOver(takeover = true) {
    if (accessBlocked) return
    if (!snapshot.lease?.owner && snapshot.lease?.activeCallId) {
      report("ownership", ownershipFailure(snapshot.lease))
      return
    }
    const acquisition = open("lease")
    try {
      const lease = leaseForSession(
        await backendRequest((signal) =>
          options.backend.acquireLease(
            {
              sessionID: options.sessionID,
              takeover,
            },
            signal,
          ),
        ),
        options.sessionID,
      )
      if (!live(acquisition)) return
      settle(acquisition)
      reportLease(lease, lease.owner ? undefined : ownershipFailure(lease), {
        call: claimedCall(lease),
      })
      if (!lease.owner) return
      await signalRefresh()
      await connectMedia()
    } catch (error) {
      await failRequest(acquisition, error)
    }
  }


  return {
    getSnapshot: () => snapshot,
    subscribe(listener) {
      listeners.add(listener)
      return () => listeners.delete(listener)
    },
    async start() {
      if (stopInFlight) await stopInFlight
      if (!stopped) return
      recoveryCallID = options.loadCallRecoveryID?.() ?? ""
      readinessFailClosed = false
      accessBlocked = false
      stopped = false
      operations.clear()
      const session = open("session")
      publish({ phase: "starting" })
      unsubscribeVisibility = visibility.subscribe(() => {
        if (!stopped) {
          syncAttention(snapshot.offers)
          void signalRefresh()
        }
      })
      unsubscribeCallingHints = callingHints.subscribe(handleCallingHint)
      const acquisition = open("lease")
      try {
        const lease = leaseForSession(
          await backendRequest((signal) =>
            options.backend.acquireLease(
              {
                sessionID: options.sessionID,
                takeover: false,
              },
              signal,
            ),
          ),
          options.sessionID,
        )
        if (!live(session)) return
        settle(acquisition)
        reportLease(lease, lease.owner ? undefined : ownershipFailure(lease), {
          call: claimedCall(lease),
        })
        await requestRefresh(true)
        if (!live(session)) return
        if (lease.owner) await connectMedia()
        if (!live(session)) return
        publish({ phase: "running" })
        scheduleRefresh()
        scheduleHeartbeat()
      } catch (error) {
        await failRequest(acquisition, error)
        if (!stopped) publish({ phase: "running" })
        scheduleRefresh()
      }
    },
    stop() {
      if (stopInFlight) return stopInFlight
      if (stopped) return Promise.resolve()
      const ownedLease =
        snapshot.lease?.owner === true &&
        snapshot.lease.sessionId === options.sessionID
      stopInFlight = (async () => {
        stopped = true
        operations.clear()
        if (refreshTimer !== undefined) clock.clearTimeout(refreshTimer)
        if (heartbeatTimer !== undefined) clock.clearTimeout(heartbeatTimer)
        refreshTimer = undefined
        heartbeatTimer = undefined
        refreshQueued = false
        refreshInFlight = undefined
        requestedReadiness = undefined
        etag = undefined
        authoritativeRingingMedia.clear()
        temporaryFailures = 0
        heartbeatFailures = 0
        for (const request of backendRequests) request.abort()
        unsubscribeVisibility?.()
        unsubscribeVisibility = undefined
        unsubscribeCallingHints?.()
        unsubscribeCallingHints = undefined
        callingHintPending = false
        callingPracticeIDs = undefined
        callingChanges.reset()
        lastRefreshStartedAt = Number.NEGATIVE_INFINITY
        await releaseLocalMedia()
        operations.clear()
        publish({
          phase: "stopped",
          lease: undefined,
          offers: [],
          staffTransfers: [],
          transferCandidates: [],
          call: idleCall,
          expectedMedia: undefined,
          mediaAttachment: undefined,
          muted: false,
        })
        await readinessInFlight
        if (!ownedLease) return
        try {
          await backendRequest((signal) =>
            options.backend.writeReadiness(
              {
                sessionId: options.sessionID,
                registered: false,
                microphoneReady: false,
                audioReady: false,
                sessionHealthy: false,
                available: false,
              },
              signal,
            ),
          )
        } catch (error) {
          const failure = failureFrom(error, "temporary-request")
          report("readiness", {
            ...failure,
            message: `Calling stopped locally, but backend readiness could not be cleared. ${failure.message}`,
            recoverable: false,
          })
        }
      })().finally(() => {
        stopInFlight = undefined
      })
      return stopInFlight
    },
    signalRefresh,
    async setAvailability(available) {
      publish({ availabilityIntent: available })
      options.persistAvailabilityIntent?.(available)
      if (accessBlocked) return
      if (!snapshot.lease?.owner && available) await takeOver()
      if (snapshot.lease?.owner && snapshot.readiness.mediaState === "unavailable") {
        await connectMedia()
      }
      await commitReadiness()
      await signalRefresh()
    },
    async startOutbound(input) {
      if (!snapshot.lease?.owner || snapshot.readiness.mediaState !== "ready") {
        const failure: SoftphoneFailure = {
          kind: "technical-readiness",
          message: "Reconnect calling before starting a Call.",
          recoverable: true,
        }
        fail(open("outbound"), failure)
        return failure
      }
      if (snapshot.occupied) {
        const failure: SoftphoneFailure = {
          kind: "conflict",
          message: "Finish the current Call before starting another.",
          recoverable: true,
        }
        fail(open("outbound"), failure)
        return failure
      }
      const outbound = open("outbound", {}, {
        call: { stage: "dialing", pending: pendingOutboundCall(input) },
      })
      signalStaffIntent()
      try {
        const call = await backendRequest((signal) =>
          options.backend.startOutbound(
            {
              ...input,
              sessionId: options.sessionID,
            },
            signal,
          ),
        )
        if (!live(outbound)) {
          await reconcileStaleCommand()
          return
        }
        settle(outbound, {
          call: { ...trackCall(snapshot.call, call.id), pending: undefined },
        })
        applyCall(call)
        await commitReadiness(false)
        await signalRefresh()
      } catch (error) {
        if (!live(outbound)) {
          await reconcileStaleCommand()
          return
        }
        if (
          await commandFailed(outbound, error, () =>
            Boolean(snapshot.expectedCallID),
          )
        ) {
          return failRequest(outbound, error, "temporary-request", {
            call: withoutPending(snapshot.call),
          })
        }
        settle(outbound, { call: withoutPending(snapshot.call) })
      }
    },
    async answer(callLegID) {
      const offer = snapshot.offers.find((candidate) => candidate.callLegId === callLegID)
      const leg = offer && exactIncomingLeg(offer)
      if (
        !offer ||
        !leg ||
        snapshot.activeCall ||
        snapshot.pendingCall ||
        snapshot.pending.transfer ||
        attachedLeg ||
        answeredLeg(snapshot.call)
      ) {
        return
      }
      incomingMedia.delete(offer.mediaToken)
      const media = operations.get("media")
      const answer = open("answer", {}, {
        call: {
          stage: "answering",
          pending: pendingInboundCall(offer),
          answered: {
            callID: offer.callId,
            callLegID: offer.callLegId,
            media: mediaKey(leg),
          },
        },
        offers: snapshot.offers.filter(
          (candidate) => candidate.callLegId !== callLegID,
        ),
      })
      syncAttention(snapshot.offers)
      signalStaffIntent()
      const restoredOffers = () =>
        offersWithMedia([
          ...snapshot.offers.filter(
            (candidate) => candidate.callLegId !== offer.callLegId,
          ),
          offer,
        ])
      let outcome
      try {
        outcome = await answerMediaLeg(leg)
      } catch (error) {
        if (!live(media) || !live(answer)) {
          await discardStaleMedia(leg)
          return
        }
        const abandoned = withoutAnswered(withoutPending(snapshot.call))
        if (mediaEffectTimedOut(error)) {
          publish({ call: abandoned, offers: restoredOffers() }, answer)
          await commitReadiness(false)
          fail(answer, failureFrom(error, "media"))
          await signalRefresh()
          void rejectSafely(leg)
          return
        }
        incomingMedia.set(offer.mediaToken, leg)
        fail(answer, failureFrom(error, "media"), {
          call: abandoned,
          offers: restoredOffers(),
        })
        return
      }
      if (!live(media) || !live(answer)) {
        await discardStaleMedia(leg)
        return
      }
      if (outcome !== "attached") {
        publish({ call: withoutPending(snapshot.call) })
        await signalRefresh()
        if (live(answer)) {
          settle(answer, { call: withoutAnswered(snapshot.call) })
        }
        return
      }
      attachedLeg = leg
      ignoredCallIDs.delete(offer.callId)
      const mediaAttachment: RuntimeMediaCorrelation = {
        callID: offer.callId,
        callLegID: offer.callLegId,
        providerLegID: leg.providerLegID,
        mediaToken: offer.mediaToken,
      }
      settle(answer, {
        call: trackCall(snapshot.call, offer.callId, offer.callLegId),
        offers: [],
        recoveryMedia: persistMediaCorrelation(mediaAttachment),
        mediaAttachment,
      })
      syncAttention(snapshot.offers)
      await commitReadiness(false)
      await signalRefresh()
    },
    async loadTransferCandidates() {
      const call = snapshot.activeCall
      if (!call || !snapshot.controls.canTransfer) return
      const transfer = beginTransfer()
      try {
        const candidates = await backendRequest((signal) =>
          options.backend.listTransferCandidates(
            { callID: call.id, sessionID: options.sessionID },
            signal,
          ),
        )
        if (live(transfer)) settle(transfer, { transferCandidates: candidates })
      } catch (error) {
        if (!live(transfer)) return
        await failRequest(transfer, error, "temporary-request", {
          transferCandidates: [],
        })
      }
    },
    async requestTransfer(recipientSubject, handoffNote, idempotencyKey) {
      const call = snapshot.activeCall
      if (
        !call ||
        !snapshot.controls.canTransfer ||
        !recipientSubject ||
        !idempotencyKey
      ) {
        return
      }
      const normalizedNote = handoffNote.trim()
      const transfer = beginTransfer()
      signalStaffIntent()
      try {
        const requested = await backendRequest((signal) =>
          options.backend.requestTransfer(
            {
              callID: call.id,
              sessionID: options.sessionID,
              recipientSubject,
              idempotencyKey,
              expectedVersion: call.version,
              ...(normalizedNote ? { handoffNote: normalizedNote } : {}),
            },
            signal,
          ),
        )
        if (live(transfer)) {
          settle(transfer, {
            staffTransfers: [
              ...snapshot.staffTransfers.filter(
                (current) => current.id !== requested.id,
              ),
              requested,
            ],
            transferCandidates: [],
          })
        }
        await signalRefresh()
      } catch (error) {
        await requestRefresh(true)
        if (!live(transfer)) return
        const committed = snapshot.staffTransfers.some(
          (current) =>
            current.callId === call.id &&
            current.recipientSubject === recipientSubject &&
            current.handoffNote === normalizedNote,
        )
        if (committed) settle(transfer, { transferCandidates: [] })
        else await failRequest(transfer, error)
      }
    },
    async cancelTransfer() {
      const transfer = activeSourceTransfer()
      if (!transfer || snapshot.pending.transfer) return
      const cancel = beginTransfer()
      try {
        await backendRequest((signal) =>
          options.backend.cancelTransfer(
            { transferID: transfer.id, sessionID: options.sessionID },
            signal,
          ),
        )
        if (live(cancel)) {
          settle(cancel, {
            staffTransfers: snapshot.staffTransfers.filter(
              (current) => current.id !== transfer.id,
            ),
          })
        }
        await signalRefresh()
      } catch (error) {
        const committed = await reconcileSettledTransfer(transfer.id)
        if (!live(cancel)) return
        if (committed) settle(cancel)
        else await failRequest(cancel, error)
      }
    },
    async declineTransfer(callLegID) {
      const offer = snapshot.offers.find(
        (candidate) =>
          candidate.callLegId === callLegID &&
          candidate.offerKind === "STAFF_TRANSFER" &&
          candidate.staffTransferId,
      )
      if (!offer || snapshot.pending.transfer) return
      const decline = beginTransfer()
      try {
        await backendRequest((signal) =>
          options.backend.declineTransfer(
            {
              transferID: offer.staffTransferId,
              sessionID: options.sessionID,
            },
            signal,
          ),
        )
        settle(decline)
        await removeTransferOffer(offer)
        await signalRefresh()
      } catch (error) {
        const committed = await reconcileSettledTransfer(offer.staffTransferId)
        if (committed) {
          settle(decline)
          await removeTransferOffer(offer)
        } else if (live(decline)) {
          await failRequest(decline, error)
        }
      }
    },
    async hangup() {
      const call = snapshot.activeCall
      if (!call || snapshot.endingCallID) return
      const hangup = open("hangup", {}, {
        call: withEnding(snapshot.call, true),
      })
      signalStaffIntent()
      try {
        const committed = await backendRequest((signal) =>
          options.backend.hangup(
            {
              callID: call.id,
              sessionID: options.sessionID,
            },
            signal,
          ),
        )
        if (!live(hangup)) {
          await reconcileStaleCommand()
          return
        }
        settle(hangup)
        applyCall(committed)
        await signalRefresh()
      } catch (error) {
        if (!live(hangup)) return
        await signalRefresh()
        if (!live(hangup)) return
        if (
          snapshot.pendingDisposition ||
          snapshot.activeCall?.endRequested ||
          callSettled(snapshot.activeCall)
        ) {
          settle(hangup)
          return
        }
        await failRequest(hangup, error, "temporary-request", {
          call: withEnding(snapshot.call, false),
        })
      }
    },
    async retry(idempotencyKey) {
      const call = snapshot.activeCall
      if (
        !call?.retryAllowed ||
        snapshot.expectedCallID ||
        snapshot.pending.retry
      ) {
        return
      }
      const retry = open("retry", { pending: true })
      signalStaffIntent()
      try {
        const retried = await backendRequest((signal) =>
          options.backend.retry(
            {
              callID: call.id,
              sessionID: options.sessionID,
              idempotencyKey,
            },
            signal,
          ),
        )
        if (!live(retry)) {
          await reconcileStaleCommand()
          return
        }
        await releaseLocalMedia()
        if (!live(retry)) {
          await reconcileStaleCommand()
          return
        }
        settle(retry, {
          call: trackCall(snapshot.call, retried.id),
          recoveryMedia: persistMediaCorrelation(undefined),
        })
        applyCall(retried)
        await connectMedia()
        await signalRefresh()
      } catch (error) {
        if (!live(retry)) {
          await reconcileStaleCommand()
          return
        }
        const committed = () =>
          Boolean(
            snapshot.expectedCallID && snapshot.expectedCallID !== call.id,
          )
        if (await commandFailed(retry, error, committed)) {
          await failRequest(retry, error)
        } else {
          settle(retry)
        }
      }
    },
    async recover() {
      if (mediaFailureInFlight) await mediaFailureInFlight
      const failure = snapshot.failure
      if (!snapshot.lease?.owner || failure?.kind === "ownership") {
        await takeOver()
        return
      }
      clearFailures()
      publish({})
      if (
        failure?.kind === "media" ||
        failure?.kind === "technical-readiness" ||
        failure?.kind === "authentication"
      ) {
        if (heartbeatTimer !== undefined) clock.clearTimeout(heartbeatTimer)
        heartbeatTimer = undefined
        readinessFailClosed = true
        if (!(await commitReadiness(false, true))) return
        await releaseLocalMedia()
        await connectMedia()
        if (snapshot.readiness.mediaState !== "unavailable") {
          readinessFailClosed = false
          if (snapshot.readiness.mediaState === "ready") {
            await commitReadiness()
          }
        }
      }
      await signalRefresh()
    },
    dismissOutcome() {
      const call = snapshot.activeCall
      if (
        !call ||
        !callSettled(call) ||
        snapshot.expectedCallID ||
        snapshot.mediaAttachment ||
        snapshot.pending.retry
      ) {
        return
      }
      attachedLeg = undefined
      persistCallRecoveryID(undefined)
      publish({
        call: idleCall,
        mediaAttachment: undefined,
        muted: false,
      })
      void commitReadiness()
    },
    async dispose(outcome) {
      const call = snapshot.pendingDisposition
      if (!call || snapshot.pending.disposition) return
      const disposition = open("disposition", { pending: true })
      signalStaffIntent()
      try {
        const result = await backendRequest((signal) =>
          options.backend.dispose(
            {
              callID: call.id,
              sessionID: options.sessionID,
              outcome,
            },
            signal,
          ),
        )
        if (!live(disposition)) {
          await reconcileStaleCommand()
          return
        }
        applyCall(result.call)
        settle(disposition, {
          call: snapshot.call.stage === "disposition" ? idleCall : snapshot.call,
        })
        await commitReadiness()

        await signalRefresh()
        return result
      } catch (error) {
        if (!live(disposition)) {
          await reconcileStaleCommand()
          return
        }
        if (await commandFailed(disposition, error, () => !snapshot.pendingDisposition)) {
          await failRequest(disposition, error)
        } else {
          settle(disposition)
        }
      }
    },
    toggleMute() {
      if (!attachedLeg || !snapshot.controls.canMute) return
      if (snapshot.muted) attachedLeg.unmute()
      else attachedLeg.mute()
      publish({ muted: !snapshot.muted })
    },
    sendDTMF(digit) {
      return snapshot.controls.canKeypad && attachedLeg
        ? attachedLeg.sendDTMF(digit)
        : false
    },
  }

  function exactIncomingLeg(offer: Pick<RingingCallLeg, "mediaToken">) {
    return incomingMedia.get(offer.mediaToken)
  }

  function offersWithMedia(offers: readonly RingingCallLeg[]): RuntimeOffer[] {
    return offers.map((offer) => ({
      ...offer,
      answerReady: exactIncomingLeg(offer) !== undefined,
    }))
  }

  function publishOffers(offers: readonly RuntimeOffer[]) {
    const next = offersWithMedia(offers)
    if (next.some((offer, index) => offer.answerReady !== offers[index].answerReady)) {
      publish({ offers: next })
    }
  }

  function persistMediaCorrelation(
    correlation: RuntimeMediaCorrelation | undefined,
  ) {
    options.persistMediaCorrelation?.(correlation)
    return correlation
  }
}

function derive(
  model: SoftphoneModel,
  operations: ReadonlyMap<OperationSlot, Operation>,
): RuntimeSnapshot {
  const pending = {
    availability: Boolean(operations.get("readiness")?.pending),
    retry: Boolean(operations.get("retry")?.pending),
    disposition: Boolean(operations.get("disposition")?.pending),
    transfer: Boolean(operations.get("transfer")?.pending),
  }
  const snapshot = {
    ...model,
    ...callView(model.call),
    pending,
    failure: shownFailure(operations),
  }
  const call = snapshot.activeCall
  const mediaAttached = Boolean(snapshot.mediaAttachment)
  const ending = Boolean(call && snapshot.endingCallID === call.id) || Boolean(call?.endRequested)
  const connected = call?.state === "CONNECTED"
  const mediaControlsReady =
    snapshot.readiness.mediaState === "ready" &&
    snapshot.readiness.microphoneReady &&
    snapshot.readiness.audioReady &&
      snapshot.readiness.sessionHealthy
  const occupied = Boolean(
    snapshot.pendingCall ||
      snapshot.activeCall ||
      snapshot.expectedCallID ||
      snapshot.mediaAttachment ||
      snapshot.pendingDisposition ||
      snapshot.offers.length > 0 ||
      snapshot.pending.retry ||
      snapshot.pending.disposition ||
      snapshot.pending.transfer,
  )
  const canEnd =
    call?.direction === "OUTBOUND"
      ? ["PREPARING", "RINGING", "CONNECTING", "CONNECTED"].includes(
          call.state,
        )
      : call?.direction === "INBOUND" && connected
  return {
    ...snapshot,
    occupied,
    offers: snapshot.offers.map((offer) => ({ ...offer })),
    controls: {
      canEnd:
        Boolean(
          snapshot.lease?.owner &&
            canEnd &&
            !ending &&
            (call?.state !== "CONNECTED" || snapshot.committedOwner),
        ),
      canMute: Boolean(
        snapshot.lease?.owner &&
          snapshot.committedOwner &&
          connected &&
          mediaAttached &&
          mediaControlsReady &&
          !ending,
      ),
      canKeypad: Boolean(
        snapshot.lease?.owner &&
          snapshot.committedOwner &&
          connected &&
          mediaAttached &&
          mediaControlsReady &&
          !ending,
      ),
      canRetry: Boolean(snapshot.lease?.owner && call?.retryAllowed && !snapshot.expectedCallID),
      canDispose: Boolean(snapshot.pendingDisposition && !snapshot.pending.disposition),
      canTransfer: Boolean(
        snapshot.lease?.owner &&
          snapshot.committedOwner &&
          connected &&
          !snapshot.pending.transfer &&
          !snapshot.staffTransfers.some(
            (transfer) =>
              transfer.callId === call?.id &&
              transfer.sourceCallLegId === snapshot.activeCallLegID,
          ),
      ),
    },
  }
}

function callView(call: CallPointer) {
  const tracking = call.stage === "tracking" ? call : undefined
  return {
    pendingCall:
      call.stage === "dialing" || call.stage === "answering"
        ? call.pending
        : tracking && !tracking.call
          ? tracking.pending
          : undefined,
    activeCall:
      call.stage === "outcome" ? call.call : tracking?.call,
    pendingDisposition: call.stage === "disposition" ? call.call : undefined,
    expectedCallID: tracking?.callID ?? "",
    activeCallLegID: tracking?.legID ?? "",
    endingCallID: tracking?.ending ? tracking.callID : "",
    committedOwner: tracking?.committed ?? false,
  }
}

function answeredLeg(call: CallPointer) {
  return call.stage === "answering" || call.stage === "tracking"
    ? call.answered
    : undefined
}

function withoutAnswered(call: CallPointer): CallPointer {
  if (call.stage === "tracking") return { ...call, answered: undefined }
  if (call.stage !== "answering") return call
  return call.pending ? { stage: "answering", pending: call.pending } : idleCall
}

function trackCall(
  call: CallPointer,
  callID: string,
  legID?: string,
  committed?: boolean,
): TrackingCall {
  if (call.stage === "tracking" && call.callID === callID) {
    return {
      ...call,
      legID: legID ?? call.legID,
      committed: committed ?? call.committed,
    }
  }
  return {
    stage: "tracking",
    callID,
    legID: legID ?? "",
    pending:
      call.stage === "dialing" || call.stage === "answering"
        ? call.pending
        : undefined,
    answered: answeredLeg(call),
    ending: false,
    committed: committed ?? false,
  }
}

function withoutPending(call: CallPointer): CallPointer {
  if (call.stage === "dialing") return idleCall
  if (call.stage === "tracking") return { ...call, pending: undefined }
  if (call.stage !== "answering") return call
  return call.answered ? { stage: "answering", answered: call.answered } : idleCall
}

function withEnding(call: CallPointer, ending: boolean): CallPointer {
  return call.stage === "tracking" ? { ...call, ending } : call
}

function shownFailure(operations: ReadonlyMap<OperationSlot, Operation>) {
  for (const slot of failurePriority) {
    const failure = operations.get(slot)?.failure
    if (failure) return failure
  }
}

function callSettled(call: Pick<CallingCall, "state"> | undefined) {
  return Boolean(call && isSettledCallState(call.state))
}

function leaseLapsedForSession(lease: SoftphoneState, sessionID: string) {
  return !lease.owner && lease.sessionId === sessionID && !lease.activeCallId
}

function leaseForSession(lease: SoftphoneState, sessionID: string): SoftphoneState {
  const owner = lease.owner && lease.sessionId === sessionID
  return {
    ...lease,
    owner,
    available: owner && lease.available,
  }
}

function pendingOutboundCall(
  input: Omit<StartOutboundCallRequest, "sessionId">,
): RuntimePendingCall {
  return {
    id: `pending:${input.idempotencyKey}`,
    direction: "OUTBOUND",
    entryPoint: input.taskId ? "TASK" : "STANDALONE",
    state: "PREPARING",
    displayName: "",
    phone: input.destination ?? "",
    locationName: "",
    transferReason: "",
    retryAllowed: false,
    endRequested: false,
    ...(input.practiceId ? { practiceId: input.practiceId } : {}),
    ...(input.locationId ? { locationId: input.locationId } : {}),
  }
}

function pendingInboundCall(offer: RuntimeOffer): RuntimePendingCall {
  return {
    id: offer.callId,
    direction: "INBOUND",
    entryPoint: "AI_HANDOFF",
    state: "CONNECTING",
    displayName: offer.displayName,
    phone: offer.phone,
    locationName: offer.locationName,
    transferReason: offer.transferReason,
    retryAllowed: false,
    endRequested: false,
    practiceId: offer.practiceId,
    locationId: offer.locationId,
  }
}

function refreshDelay(
  snapshot: SoftphoneRuntimeSnapshot,
  hidden: boolean,
  callingHintsCover: boolean,
) {
  const activeDelay = activeRefreshDelay(snapshot)
  if (activeDelay === undefined) {
    if (callingHintsCover) return hintedIdleRefreshMilliseconds
    return hidden ? hiddenRefreshMilliseconds : idleRefreshMilliseconds
  }
  return hidden ? hiddenRefreshMilliseconds : activeDelay
}

function activeRefreshDelay(snapshot: SoftphoneRuntimeSnapshot) {
  if (snapshot.offers.length > 0) return fastRefreshMilliseconds
  if (
    snapshot.pendingCall ||
    snapshot.pending.retry ||
    snapshot.pending.disposition
  ) {
    return fastRefreshMilliseconds
  }
  if (snapshot.endingCallID || snapshot.activeCall?.endRequested) {
    return fastRefreshMilliseconds
  }
  switch (snapshot.activeCall?.state) {
    case "PREPARING":
    case "RINGING":
    case "CONNECTING":
      return fastRefreshMilliseconds
    case "CONNECTED":
      return 1_000
    default:
      return undefined
  }
}

function temporaryRetryDelay(failures: number, capMilliseconds: number) {
  return Math.min(capMilliseconds, 500 * 2 ** (failures - 1))
}

function heartbeatDelay(sessionID: string) {
  let hash = 0
  for (let index = 0; index < sessionID.length; index += 1) {
    hash = (Math.imul(hash, 31) + sessionID.charCodeAt(index)) >>> 0
  }
  return (
    heartbeatMinimumMilliseconds +
    (hash % (heartbeatMaximumMilliseconds - heartbeatMinimumMilliseconds + 1))
  )
}

function failureFrom(error: unknown, fallback: SoftphoneFailureKind): SoftphoneFailure {
  if (error instanceof SoftphoneAdapterError) {
    return {
      kind: error.kind,
      message: error.message,
      recoverable:
        error.retryable ||
        (error.kind !== "authentication" && error.kind !== "access"),
    }
  }
  return {
    kind: fallback,
    message:
      fallback === "technical-readiness"
        ? "Browser audio could not be started. Check your microphone and try again."
        : fallback === "media"
          ? "Call audio could not start. Check your microphone and try Answer again."
        : "Calling could not refresh. Check your connection and try again.",
    recoverable: true,
  }
}
