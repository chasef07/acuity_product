"use client"

import {
  createContext,
  type ReactNode,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
} from "react"
import {
  CheckIcon,
  MicIcon,
  MicOffIcon,
  PhoneCallIcon,
  PhoneOffIcon,
  RotateCcwIcon,
  ShieldAlertIcon,
} from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Spinner } from "@/components/ui/spinner"
import { Switch } from "@/components/ui/switch"
import {
  acquireSoftphone,
  confirmCallingMediaReady,
  getCallingCall,
  getCallingState,
  issueCallingMediaToken,
  recordCallingDisposition,
  requestCallingHangup,
  retryOutboundCall,
  setCallingReadiness,
  startOutboundCall,
} from "@/lib/api/generated/sdk.gen"
import type {
  CallingCall,
  CallingDispositionResult,
  RingingCallLeg,
  SoftphoneState,
} from "@/lib/api/generated/types.gen"
import { getAccessToken, getAccessTokenResult } from "@/lib/auth-client"
import {
  createCallingMediaAdapter,
  type CallingMediaAdapter,
  type IncomingMediaLeg,
  type MediaState,
} from "@/lib/calling/media-adapter"
import {
  answeredCallLegStatus,
  confirmOutboundMediaWithRetry,
  currentCallingStateCallID,
  mediaAttachmentAfterState,
  microphoneFailureMessage,
  routeIncomingMedia,
} from "@/lib/calling/dock-media-state"
import {
  activeCallEndPending,
  endingCallIDAfterProjection,
  showActiveCallEndControl,
} from "@/lib/calling/active-call-controls"
import { LatestWrite } from "@/lib/calling/latest-write"
import {
  createCallingOwnerLoop,
  type CallingOwnerLoop,
} from "@/lib/calling/owner-loop"
import {
  callIsSettled,
  dispositionWindowIsOpen,
  hangupFailure,
  providerOutcomeLabel,
} from "@/lib/calling/outcomes"
import {
  activeRingingOffers,
  offerSecondsRemaining,
  outboundCallBlockReason,
  outboundCallOccupiedMessage,
} from "@/lib/calling/offers"
import { portalClient } from "@/lib/api/client"
import { cn } from "@/lib/utils"

type CallingDockProps = {
  children: ReactNode
  callingEnabled: boolean
  practiceID: string
  taskCallRequest?: { id: string; taskID: string }
  onTaskCallHandled: (requestID: string, error?: string) => void
  onCallChanged: (call: CallingCall | undefined) => void
  onDisposition: (result: CallingDispositionResult) => void
}

const sessionStorageKey = "acuity.callingSession"
const availabilityIntentStorageKey = "acuity.callingAvailabilityIntent"
const readinessWriteTimeoutMilliseconds = 5_000

type CallingNavigationContext = {
  activeCall: CallingCall | undefined
  availabilityError: string
  availabilityPending: boolean
  available: boolean
  ownsSoftphone: boolean
  outboundPending: boolean
  callingEnabled: boolean
  setAvailability: (available: boolean) => void
  startOutbound: (locationID: string, destination: string) => Promise<string | undefined>
}

type ReadinessUpdate = {
  available: boolean
  mediaState: MediaState
}

type ReadinessCommit = {
  failure?: "authentication" | "ownership" | "request"
  state?: SoftphoneState
}

type AnsweredInboundCallLeg = Pick<
  RingingCallLeg,
  "callId" | "callLegId" | "mediaToken"
> & {
  providerLegID: string
}

const CallingNavigationContext = createContext<CallingNavigationContext | null>(
  null,
)

export function useCallingNavigation() {
  const context = useContext(CallingNavigationContext)
  if (!context) {
    throw new Error("Calling navigation must be rendered inside CallingDock.")
  }
  return context
}

export function CallingAvailabilityControl() {
  const {
    activeCall,
    availabilityError,
    availabilityPending,
    available,
    ownsSoftphone,
    callingEnabled,
    setAvailability,
  } = useCallingNavigation()
  if (!callingEnabled) return null
  return (
    <div className="flex w-full shrink-0 items-center gap-2">
      <span className="min-w-0 flex-1 text-sm font-medium">
        Available for calls
      </span>
      {availabilityPending && <Spinner aria-label="Updating availability" />}
      {!availabilityPending && availabilityError && (
        <ShieldAlertIcon
          aria-label={availabilityError}
          className="text-destructive"
        />
      )}
      <Switch
        aria-label="Availability"
        className="data-checked:bg-success"
        size="sm"
        checked={available}
        disabled={
          availabilityPending || (Boolean(activeCall) && ownsSoftphone)
        }
        onCheckedChange={setAvailability}
      />
    </div>
  )
}

