import type {
  CallingCall,
  CallingDispositionRequest,
  CallingDispositionResult,
  CallingReadinessRequest,
  CallingState,
  RingingCallLeg,
  SoftphoneState,
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
>

export type RuntimeMediaCorrelation = {
  callID: string
  callLegID?: string
  providerLegID: string
  mediaToken: string
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
  pending: {
    availability: boolean
    outbound: boolean
    retry: boolean
    disposition: boolean
  }
  occupied: boolean
  controls: {
    canEnd: boolean
    canMute: boolean
    canKeypad: boolean
    canRetry: boolean
    canDispose: boolean
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
  }): Promise<SoftphoneState>
  writeReadiness(input: CallingReadinessRequest): Promise<SoftphoneState>
  issueMediaToken(input: { sessionID: string }): Promise<string>
  readState(input: { etag?: string }): Promise<StateRead>
  readCall(callID: string): Promise<CallingCall>
  confirmMedia(input: {
    callID: string
    sessionID: string
    mediaToken: string
  }): Promise<CallingCall>
  startOutbound(input: StartOutboundCallRequest): Promise<CallingCall>
  hangup(input: { callID: string; sessionID: string }): Promise<CallingCall>
  retry(input: {
    callID: string
    sessionID: string
    idempotencyKey: string
  }): Promise<CallingCall>
  dispose(input: {
    callID: string
    sessionID: string
    outcome: CallingDispositionRequest["outcome"]
  }): Promise<CallingDispositionResult>
}

export interface SoftphoneClock {
  now: number
  setTimeout(callback: () => void, milliseconds: number): number
  clearTimeout(id: number): void
}

