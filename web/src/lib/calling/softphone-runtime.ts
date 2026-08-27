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
import type {
  CallingMediaAdapter,
  IncomingMediaLeg,
  MediaFailure,
  MediaState,
} from "./media-adapter.ts"

export type SoftphoneFailureKind =
  | "authentication"
  | "access"
  | "ownership"
  | "technical-readiness"
  | "media"
  | "conflict"
  | "temporary-request"

export type SoftphoneFailure = {
  kind: SoftphoneFailureKind
  message: string
  recoverable: boolean
}

export type RuntimeOffer = RingingCallLeg & { answerReady: boolean }

export type RuntimePendingCall = Pick<
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

function sameMediaIdentity(
  left: Pick<IncomingMediaLeg, "providerLegID" | "mediaToken">,
  right: Pick<IncomingMediaLeg, "providerLegID" | "mediaToken">,
) {
  return (
    left.providerLegID === right.providerLegID &&
    left.mediaToken === right.mediaToken
  )
}

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

export type StateRead =
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
  readState(input: { etag?: string }, signal?: AbortSignal): Promise<StateRead>
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

export interface SoftphoneVisibility {
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
  ): Promise<void>
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
  let heartbeatTimer: number | undefined
  let refreshInFlight: Promise<void> | undefined
  let refreshQueued = false
  let stopped = true
  let stopInFlight: Promise<void> | undefined
  let lifecycleGeneration = 0
  let etag: string | undefined
  let temporaryFailures = 0
  let readinessGeneration = 0
  let readinessConfirmedGeneration = 0
  let readinessInFlight: Promise<void> | undefined
  let requestedReadiness: CallingReadinessRequest | undefined
  let readinessFailClosed = false
  let accessFailureClosing = false
  let accessBlocked = false
  let unsubscribeVisibility: (() => void) | undefined
  let microphoneGeneration = 0
  let mediaConnectInFlight: Promise<void> | undefined
  let mediaConnectController: AbortController | undefined
  let mediaReleaseInFlight: Promise<void> | undefined
  let mediaFailureInFlight: Promise<void> | undefined
  let attachedLeg: IncomingMediaLeg | undefined
  let answeredInbound:
    | {
        callID: string
        callLegID: string
        providerLegID: string
        mediaToken: string
    }
    | undefined
  const ignoredCallIDs = new Set<string>()
  const restoredMedia = options.loadMediaCorrelation?.()
  let recoveryCallID = options.loadCallRecoveryID?.() ?? ""
  let snapshot: SoftphoneRuntimeSnapshot = derive({
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
    expectedCallID: recoveryCallID,
    activeCallLegID: restoredMedia?.callLegID ?? "",
    terminalVersions: {},
    recoveryMedia: restoredMedia,
    muted: false,
    endingCallID: "",
    committedOwner: false,
    pending: {
      availability: false,
      retry: false,
      disposition: false,
      transfer: false,
    },
  })

  function publish(update: Partial<SoftphoneRuntimeSnapshot>) {
    snapshot = derive({ ...snapshot, ...update })
    for (const listener of listeners) listener()
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
              // The stale lifecycle already finished; cleanup is best effort.
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
      // Local lifecycle cleanup must finish even when the provider SDK stalls.
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

  function setFailure(error: unknown, fallback: SoftphoneFailureKind = "temporary-request") {
    const failure = failureFrom(error, fallback)
    publish({ failure })
    return failure
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
      queueReadiness(false, true)
      const closing = readinessInFlight ?? drainReadiness()
      void closing.finally(() => {
        accessFailureClosing = false
      })
    }
    publish({
      lease: hideLeaseAfterAccessFailure(snapshot.lease),
      offers: [],
      staffTransfers: [],
      transferCandidates: [],
      activeCall: undefined,
      pendingCall: undefined,
      pendingDisposition: undefined,
      expectedCallID: "",
      activeCallLegID: "",
      expectedMedia: undefined,
      recoveryMedia: persistMediaCorrelation(undefined),
      mediaAttachment: undefined,
      muted: false,
      endingCallID: "",
      committedOwner: false,
      pending: {
        availability: false,
        retry: false,
        disposition: false,
        transfer: false,
      },
      readiness: {
        mediaState: "unavailable",
        microphoneReady: false,
        audioReady: false,
        sessionHealthy: false,
      },
      failure,
    })
    await releaseLocalMedia()
  }

  async function setRequestFailure(
    error: unknown,
    fallback: SoftphoneFailureKind = "temporary-request",
  ) {
    const failure = failureFrom(error, fallback)
    if (failure.kind === "access") {
      await failClosedAccess(failure)
      return failure
    }
    publish({ failure })
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

  async function setCommandFailure(
    error: unknown,
    fallback: SoftphoneFailureKind = "temporary-request",
  ) {
    const failure = failureFrom(error, fallback)
    if (failure.kind === "access") {
      await failClosedAccess(failure)
      return
    }
    publish({ failure })
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
    if (attachedLeg !== leg) return
    incomingMedia.delete(attachment.mediaToken)
    const purge = rejectSafely(leg)
    void purge.then((released) => {
      if (attachedLeg !== leg) return
      if (!released) {
        void handleMediaFailure("provider")
        return
      }
      attachedLeg = undefined
      if (answeredInbound?.callID === attachment.callID) {
        answeredInbound = undefined
      }
      if (
        snapshot.mediaAttachment?.callID === attachment.callID &&
        snapshot.mediaAttachment.providerLegID === attachment.providerLegID &&
        snapshot.mediaAttachment.mediaToken === attachment.mediaToken
      ) {
        publish({ mediaAttachment: undefined, muted: false })
      }
    })
  }

  function applyCall(call: CallingCall | undefined) {
    const current = snapshot.activeCall
    const observed = newestObservedCall(snapshot, call?.id)
    if (call && ignoredCallIDs.has(call.id)) return false
    if (call && snapshot.expectedCallID && call.id !== snapshot.expectedCallID) return false
    if (
      call &&
      (snapshot.terminalVersions[call.id] ?? -1) >= call.version
    ) {
      return false
    }
    if (
      call &&
      observed &&
      (call.version < observed.version ||
        (call.version === observed.version &&
          callSettled(observed) &&
          !callSettled(call)))
    ) {
      return false
    }
    if (call && current && current.id !== call.id && !callSettled(current)) return false
    const settled = call ? callSettled(call) : false
    const needsDisposition = call?.state === "NEEDS_DISPOSITION"
    const closed =
      call?.state === "RESOLVED" || call?.state === "FOLLOW_UP_REQUIRED"
    const settledMedia =
      settled && call && snapshot.mediaAttachment?.callID === call.id
        ? snapshot.mediaAttachment
        : undefined
    const settledLeg =
      settledMedia &&
      attachedLeg?.providerLegID === settledMedia.providerLegID &&
      attachedLeg.mediaToken === settledMedia.mediaToken
        ? attachedLeg
        : undefined
    if (settledMedia) {
      if (settledLeg) {
        purgeAttachedMedia(settledMedia, settledLeg)
      } else if (answeredInbound?.callID === settledMedia.callID) {
        answeredInbound = undefined
      }
    }
    persistCallRecoveryID(call && !closed ? call.id : undefined)
    const terminalVersions =
      call && settled
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
      activeCall: needsDisposition || closed ? undefined : call,
      pendingDisposition: needsDisposition
        ? call
        : closed && snapshot.pendingDisposition?.id === call?.id
          ? undefined
          : snapshot.pendingDisposition,
      pendingCall: call ? undefined : snapshot.pendingCall,
      terminalVersions,
      recoveryMedia,
      expectedCallID: settled ? "" : (call?.id ?? snapshot.expectedCallID),
      activeCallLegID: settled ? "" : snapshot.activeCallLegID,
      expectedMedia: settled ? undefined : snapshot.expectedMedia,
      mediaAttachment:
        settledMedia && !settledLeg ? undefined : snapshot.mediaAttachment,
      muted: settledMedia && !settledLeg ? false : snapshot.muted,
      endingCallID:
        !call || settled || snapshot.endingCallID !== call.id
          ? ""
          : snapshot.endingCallID,
    })
    return true
  }

  async function discardUnprojectedInboundCall(callID: string) {
    ignoredCallIDs.add(callID)
    persistCallRecoveryID(undefined)
    const losingLegs = new Set<IncomingMediaLeg>()
    if (attachedLeg) losingLegs.add(attachedLeg)
    attachedLeg = undefined
    if (answeredInbound?.callID === callID) answeredInbound = undefined
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
      activeCall: undefined,
      pendingCall: undefined,
      pendingDisposition: undefined,
      expectedCallID: "",
      activeCallLegID: "",
      expectedMedia: undefined,
      recoveryMedia: persistMediaCorrelation(undefined),
      mediaAttachment: undefined,
      muted: false,
      endingCallID: "",
    })
    syncAttention(offers)
    await Promise.all([
      releaseLocalMedia(),
      ...[...losingLegs].map(rejectSafely),
    ])
    if (!stopped && snapshot.lease?.owner) {
      await commitReadiness(false, true)
    }
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
    const generation = lifecycleGeneration
    expireOffers()
    try {
      const result = await backendRequest((signal) =>
        options.backend.readState({ etag }, signal),
      )
      if (result.etag) etag = result.etag
      if (stopped || generation !== lifecycleGeneration) return
      if (result.status === "not-modified") {
        if (snapshot.failure?.kind === "temporary-request") {
          publish({ failure: undefined })
        }
        if (snapshot.expectedCallID) {
          const call = await backendRequest((signal) =>
            options.backend.readCall(snapshot.expectedCallID, signal),
          )
          if (!stopped && generation === lifecycleGeneration) applyCall(call)
        }
        rejectExpiredUnmatchedMedia(correlatedMediaTokens())
        temporaryFailures = 0
        return
      }
      const state = result.state
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
        (!answeredInbound || state.ringing.length === 0)
      ) {
        await discardUnprojectedInboundCall(expectedCallID)
        if (stopped || generation !== lifecycleGeneration) return
        expectedCallID = ""
      }
      let activeCallLegID =
        expectedCallID && state.bridged?.callId === expectedCallID
          ? state.bridged.callLegId
          : snapshot.activeCallLegID
      let answeredLegLost = false
      if (answeredInbound) {
        const answeredIdentity = answeredInbound
        const exactBridge =
          state.bridged?.callId === answeredInbound.callID &&
          state.bridged.callLegId === answeredInbound.callLegID
        const exactDisposition =
          state.disposition?.callId === answeredInbound.callID &&
          state.disposition.callLegId === answeredInbound.callLegID
        const stillRinging = state.ringing.some(
          (leg) =>
            leg.callId === answeredInbound!.callID &&
            leg.callLegId === answeredInbound!.callLegID,
        )
        answeredLegLost = !exactBridge && !exactDisposition && !stillRinging
        if ((exactBridge || exactDisposition) && !stillRinging) {
          const losingLegs = [...incomingMedia.values()].filter(
            (leg) =>
              !exactBridge || !sameMediaIdentity(answeredIdentity, leg),
          )
          incomingMedia.clear()
          void Promise.all(losingLegs.map(rejectSafely))
        }
        if (answeredLegLost) {
          ignoredCallIDs.add(answeredInbound.callID)
          const losingLeg = attachedLeg
          const losingAttachment = snapshot.mediaAttachment
          const losingMedia =
            losingLeg &&
            losingAttachment &&
            losingLeg.providerLegID === losingAttachment.providerLegID &&
            losingLeg.mediaToken === losingAttachment.mediaToken
              ? { attachment: losingAttachment, leg: losingLeg }
              : undefined
          if (!losingMedia) attachedLeg = undefined
          answeredInbound = undefined
          expectedCallID = ""
          publish({
            activeCall: undefined,
            pendingCall: undefined,
            expectedCallID: "",
            activeCallLegID: "",
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
            dispositionLeg.providerLegID === dispositionAttachment.providerLegID &&
            dispositionLeg.mediaToken === dispositionAttachment.mediaToken
          ) {
            purgeAttachedMedia(dispositionAttachment, dispositionLeg)
          } else {
            attachedLeg = undefined
            answeredInbound = undefined
            publish({ mediaAttachment: undefined, muted: false })
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
        ? state.ringing
            .filter(
              (offer) => new Date(offer.deadline).getTime() > clock.now,
            )
            .map((offer) => ({
              ...offer,
              answerReady: exactIncomingLeg(offer) !== undefined,
            }))
        : []
      publish({
        lease,
        offers,
        staffTransfers: lease.owner ? state.staffTransfers : [],
        transferCandidates:
          lease.owner && !ownershipMoved ? snapshot.transferCandidates : [],
        activeCall:
          lease.owner && !ownershipMoved ? snapshot.activeCall : undefined,
        pendingCall:
          lease.owner && !ownershipMoved ? snapshot.pendingCall : undefined,
        pendingDisposition:
          lease.owner && !ownershipMoved
            ? snapshot.pendingDisposition
            : undefined,
        pending: lease.owner
          ? {
              ...snapshot.pending,
              transfer: ownershipMoved ? false : snapshot.pending.transfer,
            }
          : {
              availability: false,
              retry: false,
              disposition: false,
              transfer: false,
            },
        endingCallID: lease.owner ? snapshot.endingCallID : "",
        expectedCallID,
        activeCallLegID: lease.owner ? activeCallLegID : "",
        committedOwner: Boolean(
          lease.owner &&
            expectedCallID &&
            state.bridged?.callId === expectedCallID &&
            state.bridged.callLegId === activeCallLegID,
        ),
        expectedMedia: lease.owner ? expectedMedia : undefined,
        recoveryMedia: lease.owner
          ? snapshot.recoveryMedia
          : persistMediaCorrelation(undefined),
        failure: accessBlocked
          ? snapshot.failure
          : !lease.owner
          ? ownershipFailure(lease, ownershipLost)
          : snapshot.failure?.kind === "temporary-request" ||
              snapshot.failure?.kind === "ownership"
            ? undefined
            : snapshot.failure,
      })
      syncAttention(offers)
      if (ownershipLost || ownershipMoved) await releaseLocalMedia()
      if (ownershipMoved && !stopped && lease.owner) void connectMedia()
      const pendingID = lease.owner
        ? lease.pendingOutcomeCallId || state.disposition?.callId
        : ""
      if (pendingID) {
        const pending = await backendRequest((signal) =>
          options.backend.readCall(pendingID, signal),
        )
        if (
          !stopped &&
          generation === lifecycleGeneration &&
          pending.state === "NEEDS_DISPOSITION"
        ) {
          applyCall(pending)
        }
      } else if (snapshot.pendingDisposition) {
        publish({ pendingDisposition: undefined })
      }
      if (expectedCallID && !answeredLegLost) {
        const call = await backendRequest((signal) =>
          options.backend.readCall(expectedCallID, signal),
        )
        if (!stopped && generation === lifecycleGeneration) {
          if (!expectedCallProjected && call.direction === "INBOUND") {
            await discardUnprojectedInboundCall(call.id)
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
    } catch (error) {
      if (stopped || generation !== lifecycleGeneration) return
      temporaryFailures += 1
      await setRequestFailure(error)
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

  function scheduleRefresh() {
    if (stopped) return
    if (refreshTimer !== undefined) clock.clearTimeout(refreshTimer)
    const delay = temporaryFailures
      ? Math.min(
          visibility.isHidden() ? 12_000 : 8_000,
          500 * 2 ** (temporaryFailures - 1),
        )
      : incomingMedia.size > 0
        ? mediaConfirmationRetryMilliseconds
        : refreshDelay(snapshot, visibility.isHidden())
    refreshTimer = clock.setTimeout(() => {
      refreshTimer = undefined
      void requestRefresh(false).finally(scheduleRefresh)
    }, delay)
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
      })().finally(() =>
        scheduleHeartbeat(
          snapshot.failure?.kind === "temporary-request"
            ? 500
            : heartbeatDelayMilliseconds,
        ),
      )
    }, delay)
  }

  function queueReadiness(
    permitAvailability: boolean,
    forceUnregistered = false,
    pendingVisibility: "visible" | "silent" = "visible",
  ) {
    const generation = ++readinessGeneration
    if (pendingVisibility === "visible") {
      publish({ pending: { ...snapshot.pending, availability: true } })
    }
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
    requestedReadiness = {
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
    return generation
  }

  async function commitReadiness(
    permitAvailability = true,
    forceUnregistered = false,
    pendingVisibility: "visible" | "silent" = "visible",
  ) {
    const generation = queueReadiness(
      permitAvailability,
      forceUnregistered,
      pendingVisibility,
    )
    await drainReadiness()
    return readinessConfirmedGeneration >= generation
  }

  async function drainReadiness(): Promise<void> {
    if (readinessInFlight) {
      await readinessInFlight
      if (!stopped && requestedReadiness) await drainReadiness()
      return
    }
    const operation = (async () => {
      while (!stopped && requestedReadiness) {
        const request = requestedReadiness
        const generation = readinessGeneration
        requestedReadiness = undefined
        try {
          const lease = leaseForSession(
            await backendRequest((signal) =>
              options.backend.writeReadiness(request, signal),
            ),
            options.sessionID,
          )
          readinessConfirmedGeneration = Math.max(
            readinessConfirmedGeneration,
            generation,
          )
          if (stopped) return
          if (generation !== readinessGeneration) continue
          const visibleLease = accessBlocked
            ? hideLeaseAfterAccessFailure(lease)!
            : lease
          publish({
            lease: visibleLease,
            activeCall: visibleLease.owner ? snapshot.activeCall : undefined,
            pendingCall: visibleLease.owner ? snapshot.pendingCall : undefined,
            pendingDisposition: visibleLease.owner
              ? snapshot.pendingDisposition
              : undefined,
            expectedCallID: visibleLease.owner ? snapshot.expectedCallID : "",
            activeCallLegID: visibleLease.owner ? snapshot.activeCallLegID : "",
            expectedMedia: visibleLease.owner ? snapshot.expectedMedia : undefined,
            recoveryMedia: visibleLease.owner
              ? snapshot.recoveryMedia
              : persistMediaCorrelation(undefined),
            pending: { ...snapshot.pending, availability: false },
            failure: accessBlocked
              ? snapshot.failure
              : visibleLease.owner
              ? request.registered &&
                request.microphoneReady &&
                request.audioReady &&
                request.sessionHealthy &&
                !readinessFailClosed
                ? undefined
                : snapshot.failure
              : ownershipFailure(visibleLease),
          })
          if (!visibleLease.owner) await releaseLocalMedia()
        } catch (error) {
          if (stopped) return
          if (generation !== readinessGeneration) continue
          publish({ pending: { ...snapshot.pending, availability: false } })
          await setCommandFailure(error)
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
    const generation = ++microphoneGeneration
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
          if (stopped || generation !== microphoneGeneration) return
          publish({
            readiness: {
              ...snapshot.readiness,
              microphoneReady: false,
            },
            failure: {
              kind: "technical-readiness",
              message: "Your microphone became unavailable. Reconnect it and try again.",
              recoverable: true,
            },
          })
          void releaseLocalMedia()
            .then(() => commitReadiness(false))
            .then(() => signalRefresh())
        }, signal),
        signal,
        (lateMicrophone) => lateMicrophone.stop(),
      )
      if (stopped || generation !== microphoneGeneration) {
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
      if (
        stopped ||
        generation !== microphoneGeneration ||
        !snapshot.lease?.owner
      ) {
        return
      }
      const connectionCurrent = () =>
        !stopped &&
        generation === microphoneGeneration &&
        snapshot.lease?.owner === true
      let mediaStateSynchronization = Promise.resolve()
      await abortableOperation(
        options.media.connect(
          token,
          options.remoteElementID ?? "calling-remote-audio",
          {
            onState: (mediaState) => {
              if (!connectionCurrent()) return
              publish({
                readiness: {
                  ...snapshot.readiness,
                  mediaState,
                  audioReady: mediaState === "ready",
                  sessionHealthy: mediaState === "ready",
                },
                failure:
                  mediaState === "reconnecting"
                    ? {
                        kind: "media",
                        message: "Call audio is reconnecting. Reconnect calling if it does not recover.",
                        recoverable: true,
                      }
                    : mediaState === "ready" &&
                        snapshot.failure?.kind === "media"
                      ? undefined
                      : snapshot.failure,
              })
              mediaStateSynchronization = commitReadiness(
                mediaState === "ready",
              ).then(() => signalRefresh())
            },
            onIncoming: (leg) => {
              if (connectionCurrent()) void handleIncomingMedia(leg, generation)
              else void rejectSafely(leg)
            },
            onEnded: (leg) => {
              if (!connectionCurrent()) return
              const remembered = incomingMedia.get(leg.mediaToken)
              if (remembered?.providerLegID === leg.providerLegID) {
                incomingMedia.delete(leg.mediaToken)
                publish({
                  offers: snapshot.offers.map((offer) =>
                    offer.mediaToken === leg.mediaToken
                      ? { ...offer, answerReady: false }
                      : offer,
                  ),
                })
              }
              if (
                attachedLeg?.providerLegID === leg.providerLegID &&
                attachedLeg.mediaToken === leg.mediaToken
              ) {
                attachedLeg = undefined
                publish({ mediaAttachment: undefined, muted: false })
              }
              void signalRefresh()
            },
            onAudioIssue: () => {
              if (!connectionCurrent()) return
              setFailure(
                new SoftphoneAdapterError(
                  "media",
                  "Call audio was interrupted. Reconnect calling and try again.",
                ),
              )
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
                  await setRequestFailure(error, "authentication")
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
      const attemptCurrent =
        !stopped &&
        generation === microphoneGeneration &&
        snapshot.lease?.owner === true
      if (!attemptCurrent) return
      if (failureFrom(error, "technical-readiness").kind === "access") {
        await setRequestFailure(error, "technical-readiness")
        return
      }
      microphoneGeneration += 1
      await disconnectMedia()
      microphone?.stop()
      microphone = undefined
      publish({
        readiness: {
          mediaState: "unavailable",
          microphoneReady: false,
          audioReady: false,
          sessionHealthy: false,
        },
      })
      setFailure(error, "technical-readiness")
    }
  }

  function mediaContinuationCurrent(generation: number) {
    return (
      !stopped &&
      generation === microphoneGeneration &&
      snapshot.lease?.owner === true
    )
  }

  function commandContinuationCurrent(generation: number) {
    return (
      !stopped &&
      generation === lifecycleGeneration &&
      snapshot.lease?.owner === true &&
      snapshot.lease.sessionId === options.sessionID
    )
  }

  async function reconcileStaleCommand(generation: number) {
    if (!stopped && generation === lifecycleGeneration) {
      await signalRefresh()
    }
  }

  function signalStaffIntent() {
    void signalRefresh()
  }

  async function reconcileCommandFailure(
    error: unknown,
    generation: number,
    committed: () => boolean,
  ) {
    const failure = failureFrom(error, "temporary-request")
    if (failure.kind === "conflict" || failure.kind === "temporary-request") {
      await signalRefresh()
      if (!commandContinuationCurrent(generation) || committed()) return true
    }
    await setCommandFailure(error)
    return false
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
    generation: number,
  ) {
    if (
      !mediaContinuationCurrent(generation) ||
      snapshot.readiness.mediaState !== "ready"
    ) {
      await rejectSafely(leg)
      return
    }
    if (attachedLeg) {
      const recoversCurrent =
        leg.recovery && sameMediaIdentity(attachedLeg, leg)
      if (!recoversCurrent) {
        await rejectSafely(leg)
        return
      }
      let outcome: "attached" | "ended"
      try {
        outcome = await answerMediaLeg(leg)
      } catch {
        if (!mediaContinuationCurrent(generation)) {
          await discardStaleMedia(leg)
          return
        }
        attachedLeg = undefined
        await rejectSafely(leg)
        publish({
          mediaAttachment: undefined,
          muted: false,
          failure: {
            kind: "media",
            message: "Call audio could not reconnect. Reconnect calling to restore audio.",
            recoverable: true,
          },
        })
        await signalRefresh()
        return
      }
      if (!mediaContinuationCurrent(generation)) {
        await discardStaleMedia(leg)
        return
      }
      if (outcome !== "attached") {
        attachedLeg = undefined
        publish({
          mediaAttachment: undefined,
          muted: false,
          failure: {
            kind: "media",
            message: "Call audio could not reconnect. Reconnect calling to restore audio.",
            recoverable: true,
          },
        })
        await signalRefresh()
        return
      }
      attachedLeg = leg
      publish({
        mediaAttachment: snapshot.mediaAttachment,
        failure:
          snapshot.failure?.kind === "media" ? undefined : snapshot.failure,
      })
      await signalRefresh()
      if (!mediaContinuationCurrent(generation)) {
        await discardStaleMedia(leg)
      }
      return
    }
    const existing = incomingMedia.get(leg.mediaToken)
    if (existing && existing.providerLegID !== leg.providerLegID) {
      await rejectSafely(leg)
      return
    }
    incomingMedia.set(leg.mediaToken, leg)
    incomingMediaDeadlines.set(
      leg,
      clock.now + mediaCorrelationWindowMilliseconds,
    )
    await signalRefresh()
    if (!mediaContinuationCurrent(generation)) {
      await discardStaleMedia(leg)
      return
    }
    if (
      answeredInbound &&
      (answeredInbound.providerLegID !== leg.providerLegID ||
        answeredInbound.mediaToken !== leg.mediaToken)
    ) {
      return
    }
    if (attachedLeg) {
      if (
        sameMediaIdentity(attachedLeg, leg) &&
        incomingMedia.get(leg.mediaToken) === leg
      ) {
        incomingMedia.delete(leg.mediaToken)
      }
      return
    }
    if (
      snapshot.expectedCallID &&
      (snapshot.expectedMedia || leg.recovery)
    ) {
      await attachExpectedMedia(leg, generation)
      return
    }
    const matchingOffer = snapshot.offers.find(
      (offer) => offer.mediaToken === leg.mediaToken,
    )
    if (matchingOffer) {
      publish({
        offers: snapshot.offers.map((offer) =>
          offer.callLegId === matchingOffer.callLegId
            ? { ...offer, answerReady: true }
            : offer,
        ),
      })
      return
    }
    return
  }

  async function attachExpectedMedia(
    leg: IncomingMediaLeg,
    generation = microphoneGeneration,
  ) {
    if (!mediaContinuationCurrent(generation)) {
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
      snapshot.recoveryMedia.providerLegID === leg.providerLegID &&
      snapshot.recoveryMedia.mediaToken === leg.mediaToken
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
    let outcome: "attached" | "ended"
    try {
      outcome = await answerMediaLeg(leg)
    } catch (error) {
      if (!mediaContinuationCurrent(generation)) {
        await discardStaleMedia(leg)
        return
      }
      await rejectSafely(leg)
      setFailure(error, "media")
      await signalRefresh()
      return
    }
    if (!mediaContinuationCurrent(generation)) {
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
        : await confirmExpectedMedia(callID, leg, generation)
      if (!call) {
        await discardStaleMedia(leg)
        return
      }
      if (
        !mediaContinuationCurrent(generation) ||
        snapshot.expectedCallID !== callID
      ) {
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
        publish({ mediaAttachment })
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
      publish({
        recoveryMedia: persistMediaCorrelation(mediaAttachment),
        mediaAttachment,
        activeCallLegID: confirmedOwnerLegID,
        committedOwner: Boolean(
          call.state === "CONNECTED" &&
            mediaAttachment.callLegID &&
            mediaAttachment.callLegID === confirmedOwnerLegID,
        ),
        failure: undefined,
      })
    } catch (error) {
      await rejectSafely(leg)
      if (mediaContinuationCurrent(generation)) {
        await setRequestFailure(error, "conflict")
      }
    }
  }

  async function confirmExpectedMedia(
    callID: string,
    leg: IncomingMediaLeg,
    generation: number,
  ) {
    const deadline = clock.now + backendRequestTimeoutMilliseconds
    let lastError: unknown
    while (
      mediaContinuationCurrent(generation) &&
      snapshot.expectedCallID === callID &&
      clock.now < deadline
    ) {
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
      if (
        !mediaContinuationCurrent(generation) ||
        snapshot.expectedCallID !== callID
      ) {
        break
      }
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
    answeredInbound = undefined
    publish({
      readiness: {
        ...snapshot.readiness,
        mediaState: "unavailable",
        audioReady: false,
        sessionHealthy: false,
      },
      mediaAttachment: undefined,
      muted: false,
      failure: visibleFailure,
    })
    await releaseLocalMedia()
    if (stopped) return
    await commitReadiness(false)
    if (!stopped && snapshot.lease?.owner) {
      publish({ failure: visibleFailure })
      scheduleHeartbeat(500)
    }
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
    readinessGeneration += 1
    microphoneGeneration += 1
    clearConfirmationWaits()
    for (const controller of mediaEffectControllers) controller.abort()
    incomingMedia.clear()
    attachedLeg = undefined
    answeredInbound = undefined
    const disconnecting = disconnectMedia()
    microphone?.stop()
    microphone = undefined
    publish({
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

  async function signalRefresh() {
    if (refreshTimer !== undefined) clock.clearTimeout(refreshTimer)
    refreshTimer = undefined
    await requestRefresh(true)
    scheduleRefresh()
  }

  async function takeOver() {
    if (accessBlocked) return
    if (!snapshot.lease?.owner && snapshot.lease?.activeCallId) {
      publish({ failure: ownershipFailure(snapshot.lease) })
      return
    }
    try {
      const lease = leaseForSession(
        await backendRequest((signal) =>
          options.backend.acquireLease(
            {
              sessionID: options.sessionID,
              takeover: true,
            },
            signal,
          ),
        ),
        options.sessionID,
      )
      if (stopped) return
      publish({
        lease,
        failure: lease.owner ? undefined : ownershipFailure(lease),
        expectedCallID: lease.owner
          ? lease.activeCallId || snapshot.expectedCallID || recoveryCallID
          : "",
      })
      if (!lease.owner) return
      await signalRefresh()
      await connectMedia()
    } catch (error) {
      await setCommandFailure(error)
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
      const generation = ++lifecycleGeneration
      publish({
        phase: "starting",
        activeCallLegID: snapshot.recoveryMedia?.callLegID ?? "",
        failure: undefined,
      })
      unsubscribeVisibility = visibility.subscribe(() => {
        if (!stopped) {
          syncAttention(snapshot.offers)
          void signalRefresh()
        }
      })
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
        if (stopped || generation !== lifecycleGeneration) return
        publish({
          lease,
          expectedCallID: lease.owner
            ? lease.activeCallId || snapshot.expectedCallID || recoveryCallID
            : "",
          failure: lease.owner
            ? undefined
            : ownershipFailure(lease),
        })
        await requestRefresh(true)
        if (stopped || generation !== lifecycleGeneration) return
        if (lease.owner) await connectMedia()
        if (stopped || generation !== lifecycleGeneration) return
        publish({ phase: "running" })
        scheduleRefresh()
        scheduleHeartbeat()
      } catch (error) {
        await setCommandFailure(error)
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
        lifecycleGeneration += 1
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
        for (const request of backendRequests) request.abort()
        unsubscribeVisibility?.()
        unsubscribeVisibility = undefined
        await releaseLocalMedia()
        publish({
          phase: "stopped",
          lease: undefined,
          offers: [],
          staffTransfers: [],
          transferCandidates: [],
          activeCall: undefined,
          pendingCall: undefined,
          pendingDisposition: undefined,
          expectedCallID: "",
          activeCallLegID: "",
          expectedMedia: undefined,
          mediaAttachment: undefined,
          muted: false,
          endingCallID: "",
          committedOwner: false,
          pending: {
            availability: false,
            retry: false,
            disposition: false,
            transfer: false,
          },
          failure: undefined,
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
          publish({
            failure: {
              ...failure,
              message: `Calling stopped locally, but backend readiness could not be cleared. ${failure.message}`,
              recoverable: false,
            },
          })
        }
      })().finally(() => {
        stopInFlight = undefined
      })
      return stopInFlight
    },
    signalRefresh,
    async setAvailability(available) {
      if (accessBlocked) {
        publish({ availabilityIntent: available })
        options.persistAvailabilityIntent?.(available)
        return
      }
      publish({ availabilityIntent: available, failure: undefined })
      options.persistAvailabilityIntent?.(available)
      if (!snapshot.lease?.owner && available) await takeOver()
      if (snapshot.lease?.owner && snapshot.readiness.mediaState === "unavailable") {
        await connectMedia()
      }
      await commitReadiness()
      await signalRefresh()
    },
    async startOutbound(input) {
      if (!snapshot.lease?.owner || snapshot.readiness.mediaState !== "ready") {
        setFailure(
          new SoftphoneAdapterError(
            "technical-readiness",
            "Reconnect calling before starting a Call.",
          ),
        )
        return
      }
      if (snapshot.occupied) {
        setFailure(new SoftphoneAdapterError("conflict", "Finish the current Call before starting another."))
        return
      }
      const generation = lifecycleGeneration
      publish({
        pendingCall: pendingOutboundCall(input),
        failure: undefined,
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
        if (!commandContinuationCurrent(generation)) {
          await reconcileStaleCommand(generation)
          return
        }
        publish({
          expectedCallID: call.id,
          pendingCall: undefined,
        })
        applyCall(call)
        await commitReadiness(false)
        await signalRefresh()
      } catch (error) {
        if (!commandContinuationCurrent(generation)) {
          await reconcileStaleCommand(generation)
          return
        }
        const committed = await reconcileCommandFailure(
          error,
          generation,
          () => Boolean(snapshot.expectedCallID),
        )
        publish({
          pendingCall: undefined,
        })
        if (committed) return
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
        attachedLeg ||
        answeredInbound
      ) {
        return
      }
      incomingMedia.delete(offer.mediaToken)
      const answering = {
        callID: offer.callId,
        callLegID: offer.callLegId,
        providerLegID: leg.providerLegID,
        mediaToken: leg.mediaToken,
      }
      const generation = microphoneGeneration
      answeredInbound = answering
      publish({
        pendingCall: pendingInboundCall(offer),
        offers: snapshot.offers.filter(
          (candidate) => candidate.callLegId !== callLegID,
        ),
        failure: undefined,
      })
      syncAttention(snapshot.offers)
      signalStaffIntent()
      let outcome
      try {
        outcome = await answerMediaLeg(leg)
      } catch (error) {
        if (
          !mediaContinuationCurrent(generation) ||
          answeredInbound !== answering
        ) {
          await discardStaleMedia(leg)
          return
        }
        if (answeredInbound === answering) answeredInbound = undefined
        if (mediaEffectTimedOut(error)) {
          publish({
            pendingCall: undefined,
            offers: [
              ...snapshot.offers.filter(
                (candidate) => candidate.callLegId !== offer.callLegId,
              ),
              { ...offer, answerReady: false },
            ],
          })
          await commitReadiness(false)
          setFailure(error, "media")
          await signalRefresh()
          void rejectSafely(leg)
          return
        }
        incomingMedia.set(offer.mediaToken, leg)
        publish({
          pendingCall: undefined,
          offers: [
            ...snapshot.offers.filter(
              (candidate) => candidate.callLegId !== offer.callLegId,
            ),
            { ...offer, answerReady: true },
          ],
        })
        setFailure(error, "media")
        return
      }
      if (outcome !== "attached") {
        if (
          !mediaContinuationCurrent(generation) ||
          answeredInbound !== answering
        ) {
          await discardStaleMedia(leg)
          return
        }
        publish({ pendingCall: undefined })
        await signalRefresh()
        if (answeredInbound === answering) answeredInbound = undefined
        return
      }
      if (
        !mediaContinuationCurrent(generation) ||
        answeredInbound !== answering
      ) {
        await discardStaleMedia(leg)
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
      publish({
        expectedCallID: offer.callId,
        activeCallLegID: offer.callLegId,
        offers: [],
        recoveryMedia: persistMediaCorrelation(mediaAttachment),
        mediaAttachment,
        failure: undefined,
      })
      syncAttention(snapshot.offers)
      if (
        !mediaContinuationCurrent(generation) ||
        answeredInbound !== answering ||
        attachedLeg !== leg
      ) {
        return
      }
      await commitReadiness(false)
      await signalRefresh()
    },
    async loadTransferCandidates() {
      const call = snapshot.activeCall
      if (!call || !snapshot.controls.canTransfer) return
      publish({
        pending: { ...snapshot.pending, transfer: true },
        failure: undefined,
      })
      try {
        const candidates = await backendRequest((signal) =>
          options.backend.listTransferCandidates(
            { callID: call.id, sessionID: options.sessionID },
            signal,
          ),
        )
        if (snapshot.activeCall?.id !== call.id) return
        publish({
          transferCandidates: candidates,
          pending: { ...snapshot.pending, transfer: false },
        })
      } catch (error) {
        publish({
          transferCandidates: [],
          pending: { ...snapshot.pending, transfer: false },
        })
        await setRequestFailure(error)
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
      publish({
        pending: { ...snapshot.pending, transfer: true },
        failure: undefined,
      })
      signalStaffIntent()
      try {
        const transfer = await backendRequest((signal) =>
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
        publish({
          staffTransfers: [
            ...snapshot.staffTransfers.filter(
              (current) => current.id !== transfer.id,
            ),
            transfer,
          ],
          transferCandidates: [],
          pending: { ...snapshot.pending, transfer: false },
        })
        await signalRefresh()
      } catch (error) {
        await requestRefresh(true)
        const committed = snapshot.staffTransfers.some(
          (transfer) =>
            transfer.callId === call.id &&
            transfer.recipientSubject === recipientSubject &&
            transfer.handoffNote === normalizedNote,
        )
        publish({
          transferCandidates: committed ? [] : snapshot.transferCandidates,
          pending: { ...snapshot.pending, transfer: false },
        })
        if (!committed) await setRequestFailure(error)
      }
    },
    async cancelTransfer() {
      const transfer = activeSourceTransfer()
      if (!transfer || snapshot.pending.transfer) return
      publish({
        pending: { ...snapshot.pending, transfer: true },
        failure: undefined,
      })
      try {
        await backendRequest((signal) =>
          options.backend.cancelTransfer(
            { transferID: transfer.id, sessionID: options.sessionID },
            signal,
          ),
        )
        publish({
          staffTransfers: snapshot.staffTransfers.filter(
            (current) => current.id !== transfer.id,
          ),
          pending: { ...snapshot.pending, transfer: false },
        })
        await signalRefresh()
      } catch (error) {
        publish({ pending: { ...snapshot.pending, transfer: false } })
        await setRequestFailure(error)
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
      publish({
        pending: { ...snapshot.pending, transfer: true },
        failure: undefined,
      })
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
        const leg = exactIncomingLeg(offer)
        incomingMedia.delete(offer.mediaToken)
        publish({
          offers: snapshot.offers.filter(
            (candidate) => candidate.callLegId !== callLegID,
          ),
          staffTransfers: snapshot.staffTransfers.filter(
            (transfer) => transfer.id !== offer.staffTransferId,
          ),
          pending: { ...snapshot.pending, transfer: false },
        })
        syncAttention(snapshot.offers)
        if (leg) await rejectSafely(leg)
        await signalRefresh()
      } catch (error) {
        publish({ pending: { ...snapshot.pending, transfer: false } })
        await setRequestFailure(error)
      }
    },
    async hangup() {
      const call = snapshot.activeCall
      if (!call || snapshot.endingCallID) return
      const generation = lifecycleGeneration
      publish({ endingCallID: call.id, failure: undefined })
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
        if (!commandContinuationCurrent(generation)) {
          await reconcileStaleCommand(generation)
          return
        }
        applyCall(committed)
        await signalRefresh()
      } catch (error) {
        if (stopped || generation !== lifecycleGeneration) return
        await signalRefresh()
        if (!commandContinuationCurrent(generation)) return
        if (
          snapshot.pendingDisposition ||
          snapshot.activeCall?.endRequested ||
          callSettled(snapshot.activeCall)
        ) {
          return
        }
        if (!snapshot.activeCall?.endRequested && !callSettled(snapshot.activeCall)) {
          publish({ endingCallID: "" })
        }
        await setCommandFailure(error)
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
      const generation = lifecycleGeneration
      publish({ pending: { ...snapshot.pending, retry: true }, failure: undefined })
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
        if (!commandContinuationCurrent(generation)) {
          await reconcileStaleCommand(generation)
          return
        }
        await releaseLocalMedia()
        if (!commandContinuationCurrent(generation)) {
          await reconcileStaleCommand(generation)
          return
        }
        publish({
          expectedCallID: retried.id,
          activeCall: undefined,
          recoveryMedia: persistMediaCorrelation(undefined),
          pending: { ...snapshot.pending, retry: false },
          failure: undefined,
        })
        applyCall(retried)
        await connectMedia()
        await signalRefresh()
      } catch (error) {
        if (!commandContinuationCurrent(generation)) {
          await reconcileStaleCommand(generation)
          return
        }
        const committed = await reconcileCommandFailure(
          error,
          generation,
          () =>
            Boolean(
              snapshot.expectedCallID && snapshot.expectedCallID !== call.id,
            ),
        )
        publish({ pending: { ...snapshot.pending, retry: false } })
        if (committed) return
      }
    },
    async recover() {
      if (mediaFailureInFlight) await mediaFailureInFlight
      const failure = snapshot.failure
      if (!snapshot.lease?.owner || failure?.kind === "ownership") {
        await takeOver()
        return
      }
      publish({ failure: undefined })
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
        if (snapshot.readiness.mediaState === "ready") {
          readinessFailClosed = false
          await commitReadiness()
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
      answeredInbound = undefined
      persistCallRecoveryID(undefined)
      publish({
        activeCall: undefined,
        mediaAttachment: undefined,
        muted: false,
        failure: undefined,
      })
      void commitReadiness()
    },
    async dispose(outcome) {
      const call = snapshot.pendingDisposition
      if (!call || snapshot.pending.disposition) return
      const generation = lifecycleGeneration
      publish({ pending: { ...snapshot.pending, disposition: true }, failure: undefined })
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
        if (!commandContinuationCurrent(generation)) {
          await reconcileStaleCommand(generation)
          return
        }
        applyCall(result.call)
        publish({
          pendingDisposition: undefined,
          pending: { ...snapshot.pending, disposition: false },
        })
        await commitReadiness()
        await signalRefresh()
        return result
      } catch (error) {
        if (!commandContinuationCurrent(generation)) {
          await reconcileStaleCommand(generation)
          return
        }
        const committed = await reconcileCommandFailure(
          error,
          generation,
          () => !snapshot.pendingDisposition,
        )
        publish({ pending: { ...snapshot.pending, disposition: false } })
        if (committed) return
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

  function persistMediaCorrelation(
    correlation: RuntimeMediaCorrelation | undefined,
  ) {
    options.persistMediaCorrelation?.(correlation)
    return correlation
  }
}

function derive(
  snapshot: Omit<SoftphoneRuntimeSnapshot, "controls" | "occupied"> & {
    occupied?: boolean
    controls?: SoftphoneRuntimeSnapshot["controls"]
  },
): SoftphoneRuntimeSnapshot {
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

function callSettled(call: Pick<CallingCall, "state"> | undefined) {
  return Boolean(
    call &&
      [
        "UNANSWERED",
        "VOICEMAIL",
        "MISSED",
        "NEEDS_DISPOSITION",
        "RESOLVED",
        "FOLLOW_UP_REQUIRED",
      ].includes(call.state),
  )
}

function newestObservedCall(
  snapshot: Pick<SoftphoneRuntimeSnapshot, "activeCall" | "pendingDisposition">,
  callID: string | undefined,
) {
  if (!callID) return undefined
  const candidates = [snapshot.activeCall, snapshot.pendingDisposition].filter(
    (call): call is CallingCall => call?.id === callID,
  )
  return candidates.reduce<CallingCall | undefined>(
    (newest, candidate) =>
      !newest || candidate.version > newest.version ? candidate : newest,
    undefined,
  )
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

function refreshDelay(snapshot: SoftphoneRuntimeSnapshot, hidden: boolean) {
  if (hidden) return 8_000
  if (snapshot.offers.length > 0) return 250
  if (
    snapshot.pendingCall ||
    snapshot.pending.retry ||
    snapshot.pending.disposition
  ) {
    return 250
  }
  if (snapshot.endingCallID || snapshot.activeCall?.endRequested) return 250
  switch (snapshot.activeCall?.state) {
    case "PREPARING":
    case "RINGING":
    case "CONNECTING":
      return 250
    case "CONNECTED":
      return 1_000
    default:
      return 4_000
  }
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