export function CallingDock({
  children,
  callingEnabled,
  practiceID,
  taskCallRequest,
  onTaskCallHandled,
  onCallChanged,
  onDisposition,
}: CallingDockProps) {
  const [sessionID] = useState(browserSessionID)
  const [lease, setLease] = useState<SoftphoneState>()
  const [mediaState, setMediaState] = useState<MediaState>("unavailable")
  const [available, setAvailable] = useState(false)
  const [ringingLegs, setRingingLegs] = useState<RingingCallLeg[]>([])
  const [readyIncomingMediaTokens, setReadyIncomingMediaTokens] = useState<
    ReadonlySet<string>
  >(() => new Set())
  const [activeCall, setActiveCall] = useState<CallingCall>()
  const [pendingOutcome, setPendingOutcome] = useState<CallingCall>()
  const [expectedCallID, setExpectedCallID] = useState("")
  const [mediaAttached, setMediaAttached] = useState(false)
  const [inboundCallControlReady, setInboundCallControlReady] = useState(false)
  const [audioIssue, setAudioIssue] = useState(false)
  const [callingNotice, setCallingNotice] = useState("")
  const [muted, setMuted] = useState(false)
  const [error, setError] = useState("")
  const [outboundPending, setOutboundPending] = useState(false)
  const [endingCallID, setEndingCallID] = useState("")
  const [availabilityPending, setAvailabilityPending] = useState(false)
  const [now, setNow] = useState(() => Date.now())
  const [readinessWriter] = useState(
    () =>
      new LatestWrite<ReadinessUpdate, ReadinessCommit>(
        readinessWriteTimeoutMilliseconds,
      ),
  )
  const adapterRef = useRef<CallingMediaAdapter | null>(null)
  const mediaLegRef = useRef<IncomingMediaLeg | null>(null)
  const answeredInboundLegRef = useRef<AnsweredInboundCallLeg | undefined>(
    undefined,
  )
  const probeStreamRef = useRef<MediaStream | null>(null)
  const expectedCallRef = useRef("")
  const activeCallSnapshotRef = useRef<CallingCall | undefined>(undefined)
  const ownerRef = useRef(false)
  const leaseRef = useRef<SoftphoneState | undefined>(undefined)
  const ownerGenerationRef = useRef(0)
  const mediaStateRef = useRef<MediaState>("unavailable")
  const availabilityRef = useRef(false)
  const availabilityIntentRef = useRef(false)
  const announcedRingingLegsRef = useRef(new Set<string>())
  const incomingLegsRef = useRef(new Map<string, IncomingMediaLeg>())
  const callingStateETagRef = useRef("")
  const statePollRef = useRef<Promise<void> | null>(null)
  const ownershipPollRef = useRef<Promise<void> | null>(null)
  const ownerLoopRef = useRef<CallingOwnerLoop | null>(null)
  const ringtoneRef = useRef<(() => void) | null>(null)
  const connectingRef = useRef(false)
  const handledTaskCallRef = useRef("")
  const notificationsRef = useRef(new Map<string, Notification>())
  const rememberIncomingLeg = useCallback((leg: IncomingMediaLeg) => {
    incomingLegsRef.current.set(leg.mediaToken, leg)
    setReadyIncomingMediaTokens((current) => {
      if (current.has(leg.mediaToken)) return current
      const next = new Set(current)
      next.add(leg.mediaToken)
      return next
    })
  }, [])
  const forgetIncomingLeg = useCallback(
    (leg: Pick<IncomingMediaLeg, "providerLegID" | "mediaToken">) => {
      const currentLeg = incomingLegsRef.current.get(leg.mediaToken)
      if (currentLeg && currentLeg.providerLegID !== leg.providerLegID) return
      incomingLegsRef.current.delete(leg.mediaToken)
      setReadyIncomingMediaTokens((current) => {
        if (!current.has(leg.mediaToken)) return current
        const next = new Set(current)
        next.delete(leg.mediaToken)
        return next
      })
    },
    [],
  )
  const clearIncomingLegs = useCallback(() => {
    incomingLegsRef.current.clear()
    setReadyIncomingMediaTokens(new Set())
  }, [])
  const clearMediaAttachment = useCallback(() => {
    mediaLegRef.current = null
    setMediaAttached(false)
    setMuted(false)
    setAudioIssue(false)
  }, [])
  const clearAnsweredInboundLeg = useCallback(() => {
    answeredInboundLegRef.current = undefined
    setInboundCallControlReady(false)
  }, [])
  const rejectMediaLeg = useCallback(
    async (leg: IncomingMediaLeg, currentAttachment = false) => {
      try {
        await leg.reject()
        return true
      } catch {
        if (currentAttachment) {
          const adapter = adapterRef.current
          adapterRef.current = null
          let disconnectFailed = false
          try {
            await adapter?.disconnect()
          } catch {
            disconnectFailed = true
          }
          mediaStateRef.current = "unavailable"
          setMediaState("unavailable")
          setError(
            disconnectFailed
              ? "Browser audio could not be safely stopped. Reconnect calling before answering another call."
              : "Browser audio was stopped because the obsolete call leg could not be released. Reconnect calling.",
          )
          return false
        }
        setError(
          "An obsolete browser media invite could not be released. Reconnect calling.",
        )
        return false
      }
    },
    [],
  )
  const applyActiveCall = useCallback((call?: CallingCall) => {
    if (call && call.id !== expectedCallRef.current) return false
    const applied = activeCallSnapshotRef.current
    if (call && applied?.id === call.id && call.version < applied.version) {
      return false
    }
    activeCallSnapshotRef.current = call
    setActiveCall(call)
    setEndingCallID((current) => endingCallIDAfterProjection(current, call))
    return true
  }, [])
  useEffect(() => {
    expectedCallRef.current = expectedCallID
  }, [expectedCallID])
  useEffect(() => {
    ownerRef.current = Boolean(lease?.owner)
  }, [lease?.owner])
  useEffect(() => {
    leaseRef.current = lease
  }, [lease])
  useEffect(() => {
    mediaStateRef.current = mediaState
  }, [mediaState])
  useEffect(() => {
    availabilityRef.current = available
  }, [available])
  useEffect(() => {
    onCallChanged(activeCall)
  }, [activeCall, onCallChanged])

  const updateReadiness = useCallback(
    async (nextAvailable: boolean, nextMediaState?: MediaState) => {
      const effectiveMediaState = nextMediaState ?? mediaStateRef.current
      const write = readinessWriter.write(
        { available: nextAvailable, mediaState: effectiveMediaState },
        async (update, signal) => {
          const authentication = await getAccessTokenResult()
          if (authentication.status === "unauthenticated") {
            return { failure: "authentication" }
          }
          if (authentication.status === "unavailable") {
            return { failure: "request" }
          }
          const token = authentication.token
          if (signal.aborted) return { failure: "request" }
          const probeTrack = probeStreamRef.current?.getAudioTracks()[0]
          const technicallyReady =
            update.mediaState === "ready" && probeTrack?.readyState === "live"
          const result = await setCallingReadiness({
            client: portalClient(token),
            signal,
            body: {
              sessionId: sessionID,
              registered: technicallyReady,
              microphoneReady: technicallyReady,
              audioReady: technicallyReady,
              sessionHealthy: technicallyReady,
              available: update.available,
            },
          }).catch(() => undefined)
          if (result?.data) return { state: result.data }
          const status = result?.response?.status
          if (status === 401) return { failure: "authentication" }
          if (status === 403 || status === 409) {
            return { failure: "ownership" }
          }
          return { failure: "request" }
        },
      )
      const writeGeneration = readinessWriter.generation
      const settled = await write.catch(() => undefined)
      if (!settled) {
        if (writeGeneration !== readinessWriter.generation) return undefined
        setError(
          nextAvailable
            ? "Availability could not be confirmed."
            : "Pausing calls could not be confirmed.",
        )
        setAvailabilityPending(false)
        return undefined
      }
      if (settled.generation !== readinessWriter.generation) {
        return settled.output
      }
      if (settled.output.state) {
        setLease(settled.output.state)
        setAvailable(settled.output.state.available)
        availabilityRef.current = settled.output.state.available
      } else {
        setError(
          settled.output.failure === "authentication"
            ? "Your authentication needs to be refreshed."
            : settled.input.available
              ? "Availability could not be confirmed."
              : "Pausing calls could not be confirmed.",
        )
      }
      setAvailabilityPending(false)
      return settled.output
    },
    [readinessWriter, sessionID],
  )

  const rememberAvailabilityIntent = useCallback((nextAvailable: boolean) => {
    availabilityIntentRef.current = nextAvailable
    window.sessionStorage.setItem(
      availabilityIntentStorageKey,
      String(nextAvailable),
    )
  }, [])

  const commitOutbound = useCallback(
    async (
      idempotencyKey: string,
      route:
        | { taskId: string }
        | {
            practiceId: string
            locationId: string
            destination: string
          },
    ) => {
      if (!callingEnabled) return "Calling is not enabled for this account."
      if (!lease?.owner) return "Enable or take over calling first."
      if (mediaState !== "ready") return "Browser calling audio is not ready."
      if (activeCall || expectedCallRef.current) {
        return "Finish the active Call before starting another."
      }
      const offerBlock = outboundCallBlockReason(ringingLegs, Date.now())
      if (offerBlock) return offerBlock
      const token = await getAccessToken()
      if (!token) return "Your authentication needs to be refreshed."
      setCallingNotice("")
      setOutboundPending(true)
      setError("")
      const result = await startOutboundCall({
        client: portalClient(token),
        body: {
          sessionId: sessionID,
          idempotencyKey,
          ...route,
        },
      }).catch(() => undefined)
      setOutboundPending(false)
      if (!result?.data) {
        const status = result?.response?.status
        if (status === 409) {
          if (result?.error?.error.code === "CALL_OCCUPIED") {
            return outboundCallOccupiedMessage
          }
          return "The Call route or active softphone state changed. Refresh and try again."
        }
        if (status === 400) {
          return "The destination is not supported for outbound calling."
        }
        if (status === 403) {
          return "Current Access does not permit this Call."
        }
        return "The outbound Call could not be committed."
      }
      expectedCallRef.current = result.data.id
      setExpectedCallID(result.data.id)
      applyActiveCall(result.data)
      setAvailable(false)
      availabilityRef.current = false
      return undefined
    },
    [
      activeCall,
      applyActiveCall,
      callingEnabled,
      lease?.owner,
      mediaState,
      ringingLegs,
      sessionID,
    ],
  )

  useEffect(() => {
    if (!taskCallRequest || handledTaskCallRef.current === taskCallRequest.id) {
      return
    }
    handledTaskCallRef.current = taskCallRequest.id
    void commitOutbound(taskCallRequest.id, {
      taskId: taskCallRequest.taskID,
    }).then((requestError) => {
      onTaskCallHandled(taskCallRequest.id, requestError)
      if (requestError) setError(requestError)
    })
  }, [commitOutbound, onTaskCallHandled, taskCallRequest])

  const startNumberCall = useCallback(
    async (locationID: string, destination: string) => {
      const requestError = await commitOutbound(window.crypto.randomUUID(), {
        practiceId: practiceID,
        locationId: locationID,
        destination,
      })
      if (requestError) setError(requestError)
      return requestError
    },
    [commitOutbound, practiceID],
  )

  const handleIncoming = useCallback(
    async (leg: IncomingMediaLeg) => {
      void ownerLoopRef.current?.incomingMedia()
      const attached = mediaLegRef.current
      const route = routeIncomingMedia(
        attached,
        leg,
        ownerRef.current,
        mediaStateRef.current,
        expectedCallRef.current,
      )
      if (route === "RECOVER_ATTACHED") {
        let outcome
        try {
          outcome = await leg.answer()
        } catch {
          clearMediaAttachment()
          setError("Browser call audio could not be restored. Reconnect calling.")
          return
        }
        if (outcome === "ended") return
        mediaLegRef.current = leg
        setMediaAttached(true)
        setAudioIssue(false)
        return
      }
      if (route === "REJECT") {
        forgetIncomingLeg(leg)
        await rejectMediaLeg(leg)
        return
      }
      if (route === "CONFIRM_OUTBOUND") {
        const token = await getAccessToken()
        if (!token) {
          await rejectMediaLeg(leg)
          return
        }
        let outcome
        try {
          outcome = await leg.answer()
        } catch (error) {
          setError(microphoneFailureMessage(error))
          return
        }
        if (outcome === "ended") return
        const callID = expectedCallRef.current
        const confirmed = await confirmOutboundMediaWithRetry(async () => {
          const result = await confirmCallingMediaReady({
            client: portalClient(token),
            path: { callId: callID },
            body: { sessionId: sessionID, mediaToken: leg.mediaToken },
          })
          return { data: result.data, status: result.response?.status }
        })
        if (!confirmed) {
          await rejectMediaLeg(leg, true)
          return
        }
        applyActiveCall(confirmed)
        mediaLegRef.current = leg
        setMediaAttached(true)
        setAudioIssue(false)
        return
      }
      const existing = incomingLegsRef.current.get(leg.mediaToken)
      if (existing && existing.providerLegID !== leg.providerLegID) {
        await rejectMediaLeg(leg)
        return
      }
      rememberIncomingLeg(leg)
    },
    [
      applyActiveCall,
      clearMediaAttachment,
      forgetIncomingLeg,
      rejectMediaLeg,
      rememberIncomingLeg,
      sessionID,
    ],
  )

  const connectMedia = useCallback(async (ownedLease?: SoftphoneState) => {
    if (connectingRef.current) return
    connectingRef.current = true
    try {
      const storedIntent = window.sessionStorage.getItem(
        availabilityIntentStorageKey,
      )
      if (storedIntent === null) {
        rememberAvailabilityIntent(true)
      } else {
        availabilityIntentRef.current = storedIntent === "true"
      }
      setError("")
      const token = await getAccessToken()
      if (!token) {
        setError("Your authentication needs to be refreshed.")
        setAvailabilityPending(false)
        return
      }
      const acquiredState =
        ownedLease ??
        (
          await acquireSoftphone({
            client: portalClient(token),
            body: { sessionId: sessionID, takeover: false },
          }).catch(() => undefined)
        )?.data
      if (!acquiredState) {
        setError("The softphone lease is temporarily unavailable.")
        setAvailabilityPending(false)
        return
      }
      setLease(acquiredState)
      if (!acquiredState.owner) {
        if (acquiredState.activeCallId) {
          expectedCallRef.current = acquiredState.activeCallId
          setExpectedCallID(acquiredState.activeCallId)
          const observed = await getCallingCall({
            client: portalClient(token),
            path: { callId: acquiredState.activeCallId },
          }).catch(() => undefined)
          if (observed?.data) applyActiveCall(observed.data)
        }
        setError("Calling is active in another browser.")
        setAvailabilityPending(false)
        return
      }
      if (acquiredState.activeCallId) {
        expectedCallRef.current = acquiredState.activeCallId
        setExpectedCallID(acquiredState.activeCallId)
        setAvailable(false)
        availabilityRef.current = false
        const restored = await getCallingCall({
          client: portalClient(token),
          path: { callId: acquiredState.activeCallId },
        }).catch(() => undefined)
        if (restored?.data) {
          applyActiveCall(restored.data)
          setMediaAttached(false)
        }
      }
      if (acquiredState.pendingOutcomeCallId) {
        const pending = await getCallingCall({
          client: portalClient(token),
          path: { callId: acquiredState.pendingOutcomeCallId },
        }).catch(() => undefined)
        if (pending?.data?.state === "NEEDS_DISPOSITION") {
          setPendingOutcome(pending.data)
        }
      }
      try {
        const stream = await navigator.mediaDevices.getUserMedia({ audio: true })
        const microphone = stream.getAudioTracks()[0]
        if (!microphone || microphone.readyState !== "live") {
          stream.getTracks().forEach((track) => track.stop())
          throw new Error("microphone unavailable")
        }
        probeStreamRef.current?.getTracks().forEach((track) => track.stop())
        probeStreamRef.current = stream
        microphone.addEventListener(
          "ended",
          () => {
            setAvailable(false)
            availabilityRef.current = false
            void updateReadiness(false, "unavailable")
          },
          { once: true },
        )
        const audioContext = new AudioContext()
        await audioContext.resume()
        await audioContext.close()
      } catch (error) {
        setError(microphoneFailureMessage(error))
        setAvailabilityPending(false)
        return
      }
      if ("Notification" in window && Notification.permission === "default") {
        void Notification.requestPermission().catch(() => undefined)
      }
      const issued = await issueCallingMediaToken({
        client: portalClient(token),
        body: { sessionId: sessionID },
      }).catch(() => undefined)
      if (!issued?.data) {
        setError(
          "Calling credentials are still being prepared. Try again shortly.",
        )
        setAvailabilityPending(false)
        return
      }
      if (!document.querySelector("#acuity-calling-remote-audio")) {
        setError("The browser audio output is unavailable.")
        setAvailabilityPending(false)
        return
      }
      clearIncomingLegs()
      await adapterRef.current?.disconnect()
      const adapter = createCallingMediaAdapter()
      adapterRef.current = adapter
      await adapter.connect(issued.data.token, "acuity-calling-remote-audio", {
        onState: (state) => {
          mediaStateRef.current = state
          setMediaState(state)
          if (state === "ready") {
            const mayReceiveCalls =
              availabilityIntentRef.current &&
              expectedCallRef.current === ""
            void updateReadiness(mayReceiveCalls, state)
          } else if (state === "unavailable" || state === "reconnecting") {
            clearIncomingLegs()
            setAvailable(false)
            availabilityRef.current = false
            mediaLegRef.current = mediaAttachmentAfterState(
              state,
              mediaLegRef.current,
            )
            setMediaAttached(false)
            setMuted(false)
            void updateReadiness(false, state)
          }
        },
        onIncoming: (leg) => void handleIncoming(leg),
        onEnded: (leg) => {
          const attached = mediaLegRef.current
          const answering = answeredInboundLegRef.current
          forgetIncomingLeg(leg)
          if (
            !(
              attached?.providerLegID === leg.providerLegID &&
              attached.mediaToken === leg.mediaToken
            ) &&
            !(
              answering?.providerLegID === leg.providerLegID &&
              answering.mediaToken === leg.mediaToken
            )
          ) {
            return
          }
          clearMediaAttachment()
          setInboundCallControlReady(false)
          void ownerLoopRef.current?.incomingMedia()
        },
        onAudioIssue: () => setAudioIssue(true),
        onFailure: (failure) => {
          setError(
            failure === "authentication"
              ? "Calling authentication expired. Reconnect calling to refresh it."
              : failure === "network"
                ? "Calling lost its network connection. Check your connection and reconnect."
                : "Telnyx calling is unavailable. Reconnect calling and try again.",
          )
        },
        refreshToken: async () => {
          const accessToken = await getAccessToken()
          if (!accessToken) return undefined
          const refreshed = await issueCallingMediaToken({
            client: portalClient(accessToken),
            body: { sessionId: sessionID },
          }).catch(() => undefined)
          return refreshed?.data?.token
        },
      })
    } catch {
      setAvailable(false)
      availabilityRef.current = false
      setAvailabilityPending(false)
      setError("Browser calling could not be started.")
    } finally {
      connectingRef.current = false
    }
  }, [
    handleIncoming,
    applyActiveCall,
    clearMediaAttachment,
    clearIncomingLegs,
    forgetIncomingLeg,
    rememberAvailabilityIntent,
    sessionID,
    updateReadiness,
  ])

  const refreshCallingState = useCallback(async (requireFresh = false) => {
    if (!ownerRef.current) {
      setRingingLegs([])
      return
    }
    const currentPoll = statePollRef.current
    if (currentPoll) {
      await currentPoll
      if (!requireFresh) return
    }
    if (statePollRef.current) return statePollRef.current
    const poll = (async () => {
      const ownerGeneration = ownerGenerationRef.current
      const availabilityGeneration = readinessWriter.generation
      const availabilityWriteWasPending = readinessWriter.pending
      const token = await getAccessToken()
      if (!token) return
      const result = await getCallingState({
        client: portalClient(token),
        headers: callingStateETagRef.current
          ? { "If-None-Match": callingStateETagRef.current }
          : undefined,
      }).catch(() => undefined)
      if (!result?.data) return
      if (
        !ownerRef.current ||
        ownerGeneration !== ownerGenerationRef.current
      ) {
        return
      }
      setLease(result.data.softphone)
      const availabilitySnapshotIsCurrent =
        readinessWriter.snapshotIsCurrent(
          availabilityGeneration,
          availabilityWriteWasPending,
        )
      if (availabilitySnapshotIsCurrent) {
        const etag = result.response?.headers.get("ETag")
        if (etag) callingStateETagRef.current = etag
        setAvailable(result.data.softphone.available)
        availabilityRef.current = result.data.softphone.available
      }
      setRingingLegs(result.data.ringing)
      if (result.data.ringing.length > 0) setCallingNotice("")
      if (!result.data.disposition) {
        setPendingOutcome(undefined)
      }
      const answeredInboundLeg = answeredInboundLegRef.current
      if (answeredInboundLeg) {
        const status = answeredCallLegStatus(result.data, answeredInboundLeg)
        setInboundCallControlReady(status === "BRIDGED")
        if (status === "LOST") {
          let answeredElsewhere =
            (result.data.bridged?.callId === answeredInboundLeg.callId &&
              result.data.bridged.callLegId !== answeredInboundLeg.callLegId) ||
            (result.data.disposition?.callId === answeredInboundLeg.callId &&
              result.data.disposition.callLegId !==
                answeredInboundLeg.callLegId)
          let callEnded = false
          if (!answeredElsewhere) {
            try {
              const observed = await getCallingCall({
                client: portalClient(token),
                path: { callId: answeredInboundLeg.callId },
              })
              if (observed.data) {
                answeredElsewhere =
                  observed.data.state === "CONNECTING" ||
                  observed.data.state === "CONNECTED" ||
                  observed.data.state === "NEEDS_DISPOSITION"
                callEnded = !answeredElsewhere
              } else {
                setError(
                  "The Call changed, but its current state could not be confirmed. Refresh calling.",
                )
              }
            } catch {
              setError(
                "The Call changed, but its current state could not be confirmed. Refresh calling.",
              )
            }
          }
          const attached = mediaLegRef.current
          if (
            attached?.providerLegID === answeredInboundLeg.providerLegID &&
            attached.mediaToken === answeredInboundLeg.mediaToken
          ) {
            await rejectMediaLeg(attached, true)
          }
          clearAnsweredInboundLeg()
          clearMediaAttachment()
          applyActiveCall()
          expectedCallRef.current = ""
          setExpectedCallID("")
          setCallingNotice(
            answeredElsewhere
              ? "Another staff member answered this call."
              : callEnded
                ? "The call ended before this browser connected."
                : "This call is no longer available in this browser.",
          )
          if (
            mediaStateRef.current === "ready" &&
            availabilityIntentRef.current
          ) {
            setAvailabilityPending(true)
            void updateReadiness(true, "ready")
          }
          return
        }
      } else if (activeCallSnapshotRef.current?.direction === "INBOUND") {
        setInboundCallControlReady(
          result.data.bridged?.callId === expectedCallRef.current,
        )
      }
      const currentCallID = currentCallingStateCallID(result.data)
      if (currentCallID && currentCallID !== expectedCallRef.current) {
        expectedCallRef.current = currentCallID
        setExpectedCallID(currentCallID)
      }
    })().finally(() => {
      statePollRef.current = null
    })
    statePollRef.current = poll
    return poll
  }, [
    applyActiveCall,
    clearAnsweredInboundLeg,
    clearMediaAttachment,
    rejectMediaLeg,
    readinessWriter,
    updateReadiness,
  ])

  const refreshCall = useCallback(async (requestedCallID?: string) => {
    const callID = requestedCallID ?? expectedCallRef.current
    if (!callID) return
    const token = await getAccessToken()
    if (!token) return { status: 401 }
    const result = await getCallingCall({
      client: portalClient(token),
      path: { callId: callID },
    }).catch(() => undefined)
    if (
      requestedCallID
        ? expectedCallRef.current && expectedCallRef.current !== callID
        : expectedCallRef.current !== callID
    ) {
      return
    }
    const releaseCallControl = () => {
      applyActiveCall()
      expectedCallRef.current = ""
      setExpectedCallID("")
      mediaLegRef.current = null
      clearAnsweredInboundLeg()
      setMediaAttached(false)
      setMuted(false)
      if (
        ownerRef.current &&
        mediaStateRef.current === "ready" &&
        availabilityIntentRef.current &&
        !availabilityRef.current
      ) {
        setAvailabilityPending(true)
        void updateReadiness(true, "ready")
      }
    }
    if (result?.data) {
      if (
        result.data.state === "VOICEMAIL_GREETING" ||
        result.data.state === "VOICEMAIL_RECORDING"
      ) {
        releaseCallControl()
        return { call: result.data, status: result.response?.status }
      }
      if (result.data.state === "NEEDS_DISPOSITION") {
        setPendingOutcome(result.data)
        releaseCallControl()
        return { call: result.data, status: result.response?.status }
      }
      if (!applyActiveCall(result.data)) {
        return { call: result.data, status: result.response?.status }
      }
      if (callIsSettled(result.data.state)) {
        setMediaAttached(false)
        mediaLegRef.current = null
        clearAnsweredInboundLeg()
      }
      if (result.data.state === "UNANSWERED") {
        releaseCallControl()
      }
      return { call: result.data, status: result.response?.status }
    } else if (
      result?.response?.status === 403 ||
      result?.response?.status === 409
    ) {
      releaseCallControl()
    }
    return { status: result?.response?.status }
  }, [applyActiveCall, clearAnsweredInboundLeg, updateReadiness])

  const releaseCallingOwnership = useCallback(
    async (currentLease?: SoftphoneState) => {
      if (ownerRef.current) ownerGenerationRef.current += 1
      ownerRef.current = false
      setLease(currentLease)
      setAvailable(false)
      availabilityRef.current = false
      setMediaState("unavailable")
      mediaStateRef.current = "unavailable"
      setRingingLegs([])
      clearIncomingLegs()
      mediaLegRef.current = null
      clearAnsweredInboundLeg()
      setMediaAttached(false)
      setMuted(false)
      await adapterRef.current?.disconnect()
      adapterRef.current = null
      probeStreamRef.current?.getTracks().forEach((track) => track.stop())
      probeStreamRef.current = null
    },
    [clearAnsweredInboundLeg, clearIncomingLegs],
  )

  const refreshOwnershipNow = useCallback(async () => {
    const token = await getAccessToken()
    if (!token) return
    const result = await acquireSoftphone({
      client: portalClient(token),
      body: { sessionId: sessionID, takeover: false },
    }).catch(() => undefined)
    if (!result?.data?.owner) {
      await releaseCallingOwnership(result?.data)
      if (result?.data?.activeCallId) {
        expectedCallRef.current = result.data.activeCallId
        setExpectedCallID(result.data.activeCallId)
        await refreshCall()
      } else {
        expectedCallRef.current = ""
        setExpectedCallID("")
        applyActiveCall()
      }
      return
    }
    setLease(result.data)
    if (!ownerRef.current) ownerGenerationRef.current += 1
    ownerRef.current = true
    if (result.data.pendingOutcomeCallId) {
      const pending = await getCallingCall({
        client: portalClient(token),
        path: { callId: result.data.pendingOutcomeCallId },
      }).catch(() => undefined)
      if (pending?.data?.state === "NEEDS_DISPOSITION") {
        setPendingOutcome(pending.data)
      }
    } else {
      setPendingOutcome(undefined)
    }
    if (result.data.activeCallId) {
      const restoredCall =
        expectedCallRef.current !== result.data.activeCallId
      if (restoredCall) {
        expectedCallRef.current = result.data.activeCallId
        setExpectedCallID(result.data.activeCallId)
      }
      setAvailable(false)
      availabilityRef.current = false
      if (restoredCall) await refreshCall()
    }
    if (!adapterRef.current) {
      await connectMedia(result.data)
      return
    }
    await updateReadiness(
      availabilityIntentRef.current && expectedCallRef.current === "",
    )
  }, [
    applyActiveCall,
    connectMedia,
    refreshCall,
    releaseCallingOwnership,
    sessionID,
    updateReadiness,
  ])

  const refreshOwnership = useCallback(() => {
    if (ownershipPollRef.current) return ownershipPollRef.current
    const poll = refreshOwnershipNow().finally(() => {
      ownershipPollRef.current = null
    })
    ownershipPollRef.current = poll
    return poll
  }, [refreshOwnershipNow])

  useEffect(() => {
    if (!callingEnabled || lease?.owner) return
    let cancelled = false
    let timeout = 0
    const schedule = (delay: number) => {
      timeout = window.setTimeout(async () => {
        await refreshOwnership()
        if (ownerRef.current) {
          await refreshCallingState()
          await refreshCall()
          return
        }
        if (cancelled) return
        schedule(6_000 + Math.floor(Math.random() * 2_001))
      }, delay)
    }
    schedule(0)
    return () => {
      cancelled = true
      window.clearTimeout(timeout)
    }
  }, [
    callingEnabled,
    lease?.owner,
    refreshCall,
    refreshCallingState,
    refreshOwnership,
  ])

  useEffect(() => {
    if (!lease?.owner) return
    let lostReason: ReadinessCommit["failure"]
    const loop = createCallingOwnerLoop({
      ensureMediaConnected: async () => {
        const ownedLease = leaseRef.current
        if (!adapterRef.current && ownedLease?.owner) {
          await connectMedia(ownedLease)
        }
      },
      heartbeat: async () => {
        const result = await updateReadiness(
          availabilityIntentRef.current && expectedCallRef.current === "",
          mediaStateRef.current,
        )
        if (result?.state?.owner) return "owner"
        if (
          result?.failure === "authentication" ||
          result?.failure === "ownership" ||
          (result?.state && !result.state.owner)
        ) {
          lostReason = result.failure ?? "ownership"
          return "lost"
        }
        return "retry"
      },
      refresh: async () => {
        await refreshCallingState()
        await refreshCall()
      },
      onOwnershipLost: async () => {
        await releaseCallingOwnership()
        setError(
          lostReason === "authentication"
            ? "Your authentication needs to be refreshed."
            : "Calling ownership moved to another browser. Reconnect to continue.",
        )
      },
      isHidden: () => document.hidden,
    })
    ownerLoopRef.current = loop
    const handleVisibility = () => void loop.visibilityChanged()
    document.addEventListener("visibilitychange", handleVisibility)
    loop.start()
    return () => {
      loop.stop()
      if (ownerLoopRef.current === loop) ownerLoopRef.current = null
      document.removeEventListener("visibilitychange", handleVisibility)
    }
  }, [
    connectMedia,
    lease?.owner,
    refreshCall,
    refreshCallingState,
    releaseCallingOwnership,
    updateReadiness,
  ])

  useEffect(() => {
    if (callingEnabled || !ownerRef.current) return
    rememberAvailabilityIntent(false)
    setAvailabilityPending(true)
    void updateReadiness(false)
  }, [callingEnabled, rememberAvailabilityIntent, updateReadiness])

  useEffect(() => {
    const activeOffers = activeRingingOffers(ringingLegs, now)
    const activeLegIDs = new Set(activeOffers.map((leg) => leg.callLegId))
    for (const [legID, notification] of notificationsRef.current) {
      if (activeLegIDs.has(legID)) continue
      notification.close()
      notificationsRef.current.delete(legID)
      announcedRingingLegsRef.current.delete(legID)
    }
    if (activeOffers.length === 0) {
      ringtoneRef.current?.()
      ringtoneRef.current = null
      return
    }
    if (!ringtoneRef.current) ringtoneRef.current = startRingtone()
    for (const leg of activeOffers) {
      if (announcedRingingLegsRef.current.has(leg.callLegId)) continue
      announcedRingingLegsRef.current.add(leg.callLegId)
      if (
        document.hidden &&
        "Notification" in window &&
        Notification.permission === "granted"
      ) {
        const notification = new Notification("Incoming Acuity transfer", {
          body: `${leg.locationName} · answer in Acuity`,
          tag: leg.callLegId,
        })
        notificationsRef.current.set(leg.callLegId, notification)
      }
    }
  }, [now, ringingLegs])

  useEffect(() => {
    const interval = window.setInterval(() => setNow(Date.now()), 250)
    return () => window.clearInterval(interval)
  }, [])

  useEffect(() => {
    const mediaDevices = navigator.mediaDevices
    if (!mediaDevices?.addEventListener || !mediaDevices.enumerateDevices) return
    const verifyDevices = async () => {
      const devices = await mediaDevices.enumerateDevices().catch(() => [])
      if (devices.some((device) => device.kind === "audioinput")) return
      setAvailable(false)
      availabilityRef.current = false
      await updateReadiness(false, "unavailable")
    }
    mediaDevices.addEventListener("devicechange", verifyDevices)
    return () => mediaDevices.removeEventListener("devicechange", verifyDevices)
  }, [updateReadiness])

  useEffect(
    () => () => {
      ringtoneRef.current?.()
      for (const notification of notificationsRef.current.values()) {
        notification.close()
      }
      notificationsRef.current.clear()
      void adapterRef.current?.disconnect()
      probeStreamRef.current?.getTracks().forEach((track) => track.stop())
      probeStreamRef.current = null
    },
    [],
  )

  async function takeOver() {
    const token = await getAccessToken()
    if (!token) {
      setError("Your authentication needs to be refreshed.")
      setAvailabilityPending(false)
      return
    }
    const result = await acquireSoftphone({
      client: portalClient(token),
      body: { sessionId: sessionID, takeover: true },
    }).catch(() => undefined)
    if (!result?.data?.owner) {
      setError("This browser could not take over the softphone.")
      setAvailabilityPending(false)
      return
    }
    if (!ownerRef.current) ownerGenerationRef.current += 1
    ownerRef.current = true
    setLease(result.data)
    await connectMedia(result.data)
    await refreshCallingState()
    await refreshCall()
  }

  async function pauseCalling() {
    setAvailabilityPending(true)
    rememberAvailabilityIntent(false)
    await updateReadiness(false)
  }

  async function resumeCalling() {
    setAvailabilityPending(true)
    rememberAvailabilityIntent(true)
    await updateReadiness(true)
  }

  async function setAvailabilityIntent(nextAvailable: boolean) {
    setError("")
    if (!nextAvailable) {
      await pauseCalling()
      return
    }
    setAvailabilityPending(true)
    rememberAvailabilityIntent(true)
    if (!lease?.owner) {
      await takeOver()
      return
    }
    if (mediaState !== "ready") {
      await connectMedia(lease)
      return
    }
    await resumeCalling()
  }

  async function answerRingingLeg(ringingLeg: RingingCallLeg) {
    if (answeredInboundLegRef.current) return
    setError("")
    setCallingNotice("")
    setAudioIssue(false)
    const leg = incomingLegsRef.current.get(ringingLeg.mediaToken)
    if (!leg) {
      setReadyIncomingMediaTokens((current) => {
        if (!current.has(ringingLeg.mediaToken)) return current
        const next = new Set(current)
        next.delete(ringingLeg.mediaToken)
        return next
      })
      return
    }
    answeredInboundLegRef.current = {
      callId: ringingLeg.callId,
      callLegId: ringingLeg.callLegId,
      providerLegID: leg.providerLegID,
      mediaToken: leg.mediaToken,
    }
    setInboundCallControlReady(false)
    forgetIncomingLeg(leg)
    try {
      const outcome = await leg.answer()
      if (outcome === "ended") {
        setCallingNotice("This call is no longer available in this browser.")
        await refreshCallingState(true)
        return
      }
    } catch (error) {
      clearAnsweredInboundLeg()
      clearMediaAttachment()
      rememberIncomingLeg(leg)
      setError(microphoneFailureMessage(error))
      await refreshCallingState()
      return
    }
    mediaLegRef.current = leg
    setMediaAttached(true)
    setExpectedCallID(ringingLeg.callId)
    expectedCallRef.current = ringingLeg.callId
    setAvailable(false)
    availabilityRef.current = false
    ringtoneRef.current?.()
    ringtoneRef.current = null
    setRingingLegs((current) =>
      current.filter((item) => item.callLegId !== ringingLeg.callLegId),
    )
    await refreshCallingState()
    await refreshCall()
  }

  async function hangup() {
    if (!activeCall || endingCallID) return
    const callID = activeCall.id
    setEndingCallID(callID)
    setError("")
    const token = await getAccessToken()
    if (!token) {
      setEndingCallID("")
      setError(
        "Your authentication needs to be refreshed before you try End again.",
      )
      return
    }
    const result = await requestCallingHangup({
      client: portalClient(token),
      path: { callId: callID },
      body: { sessionId: sessionID },
    }).catch(() => undefined)
    if (!result?.data) {
      setEndingCallID((current) => (current === callID ? "" : current))
      const failure = hangupFailure({
        status: result?.response?.status,
        code: result?.error?.error.code,
      })
      if (failure === "conflict") {
        const refreshed = await refreshCall(callID)
        if (refreshed?.call && callIsSettled(refreshed.call.state)) {
          setError("")
          return
        }
        const reconciliationFailure = hangupFailure({
          status: refreshed?.status,
        })
        if (reconciliationFailure === "authentication") {
          setError(
            "Your authentication or Call access needs to be refreshed before you try End again.",
          )
          return
        }
        if (reconciliationFailure === "retry") {
          setError(
            "End status could not be refreshed. Check your connection and try again.",
          )
          return
        }
        setError("Calling ownership or the Call state changed before End.")
        return
      }
      if (failure === "authentication") {
        setError(
          "Your authentication or Call access needs to be refreshed before you try End again.",
        )
        return
      }
      if (failure === "retry") {
        setError(
          "End was not committed. Check your connection and try again.",
        )
        return
      }
      setError("End is not available for the current Call.")
      return
    }
    if (result.data.state === "NEEDS_DISPOSITION") {
      setPendingOutcome(result.data)
      applyActiveCall()
      setExpectedCallID("")
      expectedCallRef.current = ""
      mediaLegRef.current = null
      clearAnsweredInboundLeg()
      setMediaAttached(false)
      setMuted(false)
      if (
        mediaState === "ready" &&
        ownerRef.current &&
        availabilityIntentRef.current
      ) {
        setAvailabilityPending(true)
        await updateReadiness(true, "ready")
      }
      return
    }
    applyActiveCall(result.data)
  }

  function toggleMute() {
    if (!mediaLegRef.current) return
    if (muted) mediaLegRef.current.unmute()
    else mediaLegRef.current.mute()
    setMuted(!muted)
  }

  function sendDTMF(digit: string) {
    if (!mediaLegRef.current?.sendDTMF(digit)) {
      setError(
        "The keypad is available only on the current connected media leg.",
      )
    }
  }

  async function retryCall() {
    if (!activeCall?.retryAllowed) return
    const token = await getAccessToken()
    if (!token) return
    setCallingNotice("")
    setOutboundPending(true)
    setError("")
    const result = await retryOutboundCall({
      client: portalClient(token),
      path: { callId: activeCall.id },
      body: {
        sessionId: sessionID,
        idempotencyKey: window.crypto.randomUUID(),
      },
    }).catch(() => undefined)
    setOutboundPending(false)
    if (!result?.data) {
      setError("The retry could not be committed as a new Call attempt.")
      return
    }
    await adapterRef.current?.disconnect()
    adapterRef.current = null
    mediaLegRef.current = null
    clearAnsweredInboundLeg()
    setMediaAttached(false)
    setMuted(false)
    expectedCallRef.current = result.data.id
    setExpectedCallID(result.data.id)
    applyActiveCall(result.data)
    setAvailable(false)
    availabilityRef.current = false
    await connectMedia(lease)
  }

  async function dispose(
    outcome:
      | "RESOLVED"
      | "FOLLOW_UP_REQUIRED"
      | "COMPLETE_TASK"
      | "KEEP_OPEN"
      | "CREATE_TASK"
      | "NO_FOLLOW_UP",
  ) {
    const call = pendingOutcome ?? activeCall
    if (!call || call.state !== "NEEDS_DISPOSITION") return
    const token = await getAccessToken()
    if (!token) return
    const result = await recordCallingDisposition({
      client: portalClient(token),
      path: { callId: call.id },
      body: { sessionId: sessionID, outcome },
    }).catch(() => undefined)
    if (!result?.data) {
      setError("The Call disposition could not be recorded.")
      return
    }
    onDisposition(result.data)
    setPendingOutcome(undefined)
    applyActiveCall()
    setExpectedCallID("")
    expectedCallRef.current = ""
    mediaLegRef.current = null
    clearAnsweredInboundLeg()
    setMediaAttached(false)
    setMuted(false)
    if (mediaState === "ready" && ownerRef.current) {
      const nextAvailable = availabilityIntentRef.current
      setAvailabilityPending(true)
      await updateReadiness(nextAvailable)
    }
  }

  const activeOffers = activeRingingOffers(ringingLegs, now)
  const earliest = activeOffers[0]
  const visiblePendingOutcome =
    pendingOutcome &&
    dispositionWindowIsOpen(pendingOutcome.dispositionDeadline, now)
      ? pendingOutcome
      : undefined
  const exactCallLegControlReady =
    activeCall?.direction !== "INBOUND" || inboundCallControlReady
  const visibleCallState =
    activeCall?.state === "CONNECTED" && !exactCallLegControlReady
      ? "CONNECTING"
      : activeCall?.state
  const activeCallEnding = activeCall
    ? activeCallEndPending(activeCall, endingCallID)
    : false
  return (
    <CallingNavigationContext.Provider
      value={{
        activeCall,
        availabilityError: error,
        availabilityPending,
        available,
        ownsSoftphone: Boolean(lease?.owner),
        outboundPending,
        callingEnabled,
        setAvailability: (nextAvailable) =>
          void setAvailabilityIntent(nextAvailable),
        startOutbound: startNumberCall,
      }}
    >
      {children}
      <audio id="acuity-calling-remote-audio" autoPlay className="hidden" />
      {(callingEnabled || lease?.owner) && earliest && !activeCall && (
        <div className="fixed inset-x-3 bottom-3 z-40 md:left-auto md:right-4 md:w-96">
          <IncomingCallControls
            ringingLegs={activeOffers}
            readyMediaTokens={readyIncomingMediaTokens}
            now={now}
            error={error}
            onAnswer={(leg) => void answerRingingLeg(leg)}
          />
        </div>
      )}
      {activeCall && (
        <div className="fixed inset-x-3 bottom-3 z-40 md:left-auto md:right-4 md:w-[26rem]">
          <Card role="region" aria-label="Active call controls" size="sm">
            <CardHeader>
              <CardTitle
                className={cn(
                  "flex min-w-0 items-center gap-2",
                  activeCall.direction === "OUTBOUND" &&
                    "text-2xl font-semibold tracking-tight",
                )}
              >
                {activeCall.direction === "OUTBOUND" ? (
                  <span className="truncate tabular-nums">
                    {formatPhone(activeCall.phone) || "Phone unavailable"}
                  </span>
                ) : (
                  <>
                    <Badge
                      variant={
                        visibleCallState === "CONNECTED"
                          ? "secondary"
                          : "outline"
                      }
                      className={cn(
                        visibleCallState === "CONNECTED" && "text-success",
                        activeCallEnding && "text-warning",
                        visibleCallState === "CONNECTING" && "text-warning",
                      )}
                    >
                      {activeCallEnding
                        ? "Ending…"
                        : callStateLabel(visibleCallState!)}
                    </Badge>
                    <span className="truncate">
                      {activeCall.displayName || "Caller"}
                    </span>
                  </>
                )}
              </CardTitle>
              <CardDescription className="truncate">
                {activeCall.direction === "OUTBOUND" ? (
                  activeCall.locationName
                ) : (
                  <>
                    {activeCall.phone || "Phone unavailable"} ·{" "}
                    {activeCall.locationName}
                  </>
                )}
              </CardDescription>
              <CardAction>
                <Badge
                  aria-label={
                    visibleCallState === "CONNECTED" &&
                    !activeCallEnding
                      ? "Call timer"
                      : "Call status"
                  }
                  variant="outline"
                  className="tabular-nums"
                >
                  {activeCallEnding
                    ? "Ending…"
                    : visibleCallState === "CONNECTING"
                      ? "Connecting"
                      : callTimerLabel(activeCall, now)}
                </Badge>
              </CardAction>
            </CardHeader>
            <CardContent className="flex flex-col gap-2 empty:hidden">
              <ActiveCallControls
                call={activeCall}
                mediaState={mediaState}
                mediaAttached={mediaAttached}
                controlReady={exactCallLegControlReady}
                audioIssue={audioIssue}
                owner={Boolean(lease?.owner)}
                muted={muted}
                onMute={toggleMute}
                onDTMF={sendDTMF}
                onHangup={() => void hangup()}
                onDispose={(outcome) => void dispose(outcome)}
                onRetry={() => void retryCall()}
                retryPending={outboundPending}
                controlsPending={activeCallEnding}
                onClose={() => {
                  applyActiveCall()
                  setExpectedCallID("")
                  expectedCallRef.current = ""
                  mediaLegRef.current = null
                  clearAnsweredInboundLeg()
                  setMediaAttached(false)
                }}
              />
              {error && <p className="text-destructive">{error}</p>}
            </CardContent>
          </Card>
        </div>
      )}
      {visiblePendingOutcome && !activeCall && !earliest && (
        <div className="fixed inset-x-3 bottom-3 z-40 md:left-auto md:right-4 md:w-[26rem]">
          <Card role="region" aria-label="Call outcome" size="sm">
            <CardHeader>
              <CardTitle>Call ended</CardTitle>
              <CardDescription>
                {visiblePendingOutcome.phone} ·{" "}
                {visiblePendingOutcome.locationName}
              </CardDescription>
              {visiblePendingOutcome.dispositionDeadline && (
                <CardAction>
                  <Badge
                    aria-label="Resolution countdown"
                    variant="outline"
                    className="tabular-nums"
                  >
                    {secondsRemaining(
                      visiblePendingOutcome.dispositionDeadline,
                      now,
                    )}
                    s
                  </Badge>
                </CardAction>
              )}
            </CardHeader>
            <CardContent className="flex flex-wrap gap-2">
              {callDispositionChoices(visiblePendingOutcome).map((choice) => (
                <Button
                  key={choice.outcome}
                  variant={choice.label === "Resolved" ? "default" : "outline"}
                  onClick={() => void dispose(choice.outcome)}
                >
                  {choice.label === "Resolved" && <CheckIcon />}
                  {choice.label}
                </Button>
              ))}
              {error && <p className="basis-full text-xs text-destructive">{error}</p>}
            </CardContent>
          </Card>
        </div>
      )}
      {(callingEnabled || lease?.owner) && !activeCall && !earliest && error && (
        <Alert
          aria-label="Calling status"
          variant="destructive"
          className="pointer-events-none fixed right-4 bottom-4 z-40 max-w-sm"
        >
          <ShieldAlertIcon />
          <AlertTitle>Calling needs attention</AlertTitle>
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      {(callingEnabled || lease?.owner) &&
        !activeCall &&
        !earliest &&
        !error &&
        callingNotice && (
          <Alert
            aria-label="Calling status"
            className="pointer-events-none fixed right-4 bottom-4 z-40 max-w-sm"
          >
            <PhoneCallIcon />
            <AlertTitle>Call ended</AlertTitle>
            <AlertDescription>{callingNotice}</AlertDescription>
          </Alert>
        )}
    </CallingNavigationContext.Provider>
  )
}

function IncomingCallControls({
  ringingLegs,
  readyMediaTokens,
  now,
  error,
  onAnswer,
}: {
  ringingLegs: RingingCallLeg[]
  readyMediaTokens: ReadonlySet<string>
  now: number
  error: string
  onAnswer: (leg: RingingCallLeg) => void
}) {
  if (ringingLegs.length === 0) return null
  return (
    <div
      role="region"
      aria-label="Incoming calls"
      className="flex max-h-[calc(100vh-1.5rem)] flex-col gap-2 overflow-y-auto p-px"
    >
      {ringingLegs.map((leg) => {
        const phone = formatPhone(leg.phone)
        const mediaReady = readyMediaTokens.has(leg.mediaToken)
        const displayName = leg.displayName.trim()
        const showDisplayName =
          Boolean(displayName) && displayName.toLowerCase() !== "incoming caller"
        return (
          <Card key={leg.callLegId} size="sm">
            <CardHeader>
              <CardTitle className="flex min-w-0 items-start gap-3 text-2xl font-semibold tracking-tight">
                <PhoneCallIcon className="mt-1 size-5 shrink-0 text-foreground" />
                <span className="truncate tabular-nums">{phone}</span>
              </CardTitle>
              <CardDescription className="pl-8 text-sm">
                {showDisplayName && <>{displayName} · </>}
                {leg.locationName}
              </CardDescription>
            </CardHeader>
            {leg.transferReason && (
              <CardContent>
                <p className="text-muted-foreground">{leg.transferReason}</p>
              </CardContent>
            )}
            <CardFooter className="justify-between">
              <Badge
                aria-label={`Incoming offer countdown for ${phone}`}
                variant="outline"
                className="tabular-nums text-foreground"
              >
                {offerSecondsRemaining(leg.deadline, now)}s
              </Badge>
              <div className="flex items-center gap-3">
                {!mediaReady && (
                  <span className="text-sm text-muted-foreground">
                    Connecting audio…
                  </span>
                )}
                <Button
                  size="sm"
                  aria-label={`Answer ${phone}`}
                  disabled={!mediaReady}
                  onClick={() => onAnswer(leg)}
                >
                  Answer
                </Button>
              </div>
            </CardFooter>
          </Card>
        )
      })}
      {error && <p className="text-destructive">{error}</p>}
    </div>
  )
}

function ActiveCallControls({
  call,
  mediaState,
  mediaAttached,
  controlReady,
  audioIssue,
  owner,
  muted,
  onMute,
  onDTMF,
  onHangup,
  onDispose,
  onRetry,
  retryPending,
  controlsPending,
  onClose,
}: {
  call: CallingCall
  mediaState: MediaState
  mediaAttached: boolean
  controlReady: boolean
  audioIssue: boolean
  owner: boolean
  muted: boolean
  onMute: () => void
  onDTMF: (digit: string) => void
  onHangup: () => void
  onDispose: (
    outcome:
      | "RESOLVED"
      | "FOLLOW_UP_REQUIRED"
      | "COMPLETE_TASK"
      | "KEEP_OPEN"
      | "CREATE_TASK"
      | "NO_FOLLOW_UP",
  ) => void
  onRetry: () => void
  retryPending: boolean
  controlsPending: boolean
  onClose: () => void
}) {
  const [keypadOpen, setKeypadOpen] = useState(false)
  const ended = call.state === "NEEDS_DISPOSITION"
  const keypadEligible =
    !controlsPending &&
    owner &&
    controlReady &&
    call.state === "CONNECTED" &&
    mediaState === "ready" &&
    mediaAttached
  const dispositionChoices = callDispositionChoices(call)
  const showCallDetails =
    call.direction !== "OUTBOUND" || Boolean(call.providerTermination)
  const showMute =
    owner &&
    controlReady &&
    (call.state === "CONNECTING" || call.state === "CONNECTED")
  const showKeypad = owner && controlReady && call.state === "CONNECTED"
  const showEnd =
    showActiveCallEndControl(call, owner) ||
    (call.direction !== "OUTBOUND" && showKeypad)
  const showDisposition = owner && ended
  const showRetry = call.retryAllowed
  const showClosedState = callIsSettled(call.state) && !ended
  if (
    !showCallDetails &&
    !showMute &&
    !showKeypad &&
    !showEnd &&
    !showDisposition &&
    !showRetry &&
    !showClosedState
  ) {
    return null
  }
  return (
    <div className="flex flex-wrap items-center gap-2">
      {showCallDetails && (
        <p className="basis-full truncate text-xs text-muted-foreground">
          {call.direction !== "OUTBOUND" && (
            <>
              {call.nameSource ? `Name: ${call.nameSource} · ` : ""}
              Audio:{" "}
              {audioIssue && mediaAttached && controlReady
                ? "no incoming audio detected"
                : mediaAttached && controlReady
                ? "connected"
                : mediaAttached
                  ? "waiting for provider bridge"
                : mediaState === "ready"
                  ? "waiting for exact leg"
                  : mediaState}
              {call.transferReason ? ` · ${call.transferReason}` : ""}
              {call.reasonSource ? ` (${call.reasonSource})` : ""}
            </>
          )}
          {call.providerTermination && (
            <>
              {call.direction !== "OUTBOUND" && " · "}
              {providerOutcomeLabel(call.providerTermination)}
            </>
          )}
        </p>
      )}
      {showEnd && (
        <div className="flex basis-full flex-col items-center gap-1">
          <Button
            size="icon"
            variant="destructive"
            aria-label={controlsPending ? "Ending" : "End"}
            className="size-12 rounded-full bg-destructive text-background hover:bg-destructive/85 hover:text-background"
            disabled={controlsPending}
            onClick={onHangup}
          >
            <PhoneOffIcon className="size-5" />
          </Button>
          <span className="text-xs font-medium text-foreground">
            {controlsPending ? "Ending…" : "End"}
          </span>
        </div>
      )}
      {showMute && (
        <Button
          size="sm"
          variant="outline"
          disabled={controlsPending || !mediaAttached}
          onClick={onMute}
        >
          {muted ? <MicOffIcon /> : <MicIcon />}
          {muted ? "Unmute" : "Mute"}
        </Button>
      )}
      {showKeypad && (
        <Button
          size="sm"
          variant="outline"
          disabled={!keypadEligible}
          onClick={() => setKeypadOpen((current) => !current)}
        >
          Keypad
        </Button>
      )}
      {showDisposition && (
        <>
          <Button
            size="sm"
            onClick={() => onDispose(dispositionChoices[0].outcome)}
          >
            <CheckIcon />
            {dispositionChoices[0].label}
          </Button>
          <Button
            size="sm"
            variant="outline"
            onClick={() => onDispose(dispositionChoices[1].outcome)}
          >
            {dispositionChoices[1].label}
          </Button>
        </>
      )}
      {showRetry && (
        <Button
          size="sm"
          variant="outline"
          disabled={retryPending}
          onClick={onRetry}
        >
          <RotateCcwIcon />
          {retryPending ? "Preparing…" : "Try again"}
        </Button>
      )}
      {showClosedState && (
        <>
          {(call.state === "RESOLVED" ||
            call.state === "FOLLOW_UP_REQUIRED") && (
            <span className="text-xs text-muted-foreground">
              Disposition saved
            </span>
          )}
          <Button size="sm" variant="ghost" onClick={onClose}>
            Close
          </Button>
        </>
      )}
      {keypadOpen && (
        <div className="basis-full border-t pt-2">
          <div className="grid w-48 grid-cols-3 gap-1">
            {["1", "2", "3", "4", "5", "6", "7", "8", "9", "*", "0", "#"].map(
              (digit) => (
                <Button
                  key={digit}
                  type="button"
                  size="sm"
                  variant="outline"
                  disabled={!keypadEligible}
                  aria-label={`Send ${digit}`}
                  onClick={() => onDTMF(digit)}
                >
                  {digit}
                </Button>
              ),
            )}
          </div>
        </div>
      )}
    </div>
  )
}

type DispositionChoice = {
  outcome:
    | "RESOLVED"
    | "FOLLOW_UP_REQUIRED"
    | "COMPLETE_TASK"
    | "KEEP_OPEN"
    | "CREATE_TASK"
    | "NO_FOLLOW_UP"
  label: string
}

function callDispositionChoices(
  call: CallingCall,
): [DispositionChoice, DispositionChoice] {
  if (call.direction !== "OUTBOUND") {
    return [
      { outcome: "RESOLVED", label: "Resolved" },
      { outcome: "FOLLOW_UP_REQUIRED", label: "Follow-up needed" },
    ]
  }
  if (call.entryPoint === "TASK") {
    return [
      { outcome: "COMPLETE_TASK", label: "Resolved" },
      { outcome: "KEEP_OPEN", label: "Follow-up needed" },
    ]
  }
  if (call.connectedAt) {
    return [
      { outcome: "RESOLVED", label: "Resolved" },
      { outcome: "CREATE_TASK", label: "Follow-up needed" },
    ]
  }
  return [
    { outcome: "NO_FOLLOW_UP", label: "No follow-up" },
    { outcome: "CREATE_TASK", label: "Create task" },
  ]
}

function browserSessionID() {
  if (typeof window === "undefined") return "server"
  const tabPrefix = "acuity-calling-tab:"
  if (!window.name.startsWith(tabPrefix)) {
    window.name = tabPrefix + window.crypto.randomUUID()
  }
  const tabID = window.name.slice(tabPrefix.length)
  const existing = window.sessionStorage.getItem(sessionStorageKey)
  if (existing) return `${existing}:${tabID}`
  const created = window.crypto.randomUUID()
  window.sessionStorage.setItem(sessionStorageKey, created)
  return `${created}:${tabID}`
}

function secondsRemaining(deadline: string, now: number) {
  return Math.max(0, Math.ceil((new Date(deadline).getTime() - now) / 1000))
}

function formatPhone(phone: string) {
  const match = phone.match(/^\+1(\d{3})(\d{3})(\d{4})$/)
  if (!match) return phone
  return `(${match[1]}) ${match[2]}-${match[3]}`
}

function callTimerLabel(call: CallingCall, now: number) {
	if (call.state === "NEEDS_DISPOSITION" && call.dispositionDeadline) {
		return `${secondsRemaining(call.dispositionDeadline, now)}s`
	}
  if (call.connectedAt) {
    const elapsed = Math.max(
      0,
      Math.floor((now - new Date(call.connectedAt).getTime()) / 1000),
    )
    const minutes = Math.floor(elapsed / 60)
    const seconds = String(elapsed % 60).padStart(2, "0")
    return `${minutes}:${seconds}`
  }
  if (
    call.state === "PREPARING" ||
    call.state === "RINGING" ||
    call.state === "CONNECTING" ||
    call.state === "VOICEMAIL_GREETING" ||
    call.state === "VOICEMAIL_RECORDING"
  ) {
    return callStateLabel(call.state)
  }
  return "Ended"
}

function callStateLabel(state: CallingCall["state"]) {
  switch (state) {
    case "PREPARING":
      return "Preparing"
    case "RINGING":
      return "Ringing"
    case "CONNECTING":
      return "Connecting"
    case "CONNECTED":
      return "Connected"
    case "VOICEMAIL_GREETING":
      return "Voicemail greeting"
    case "VOICEMAIL_RECORDING":
      return "Recording voicemail"
    case "NEEDS_DISPOSITION":
      return "Call ended"
    case "UNANSWERED":
      return "Not connected"
    case "VOICEMAIL":
      return "Voicemail"
    case "MISSED":
      return "Missed call"
    case "RESOLVED":
      return "Resolved"
    case "FOLLOW_UP_REQUIRED":
      return "Follow-up created"
  }
}

function startRingtone() {
  let context: AudioContext | undefined
  let timer = 0
  try {
    context = new AudioContext()
    const pulse = () => {
      if (!context) return
      const oscillator = context.createOscillator()
      const gain = context.createGain()
      oscillator.frequency.value = 440
      gain.gain.setValueAtTime(0.035, context.currentTime)
      gain.gain.exponentialRampToValueAtTime(
        0.0001,
        context.currentTime + 0.18,
      )
      oscillator.connect(gain).connect(context.destination)
      oscillator.start()
      oscillator.stop(context.currentTime + 0.18)
    }
    pulse()
    timer = window.setInterval(pulse, 1200)
  } catch {
    return () => undefined
  }
  return () => {
    window.clearInterval(timer)
    void context?.close()
  }
}