export interface SoftphoneMicrophone {
  start(onUnavailable: () => void): Promise<{ stop(): void }>
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
  signalRefresh(
    reason: "staff-intent" | "incoming-media" | "media" | "visibility" | "backend",
  ): Promise<void>
  takeOver(): Promise<void>
  setAvailability(available: boolean): Promise<void>
  startOutbound(
    input: Omit<StartOutboundCallRequest, "sessionId">,
  ): Promise<void>
  answer(callLegID: string): Promise<void>
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
  const listeners = new Set<() => void>()
  const incomingMedia = new Map<string, IncomingMediaLeg>()
  const notificationStops = new Map<string, () => void>()
  let stopRingtone: (() => void) | undefined
  let microphone: { stop(): void } | undefined
  let refreshTimer: number | undefined
  let heartbeatTimer: number | undefined
  let refreshInFlight: Promise<void> | undefined
  let refreshQueued = false
  let stopped = true
  let lifecycleGeneration = 0
  let etag: string | undefined
  let temporaryFailures = 0
  let readinessGeneration = 0
  let readinessInFlight: Promise<void> | undefined
  let requestedReadiness: CallingReadinessRequest | undefined
  let unsubscribeVisibility: (() => void) | undefined
  let microphoneGeneration = 0
  let mediaConnectInFlight: Promise<void> | undefined
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
    expectedCallID: "",
    activeCallLegID: "",
    terminalVersions: {},
    recoveryMedia: restoredMedia,
    muted: false,
    endingCallID: "",
    pending: {
      availability: false,
      outbound: false,
      retry: false,
      disposition: false,
    },
  })

  function publish(update: Partial<SoftphoneRuntimeSnapshot>) {
    snapshot = derive({ ...snapshot, ...update })
    for (const listener of listeners) listener()
  }

  function setFailure(error: unknown, fallback: SoftphoneFailureKind = "temporary-request") {
    const failure = failureFrom(error, fallback)
    publish({ failure })
    return failure
  }

  async function setCommandFailure(
    error: unknown,
    fallback: SoftphoneFailureKind = "temporary-request",
  ) {
    const failure = failureFrom(error, fallback)
    if (failure.kind === "access") {
      await signalRefresh("backend")
      if (snapshot.failure?.kind === "ownership") return
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

  function applyCall(call: CallingCall | undefined) {
    const current = snapshot.activeCall
    const observed = newestObservedCall(snapshot, call?.id)
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
      endingCallID:
        !call || settled || snapshot.endingCallID !== call.id
          ? ""
          : snapshot.endingCallID,
    })
    return true
  }

  async function refreshOnce() {
    const generation = lifecycleGeneration
    try {
      const result = await options.backend.readState({ etag })
      if (result.etag) etag = result.etag
      if (stopped || generation !== lifecycleGeneration) return
      if (result.status === "not-modified") {
        if (snapshot.failure?.kind === "temporary-request") {
          publish({ failure: undefined })
        }
        if (snapshot.expectedCallID) {
          const call = await options.backend.readCall(snapshot.expectedCallID)
          if (!stopped && generation === lifecycleGeneration) applyCall(call)
        }
        temporaryFailures = 0
        return
      }
      const state = result.state
      const lease = leaseForSession(state.softphone, options.sessionID)
      const ownershipLost = snapshot.lease?.owner && !lease.owner
      let expectedCallID =
        lease.owner
          ? state.bridged?.callId ||
            lease.activeCallId ||
            snapshot.expectedCallID
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
      const activeCallLegID =
        expectedCallID && state.bridged?.callId === expectedCallID
          ? state.bridged.callLegId
          : snapshot.activeCallLegID
      let answeredLegLost = false
      if (answeredInbound) {
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
        if (answeredLegLost) {
          ignoredCallIDs.add(answeredInbound.callID)
          const losingLeg = attachedLeg
          attachedLeg = undefined
          answeredInbound = undefined
          expectedCallID = ""
          incomingMedia.clear()
          if (losingLeg) await rejectSafely(losingLeg)
          publish({
            activeCall: undefined,
            pendingCall: undefined,
            expectedCallID: "",
            activeCallLegID: "",
            expectedMedia: undefined,
            recoveryMedia: persistMediaCorrelation(undefined),
            mediaAttachment: undefined,
            muted: false,
          })
        } else if (exactDisposition) {
          attachedLeg = undefined
          answeredInbound = undefined
          publish({ mediaAttachment: undefined, muted: false })
        }
      }
      const offers = lease.owner
        ? state.ringing
            .filter((offer) => offer.callId !== expectedCallID)
            .map((offer) => ({
              ...offer,
              answerReady: exactIncomingLeg(offer) !== undefined,
            }))
        : []
      publish({
        lease,
        offers,
        activeCall: lease.owner ? snapshot.activeCall : undefined,
        pendingCall: lease.owner ? snapshot.pendingCall : undefined,
        pendingDisposition: lease.owner ? snapshot.pendingDisposition : undefined,
        pending: lease.owner
          ? snapshot.pending
          : {
              availability: false,
              outbound: false,
              retry: false,
              disposition: false,
            },
        endingCallID: lease.owner ? snapshot.endingCallID : "",
        expectedCallID,
        activeCallLegID: lease.owner ? activeCallLegID : "",
        expectedMedia: lease.owner ? expectedMedia : undefined,
        recoveryMedia: lease.owner
          ? snapshot.recoveryMedia
          : persistMediaCorrelation(undefined),
        failure: !lease.owner
          ? {
              kind: "ownership",
              message: ownershipLost
                ? "Calling ownership moved to another browser. Reconnect to continue."
                : "Calling is active in another browser. Take over to continue.",
              recoverable: true,
            }
          : snapshot.failure?.kind === "temporary-request" ||
              snapshot.failure?.kind === "ownership"
            ? undefined
            : snapshot.failure,
      })
      syncAttention(offers)
      if (ownershipLost) await releaseLocalMedia()
      const pendingID = lease.owner
        ? lease.pendingOutcomeCallId || state.disposition?.callId
        : ""
      if (pendingID) {
        const pending = await options.backend.readCall(pendingID)
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
        const call = await options.backend.readCall(expectedCallID)
        if (!stopped && generation === lifecycleGeneration) applyCall(call)
      }
      const queuedExpectedLeg = snapshot.expectedMedia
        ? incomingMedia.get(snapshot.expectedMedia.mediaToken)
        : undefined
      if (queuedExpectedLeg && !attachedLeg) {
        await attachExpectedMedia(queuedExpectedLeg)
      }
      temporaryFailures = 0
    } catch (error) {
      temporaryFailures += 1
      setFailure(error)
    }
  }

  function requestRefresh(queueIfBusy: boolean) {
    if (stopped) return Promise.resolve()
    if (refreshInFlight) {
      if (queueIfBusy) refreshQueued = true
      return refreshInFlight
    }
    refreshInFlight = (async () => {
      do {
        refreshQueued = false
        await refreshOnce()
      } while (!stopped && refreshQueued)
    })().finally(() => {
      refreshInFlight = undefined
    })
    return refreshInFlight
  }

  function scheduleRefresh() {
    if (stopped) return
    if (refreshTimer !== undefined) clock.clearTimeout(refreshTimer)
    const delay = temporaryFailures
      ? Math.min(
          visibility.isHidden() ? 12_000 : 8_000,
          500 * 2 ** (temporaryFailures - 1),
        )
      : refreshDelay(snapshot, visibility.isHidden())
    refreshTimer = clock.setTimeout(() => {
      refreshTimer = undefined
      void requestRefresh(false).finally(scheduleRefresh)
    }, delay)
  }

  function scheduleHeartbeat(delay = 3_500) {
    if (stopped || !snapshot.lease?.owner) return
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
        await commitReadiness(snapshot.availabilityIntent)
      })().finally(() =>
        scheduleHeartbeat(snapshot.failure?.kind === "temporary-request" ? 500 : 3_500),
      )
    }, delay)
  }

  async function commitReadiness(availableIntent: boolean) {
    readinessGeneration += 1
    publish({ pending: { ...snapshot.pending, availability: true } })
    const technicallyReady =
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
      available: availableIntent && technicallyReady && !occupied,
    }
    if (!readinessInFlight) {
      readinessInFlight = (async () => {
        while (!stopped && requestedReadiness) {
          const request = requestedReadiness
          const generation = readinessGeneration
          requestedReadiness = undefined
          try {
            const lease = leaseForSession(
              await options.backend.writeReadiness(request),
              options.sessionID,
            )
            if (stopped) return
            if (generation !== readinessGeneration) continue
            publish({
              lease,
              activeCall: lease.owner ? snapshot.activeCall : undefined,
              pendingCall: lease.owner ? snapshot.pendingCall : undefined,
              pendingDisposition: lease.owner
                ? snapshot.pendingDisposition
                : undefined,
              expectedCallID: lease.owner ? snapshot.expectedCallID : "",
              activeCallLegID: lease.owner ? snapshot.activeCallLegID : "",
              expectedMedia: lease.owner ? snapshot.expectedMedia : undefined,
              recoveryMedia: lease.owner
                ? snapshot.recoveryMedia
                : persistMediaCorrelation(undefined),
              pending: { ...snapshot.pending, availability: false },
              failure: lease.owner
                ? request.registered &&
                  request.microphoneReady &&
                  request.audioReady &&
                  request.sessionHealthy
                  ? undefined
                  : snapshot.failure
                : {
                    kind: "ownership",
                    message: "Calling is active in another browser.",
                    recoverable: true,
                  },
            })
            if (!lease.owner) await releaseLocalMedia()
          } catch (error) {
            if (stopped) return
            if (generation !== readinessGeneration) continue
            publish({ pending: { ...snapshot.pending, availability: false } })
            await setCommandFailure(error)
          }
        }
      })().finally(() => {
        readinessInFlight = undefined
      })
    }
    await readinessInFlight
  }

  function connectMedia() {
    if (mediaConnectInFlight) return mediaConnectInFlight
    mediaConnectInFlight = connectMediaOnce().finally(() => {
      mediaConnectInFlight = undefined
    })
    return mediaConnectInFlight
  }

  async function connectMediaOnce() {
    if (!snapshot.lease?.owner || snapshot.readiness.mediaState !== "unavailable") return
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
      const acquiredMicrophone = await options.microphone.start(() => {
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
          .then(() => signalRefresh("media"))
      })
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
      const token = await options.backend.issueMediaToken({ sessionID: options.sessionID })
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
      await options.media.connect(token, options.remoteElementID ?? "calling-remote-audio", {
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
                : mediaState === "ready" && snapshot.failure?.kind === "media"
                  ? undefined
                  : snapshot.failure,
          })
          void commitReadiness(
            mediaState === "ready" ? snapshot.availabilityIntent : false,
          )
          void signalRefresh("media")
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
          void signalRefresh("media")
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
            const token = await options.backend.issueMediaToken({
              sessionID: options.sessionID,
            })
            return connectionCurrent() ? token : undefined
          } catch (error) {
            if (connectionCurrent()) setFailure(error, "authentication")
            return undefined
          }
        },
      })
      if (!connectionCurrent()) {
        await options.media.disconnect().catch(() => undefined)
        return
      }
      scheduleHeartbeat()
    } catch (error) {
      const attemptCurrent =
        !stopped &&
        generation === microphoneGeneration &&
        snapshot.lease?.owner === true
      if (attemptCurrent) microphoneGeneration += 1
      await options.media.disconnect().catch(() => undefined)
      if (!attemptCurrent) return
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
      await signalRefresh("staff-intent")
    }
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
        leg.recovery &&
        attachedLeg.providerLegID === leg.providerLegID &&
        attachedLeg.mediaToken === leg.mediaToken
      if (!recoversCurrent) {
        await rejectSafely(leg)
        return
      }
      const outcome = await leg.answer().catch(() => "ended" as const)
      if (!mediaContinuationCurrent(generation)) {
        await discardStaleMedia(leg)
        return
      }
      if (outcome === "attached") attachedLeg = leg
      await signalRefresh("incoming-media")
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
    await signalRefresh("incoming-media")
    if (!mediaContinuationCurrent(generation)) {
      await discardStaleMedia(leg)
      return
    }
    if (snapshot.expectedCallID) {
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
    if (snapshot.offers.length > 0) {
      incomingMedia.delete(leg.mediaToken)
      await rejectSafely(leg)
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
    if (!callID || (!exactTransientLeg && !restoredRecovery)) {
      incomingMedia.delete(leg.mediaToken)
      await rejectSafely(leg)
      return
    }
    if (incomingMedia.get(leg.mediaToken) !== leg) return
    incomingMedia.delete(leg.mediaToken)
    const outcome = await leg.answer().catch(() => "ended" as const)
    if (!mediaContinuationCurrent(generation)) {
      await discardStaleMedia(leg)
      return
    }
    if (outcome !== "attached") return
    try {
      const call = restoredRecovery
        ? snapshot.activeCall!
        : await options.backend.confirmMedia({
            callID,
            sessionID: options.sessionID,
            mediaToken: leg.mediaToken,
          })
      if (
        !mediaContinuationCurrent(generation) ||
        snapshot.expectedCallID !== callID
      ) {
        await discardStaleMedia(leg)
        return
      }
      if (!applyCall(call)) {
        await rejectSafely(leg)
        return
      }
      attachedLeg = leg
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
      publish({
        recoveryMedia: persistMediaCorrelation(mediaAttachment),
        mediaAttachment,
        failure: undefined,
      })
    } catch (error) {
      await rejectSafely(leg)
      if (mediaContinuationCurrent(generation)) setFailure(error, "conflict")
    }
  }

  async function handleMediaFailure(failure: MediaFailure) {
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
      failure: {
        kind: failure === "authentication" ? "authentication" : "media",
        message:
          failure === "network"
            ? "Call audio lost its connection. Check your network and reconnect calling."
            : failure === "authentication"
              ? "Calling authentication expired. Refresh access and reconnect calling."
              : "Call audio is unavailable. Reconnect calling and try again.",
        recoverable: true,
      },
    })
    await releaseLocalMedia()
    await commitReadiness(false)
  }

  async function releaseLocalMedia() {
    if (heartbeatTimer !== undefined) clock.clearTimeout(heartbeatTimer)
    heartbeatTimer = undefined
    readinessGeneration += 1
    microphoneGeneration += 1
    incomingMedia.clear()
    attachedLeg = undefined
    answeredInbound = undefined
    await options.media.disconnect().catch(() => undefined)
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
  }

  async function signalRefresh(
    reason: "staff-intent" | "incoming-media" | "media" | "visibility" | "backend",
  ) {
    void reason
    if (refreshTimer !== undefined) clock.clearTimeout(refreshTimer)
    refreshTimer = undefined
    await requestRefresh(true)
    scheduleRefresh()
  }

  async function takeOver() {
    try {
      const lease = leaseForSession(
        await options.backend.acquireLease({
          sessionID: options.sessionID,
          takeover: true,
        }),
        options.sessionID,
      )
      if (stopped) return
      publish({
        lease,
        failure: undefined,
        expectedCallID: lease.owner ? lease.activeCallId : "",
      })
      if (!lease.owner) {
        setFailure(
          new SoftphoneAdapterError(
            "ownership",
            "This browser could not take over calling.",
          ),
        )
        return
      }
      await signalRefresh("staff-intent")
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
      if (!stopped) return
      stopped = false
      const generation = ++lifecycleGeneration
      publish({ phase: "starting", failure: undefined })
      unsubscribeVisibility = visibility.subscribe(() => {
        if (!stopped) {
          syncAttention(snapshot.offers)
          void signalRefresh("visibility")
        }
      })
      try {
        const lease = leaseForSession(
          await options.backend.acquireLease({
            sessionID: options.sessionID,
            takeover: false,
          }),
          options.sessionID,
        )
        if (stopped || generation !== lifecycleGeneration) return
        publish({
          lease,
          expectedCallID: lease.owner ? lease.activeCallId : "",
          failure: lease.owner
            ? undefined
            : {
                kind: "ownership",
                message: "Calling is active in another browser. Take over to continue.",
                recoverable: true,
              },
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
    async stop() {
      if (stopped) return
      stopped = true
      lifecycleGeneration += 1
      if (refreshTimer !== undefined) clock.clearTimeout(refreshTimer)
      if (heartbeatTimer !== undefined) clock.clearTimeout(heartbeatTimer)
      refreshTimer = undefined
      heartbeatTimer = undefined
      refreshQueued = false
      requestedReadiness = undefined
      unsubscribeVisibility?.()
      unsubscribeVisibility = undefined
      await releaseLocalMedia()
      publish({
        ...snapshot,
        phase: "stopped",
        lease: undefined,
        offers: [],
        failure: undefined,
      })
    },
    signalRefresh,
    takeOver,
    async setAvailability(available) {
      publish({ availabilityIntent: available, failure: undefined })
      options.persistAvailabilityIntent?.(available)
      if (!snapshot.lease?.owner && available) await takeOver()
      if (snapshot.lease?.owner && snapshot.readiness.mediaState === "unavailable") {
        await connectMedia()
      }
      await commitReadiness(available)
      await signalRefresh("staff-intent")
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
        pending: { ...snapshot.pending, outbound: true },
        failure: undefined,
      })
      try {
        const call = await options.backend.startOutbound({
          ...input,
          sessionId: options.sessionID,
        })
        if (!commandContinuationCurrent(generation)) {
          await reconcileStaleCommand(generation)
          return
        }
        publish({
          expectedCallID: call.id,
          pendingCall: undefined,
          pending: { ...snapshot.pending, outbound: false },
        })
        applyCall(call)
        await commitReadiness(false)
        await signalRefresh("staff-intent")
      } catch (error) {
        if (!commandContinuationCurrent(generation)) {
          await reconcileStaleCommand(generation)
          return
        }
        publish({
          pendingCall: undefined,
          pending: { ...snapshot.pending, outbound: false },
        })
        await setCommandFailure(error)
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
      let outcome
      try {
        outcome = await leg.answer()
      } catch (error) {
        if (
          !mediaContinuationCurrent(generation) ||
          answeredInbound !== answering
        ) {
          await discardStaleMedia(leg)
          return
        }
        if (answeredInbound === answering) answeredInbound = undefined
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
        await signalRefresh("media")
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
        recoveryMedia: persistMediaCorrelation(mediaAttachment),
        mediaAttachment,
        failure: undefined,
      })
      syncAttention(snapshot.offers)
      await commitReadiness(false)
      await signalRefresh("staff-intent")
    },
    async hangup() {
      const call = snapshot.activeCall
      if (!call || snapshot.endingCallID) return
      const generation = lifecycleGeneration
      publish({ endingCallID: call.id, failure: undefined })
      try {
        const committed = await options.backend.hangup({
          callID: call.id,
          sessionID: options.sessionID,
        })
        if (!commandContinuationCurrent(generation)) {
          await reconcileStaleCommand(generation)
          return
        }
        applyCall(committed)
        await signalRefresh("staff-intent")
      } catch (error) {
        if (stopped || generation !== lifecycleGeneration) return
        await signalRefresh("staff-intent")
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
      try {
        const retried = await options.backend.retry({
          callID: call.id,
          sessionID: options.sessionID,
          idempotencyKey,
        })
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
        await signalRefresh("staff-intent")
      } catch (error) {
        if (!commandContinuationCurrent(generation)) {
          await reconcileStaleCommand(generation)
          return
        }
        publish({ pending: { ...snapshot.pending, retry: false } })
        await setCommandFailure(error)
      }
    },
    async recover() {
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
        await releaseLocalMedia()
        await connectMedia()
      }
      await signalRefresh("staff-intent")
    },
    dismissOutcome() {
      const call = snapshot.activeCall
      if (
        !call ||
        !callSettled(call) ||
        snapshot.expectedCallID ||
        snapshot.pending.retry
      ) {
        return
      }
      attachedLeg = undefined
      answeredInbound = undefined
      publish({
        activeCall: undefined,
        mediaAttachment: undefined,
        muted: false,
        failure: undefined,
      })
      void commitReadiness(snapshot.availabilityIntent)
    },
    async dispose(outcome) {
      const call = snapshot.pendingDisposition
      if (!call || snapshot.pending.disposition) return
      const generation = lifecycleGeneration
      publish({ pending: { ...snapshot.pending, disposition: true }, failure: undefined })
      try {
        const result = await options.backend.dispose({
          callID: call.id,
          sessionID: options.sessionID,
          outcome,
        })
        if (!commandContinuationCurrent(generation)) {
          await reconcileStaleCommand(generation)
          return
        }
        applyCall(result.call)
        publish({
          pendingDisposition: undefined,
          pending: { ...snapshot.pending, disposition: false },
        })
        await commitReadiness(snapshot.availabilityIntent)
        await signalRefresh("staff-intent")
        return result
      } catch (error) {
        if (!commandContinuationCurrent(generation)) {
          await reconcileStaleCommand(generation)
          return
        }
        publish({ pending: { ...snapshot.pending, disposition: false } })
        await setCommandFailure(error)
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
      snapshot.pending.outbound ||
      snapshot.pending.retry ||
      snapshot.pending.disposition,
  )
  const canEnd =
    call?.direction === "OUTBOUND"
      ? ["PREPARING", "RINGING", "CONNECTING", "CONNECTED"].includes(
          call.state,
        )
      : call?.direction === "INBOUND" &&
        connected &&
        mediaAttached &&
        mediaControlsReady
  return {
    ...snapshot,
    occupied,
    offers: snapshot.offers.map((offer) => ({ ...offer })),
    controls: {
      canEnd:
        Boolean(snapshot.lease?.owner && canEnd && !ending),
      canMute: Boolean(
        snapshot.lease?.owner &&
          connected &&
          mediaAttached &&
          mediaControlsReady &&
          !ending,
      ),
      canKeypad: Boolean(
        snapshot.lease?.owner &&
          connected &&
          mediaAttached &&
          mediaControlsReady &&
          !ending,
      ),
      canRetry: Boolean(snapshot.lease?.owner && call?.retryAllowed && !snapshot.expectedCallID),
      canDispose: Boolean(snapshot.pendingDisposition && !snapshot.pending.disposition),
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
  return {
    ...lease,
    owner: lease.owner && lease.sessionId === sessionID,
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
  }
}

function refreshDelay(snapshot: SoftphoneRuntimeSnapshot, hidden: boolean) {
  if (hidden) return 8_000
  if (snapshot.offers.length > 0) return 250
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

async function rejectSafely(leg: IncomingMediaLeg) {
  await leg.reject().catch(() => undefined)
}
