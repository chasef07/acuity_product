"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import {
  CheckIcon,
  HeadphonesIcon,
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
import { Input } from "@/components/ui/input"
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select"
import {
  acceptCallingOffer,
  acquireSoftphone,
  confirmCallingMediaReady,
  getCallingCall,
  getOperatorCallingTimeline,
  issueCallingMediaToken,
  listCallingOffers,
  recordCallingDisposition,
  requestCallingHangup,
  retryOutboundCall,
  setCallingReadiness,
  startOutboundCall,
} from "@/lib/api/generated/sdk.gen"
import type {
  CallingCall,
  CallingDispositionResult,
  CallingOffer,
  Location,
  OperatorCallingTimeline,
  SoftphoneState,
} from "@/lib/api/generated/types.gen"
import { getAccessToken } from "@/lib/auth-client"
import {
  createCallingMediaAdapter,
  type CallingMediaAdapter,
  type IncomingMediaLeg,
  type MediaState,
} from "@/lib/calling/media-adapter"
import { providerOutcomeLabel } from "@/lib/calling/outcomes"
import { portalClient } from "@/lib/api/client"

type CallingDockProps = {
  platformOperator: boolean
  practiceID: string
  locations: Location[]
  hint: number
  taskCallRequest?: { id: string; taskID: string }
  onTaskCallHandled: (requestID: string, error?: string) => void
  onOffersChanged: (offers: CallingOffer[]) => void
  onCallChanged: (call: CallingCall | undefined) => void
  onDisposition: (result: CallingDispositionResult) => void
}

const sessionStorageKey = "acuity.callingSession"
const mediaEnabledStorageKey = "acuity.callingMediaEnabled"
const availabilityIntentStorageKey = "acuity.callingAvailabilityIntent"

export function CallingDock({
  platformOperator,
  practiceID,
  locations,
  hint,
  taskCallRequest,
  onTaskCallHandled,
  onOffersChanged,
  onCallChanged,
  onDisposition,
}: CallingDockProps) {
  const [sessionID] = useState(browserSessionID)
  const [lease, setLease] = useState<SoftphoneState>()
  const [mediaState, setMediaState] = useState<MediaState>("unavailable")
  const [available, setAvailable] = useState(false)
  const [offers, setOffers] = useState<CallingOffer[]>([])
  const [activeCall, setActiveCall] = useState<CallingCall>()
  const [expectedCallID, setExpectedCallID] = useState("")
  const [mediaAttached, setMediaAttached] = useState(false)
  const [muted, setMuted] = useState(false)
  const [error, setError] = useState("")
  const [showDialer, setShowDialer] = useState(false)
  const [dialLocationID, setDialLocationID] = useState("")
  const [dialDestination, setDialDestination] = useState("")
  const [outboundPending, setOutboundPending] = useState(false)
  const [now, setNow] = useState(() => Date.now())
  const adapterRef = useRef<CallingMediaAdapter | null>(null)
  const mediaLegRef = useRef<IncomingMediaLeg | null>(null)
  const probeStreamRef = useRef<MediaStream | null>(null)
  const expectedCallRef = useRef("")
  const ownerRef = useRef(false)
  const availabilityRef = useRef(false)
  const availabilityIntentRef = useRef(false)
  const announcedOffersRef = useRef(new Set<string>())
  const pendingMediaLegsRef = useRef(new Map<string, string>())
  const ringtoneRef = useRef<(() => void) | null>(null)
  const connectingRef = useRef(false)
  const handledTaskCallRef = useRef("")
  const notificationsRef = useRef(new Map<string, Notification>())
  const resolvedDialLocationID = locations.some(
    (location) => location.id === dialLocationID,
  )
    ? dialLocationID
    : (locations[0]?.id ?? "")

  useEffect(() => {
    expectedCallRef.current = expectedCallID
  }, [expectedCallID])
  useEffect(() => {
    ownerRef.current = Boolean(lease?.owner)
  }, [lease?.owner])
  useEffect(() => {
    availabilityRef.current = available
  }, [available])
  useEffect(() => {
    onOffersChanged(offers)
  }, [offers, onOffersChanged])
  useEffect(() => {
    onCallChanged(activeCall)
  }, [activeCall, onCallChanged])
  useEffect(
    () => () => {
      onOffersChanged([])
    },
    [onOffersChanged],
  )

  const updateReadiness = useCallback(
    async (nextAvailable: boolean, nextMediaState = mediaState) => {
      const token = await getAccessToken()
      if (!token) return undefined
      const probeTrack = probeStreamRef.current?.getAudioTracks()[0]
      const technicallyReady =
        nextMediaState === "ready" && probeTrack?.readyState === "live"
      const result = await setCallingReadiness({
        client: portalClient(token),
        body: {
          sessionId: sessionID,
          registered: technicallyReady,
          microphoneReady: technicallyReady,
          audioReady: technicallyReady,
          sessionHealthy: technicallyReady,
          available: nextAvailable,
        },
      }).catch(() => undefined)
      if (result?.data) {
        setLease(result.data)
        setAvailable(result.data.available)
        availabilityRef.current = result.data.available
      } else {
        setAvailable(false)
        availabilityRef.current = false
      }
      return result?.data
    },
    [mediaState, sessionID],
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
      if (!lease?.owner) return "Enable or take over calling first."
      if (mediaState !== "ready") return "Browser calling audio is not ready."
      if (activeCall || expectedCallRef.current) {
        return "Finish the active Call before starting another."
      }
      const token = await getAccessToken()
      if (!token) return "Your authentication needs to be refreshed."
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
      setActiveCall(result.data)
      setAvailable(false)
      availabilityRef.current = false
      setShowDialer(false)
      setDialDestination("")
      return undefined
    },
    [activeCall, lease?.owner, mediaState, sessionID],
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

  const handleIncoming = useCallback(
    async (leg: IncomingMediaLeg) => {
      const attached = mediaLegRef.current
      const replacesAttachedRecovery =
        attached &&
        leg.recovery &&
        attached.providerLegID === leg.providerLegID &&
        attached.mediaToken === leg.mediaToken
      if (attached && !replacesAttachedRecovery) {
        if (
          attached.providerLegID !== leg.providerLegID ||
          attached.mediaToken !== leg.mediaToken
        ) {
          await leg.reject().catch(() => undefined)
        }
        return
      }
      const pendingProviderLegID = pendingMediaLegsRef.current.get(
        leg.mediaToken,
      )
      if (pendingProviderLegID) {
        if (pendingProviderLegID !== leg.providerLegID) {
          await leg.reject().catch(() => undefined)
        }
        return
      }
      pendingMediaLegsRef.current.set(leg.mediaToken, leg.providerLegID)
      try {
        for (let attempt = 0; attempt < 200; attempt += 1) {
          const token = await getAccessToken()
          if (!token) {
            await leg.reject().catch(() => undefined)
            return
          }
          const currentLease = await acquireSoftphone({
            client: portalClient(token),
            body: { sessionId: sessionID, takeover: false },
          }).catch(() => undefined)
          if (!currentLease?.data?.owner) {
            setLease(currentLease?.data)
            setAvailable(false)
            ownerRef.current = false
            await leg.reject().catch(() => undefined)
            return
          }
          setLease(currentLease.data)
          ownerRef.current = true
          const callID =
            expectedCallRef.current || currentLease.data.activeCallId
          if (!callID) {
            await new Promise((resolve) => window.setTimeout(resolve, 100))
            continue
          }
          if (!expectedCallRef.current) {
            expectedCallRef.current = callID
            setExpectedCallID(callID)
            setAvailable(false)
            availabilityRef.current = false
          }
          const current = await getCallingCall({
            client: portalClient(token),
            path: { callId: callID },
          }).catch(() => undefined)
          if (!current?.data) {
            await leg.reject().catch(() => undefined)
            return
          }
          if (
            current.data.expectedMediaToken &&
            current.data.expectedMediaToken !== leg.mediaToken
          ) {
            await leg.reject().catch(() => undefined)
            return
          }
          if (current.data.expectedMediaToken === leg.mediaToken) {
            const currentAttached = mediaLegRef.current
            const replacesCurrentRecovery =
              currentAttached &&
              leg.recovery &&
              currentAttached.providerLegID === leg.providerLegID &&
              currentAttached.mediaToken === leg.mediaToken
            if (currentAttached && !replacesCurrentRecovery) {
              await leg.reject().catch(() => undefined)
              return
            }
            await leg.answer()
            let attachedCall = current.data
            if (
              current.data.direction === "OUTBOUND" &&
              (current.data.state === "PREPARING" ||
                current.data.state === "RECONCILING")
            ) {
              let confirmed = false
              for (
                let confirmationAttempt = 0;
                confirmationAttempt < 100;
                confirmationAttempt += 1
              ) {
                const result = await confirmCallingMediaReady({
                  client: portalClient(token),
                  path: { callId: current.data.id },
                  body: {
                    sessionId: sessionID,
                    mediaToken: leg.mediaToken,
                    providerLegId: leg.providerLegID,
                  },
                }).catch(() => undefined)
                if (result?.data) {
                  attachedCall = result.data
                  confirmed = true
                  break
                }
                if (
                  result?.response?.status === 401 ||
                  result?.response?.status === 403
                ) {
                  break
                }
                await new Promise((resolve) => window.setTimeout(resolve, 100))
              }
              if (!confirmed) {
                throw new Error("staff media readiness was not committed")
              }
            }
            mediaLegRef.current = leg
            setMediaAttached(true)
            setActiveCall(attachedCall)
            return
          }
          if (
            current.data.state !== "PREPARING" &&
            current.data.state !== "RINGING" &&
            current.data.state !== "CONNECTING" &&
            current.data.state !== "RECONCILING" &&
            current.data.state !== "CONNECTED"
          ) {
            await leg.reject().catch(() => undefined)
            return
          }
          await new Promise((resolve) => window.setTimeout(resolve, 100))
        }
        await leg.reject().catch(() => undefined)
        setError("The browser audio leg did not become authoritative before the connection deadline.")
        setAvailable(false)
        await updateReadiness(false, "unavailable")
      } catch {
        await leg.reject().catch(() => undefined)
        setError("The accepted browser audio leg could not be answered.")
        setAvailable(false)
        await updateReadiness(false, "unavailable")
      } finally {
        if (
          pendingMediaLegsRef.current.get(leg.mediaToken) ===
          leg.providerLegID
        ) {
          pendingMediaLegsRef.current.delete(leg.mediaToken)
        }
      }
    },
    [sessionID, updateReadiness],
  )

  const connectMedia = useCallback(async () => {
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
      return
    }
    const acquired = await acquireSoftphone({
      client: portalClient(token),
      body: { sessionId: sessionID, takeover: false },
    }).catch(() => undefined)
    if (!acquired?.data) {
      setError("The softphone lease is temporarily unavailable.")
      return
    }
    setLease(acquired.data)
    if (!acquired.data.owner) {
      if (acquired.data.activeCallId) {
        expectedCallRef.current = acquired.data.activeCallId
        setExpectedCallID(acquired.data.activeCallId)
        const observed = await getCallingCall({
          client: portalClient(token),
          path: { callId: acquired.data.activeCallId },
        }).catch(() => undefined)
        if (observed?.data) setActiveCall(observed.data)
      }
      return
    }
    if (acquired.data.activeCallId) {
      expectedCallRef.current = acquired.data.activeCallId
      setExpectedCallID(acquired.data.activeCallId)
      setAvailable(false)
      availabilityRef.current = false
      const restored = await getCallingCall({
        client: portalClient(token),
        path: { callId: acquired.data.activeCallId },
      }).catch(() => undefined)
      if (restored?.data) {
        setActiveCall(restored.data)
        setMediaAttached(false)
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
      microphone.addEventListener("ended", () => {
        setAvailable(false)
        availabilityRef.current = false
        void updateReadiness(false, "unavailable")
      }, { once: true })
      const audioContext = new AudioContext()
      await audioContext.resume()
      await audioContext.close()
      if ("Notification" in window && Notification.permission === "default") {
        await Notification.requestPermission()
      }
    } catch {
      setError("Microphone and browser audio permission are required.")
      return
    }
    const issued = await issueCallingMediaToken({
      client: portalClient(token),
      body: { sessionId: sessionID },
    }).catch(() => undefined)
    if (!issued?.data) {
      setError("Calling credentials are still being prepared. Try again shortly.")
      return
    }
    if (!document.querySelector("#acuity-calling-remote-audio")) {
      setError("The browser audio output is unavailable.")
      return
    }
    await adapterRef.current?.disconnect()
    const adapter = createCallingMediaAdapter()
    adapterRef.current = adapter
    await adapter.connect(issued.data.token, "acuity-calling-remote-audio", {
      onState: (state) => {
        setMediaState(state)
        if (state === "ready") {
          const mayReceiveOffers =
            availabilityIntentRef.current &&
            expectedCallRef.current === ""
          setAvailable(mayReceiveOffers)
          availabilityRef.current = mayReceiveOffers
          void updateReadiness(mayReceiveOffers, state)
        } else if (state === "unavailable" || state === "reconnecting") {
          setAvailable(false)
          availabilityRef.current = false
          void updateReadiness(false, state)
        }
      },
      onIncoming: (leg) => void handleIncoming(leg),
    })
    window.sessionStorage.setItem(mediaEnabledStorageKey, "true")
    } finally {
      connectingRef.current = false
    }
  }, [
    handleIncoming,
    rememberAvailabilityIntent,
    sessionID,
    updateReadiness,
  ])

  const refreshOffers = useCallback(async () => {
    if (!ownerRef.current || !availabilityRef.current) {
      setOffers([])
      return
    }
    const token = await getAccessToken()
    if (!token) return
    const result = await listCallingOffers({
      client: portalClient(token),
    }).catch(() => undefined)
    if (!result?.data) return
    setOffers(result.data.items)
  }, [])

  const refreshCall = useCallback(async () => {
    const callID = expectedCallRef.current
    if (!callID) return
    const token = await getAccessToken()
    if (!token) return
    const result = await getCallingCall({
      client: portalClient(token),
      path: { callId: callID },
    }).catch(() => undefined)
    if (expectedCallRef.current !== callID) return
    if (result?.data) {
      setActiveCall(result.data)
      if (
        result.data.state === "UNANSWERED" ||
        result.data.state === "MISSED" ||
        result.data.state === "VOICEMAIL" ||
        result.data.state === "NEEDS_DISPOSITION" ||
        result.data.state === "RESOLVED" ||
        result.data.state === "FOLLOW_UP_REQUIRED"
      ) {
        setMediaAttached(false)
        mediaLegRef.current = null
      }
      if (
        result.data.state === "UNANSWERED" &&
        ownerRef.current &&
        mediaState === "ready" &&
        availabilityIntentRef.current &&
        !availabilityRef.current
      ) {
        setAvailable(true)
        availabilityRef.current = true
        void updateReadiness(true, "ready")
        setActiveCall(undefined)
        setExpectedCallID("")
        expectedCallRef.current = ""
      }
    } else if (
      result?.response?.status === 403 ||
      result?.response?.status === 409
    ) {
      setActiveCall(undefined)
      setExpectedCallID("")
      expectedCallRef.current = ""
      mediaLegRef.current = null
      setMediaAttached(false)
      if (
        ownerRef.current &&
        mediaState === "ready" &&
        availabilityIntentRef.current &&
        !availabilityRef.current
      ) {
        setAvailable(true)
        availabilityRef.current = true
        void updateReadiness(true, "ready")
      }
    }
  }, [mediaState, updateReadiness])

  const refreshOwnership = useCallback(async () => {
    const restoreMedia =
      ownerRef.current ||
      window.sessionStorage.getItem(mediaEnabledStorageKey) === "true"
    if (!restoreMedia) return
    const token = await getAccessToken()
    if (!token) return
    const result = await acquireSoftphone({
      client: portalClient(token),
      body: { sessionId: sessionID, takeover: false },
    }).catch(() => undefined)
    if (!result?.data?.owner) {
      setLease(result?.data)
      if (result?.data?.activeCallId) {
        expectedCallRef.current = result.data.activeCallId
        setExpectedCallID(result.data.activeCallId)
        const observed = await getCallingCall({
          client: portalClient(token),
          path: { callId: result.data.activeCallId },
        }).catch(() => undefined)
        if (observed?.data) setActiveCall(observed.data)
      } else {
        expectedCallRef.current = ""
        setExpectedCallID("")
        setActiveCall(undefined)
      }
      setAvailable(false)
      availabilityRef.current = false
      ownerRef.current = false
      setMediaState("unavailable")
      mediaLegRef.current = null
      setMediaAttached(false)
      setMuted(false)
      await adapterRef.current?.disconnect()
      adapterRef.current = null
      probeStreamRef.current?.getTracks().forEach((track) => track.stop())
      probeStreamRef.current = null
      return
    }
    setLease(result.data)
    ownerRef.current = true
    if (result.data.activeCallId) {
      if (expectedCallRef.current !== result.data.activeCallId) {
        expectedCallRef.current = result.data.activeCallId
        setExpectedCallID(result.data.activeCallId)
      }
      setAvailable(false)
      availabilityRef.current = false
      await refreshCall()
    }
    if (!adapterRef.current) {
      await connectMedia()
      return
    }
    await updateReadiness(
      availabilityIntentRef.current && expectedCallRef.current === "",
    )
  }, [connectMedia, refreshCall, sessionID, updateReadiness])

  useEffect(() => {
    if (platformOperator) return
    const timeout = window.setTimeout(() => {
      void refreshOwnership()
      void refreshOffers()
      void refreshCall()
    }, 0)
    return () => window.clearTimeout(timeout)
  }, [hint, platformOperator, refreshCall, refreshOffers, refreshOwnership])

  useEffect(() => {
    if (platformOperator || !lease?.owner) return
    const interval = window.setInterval(() => {
      void refreshOffers()
      void refreshCall()
    }, 1000)
    return () => window.clearInterval(interval)
  }, [lease?.owner, platformOperator, refreshCall, refreshOffers])

  useEffect(() => {
    if (platformOperator || !lease?.owner) return
    const interval = window.setInterval(() => void refreshOwnership(), 5_000)
    return () => window.clearInterval(interval)
  }, [lease?.owner, platformOperator, refreshOwnership])

  useEffect(() => {
    const activeOfferIDs = new Set(offers.map((offer) => offer.id))
    for (const [offerID, notification] of notificationsRef.current) {
      if (activeOfferIDs.has(offerID)) continue
      notification.close()
      notificationsRef.current.delete(offerID)
      announcedOffersRef.current.delete(offerID)
    }
    if (offers.length === 0) {
      ringtoneRef.current?.()
      ringtoneRef.current = null
      return
    }
    if (!ringtoneRef.current) ringtoneRef.current = startRingtone()
    for (const offer of offers) {
      if (announcedOffersRef.current.has(offer.id)) continue
      announcedOffersRef.current.add(offer.id)
      if (
        document.hidden &&
        "Notification" in window &&
        Notification.permission === "granted"
      ) {
        const notification = new Notification("Incoming Acuity transfer", {
          body: `${offer.locationName} · answer in Acuity`,
          tag: offer.id,
        })
        notificationsRef.current.set(offer.id, notification)
      }
    }
  }, [offers])

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
    if (!token) return
    const result = await acquireSoftphone({
      client: portalClient(token),
      body: { sessionId: sessionID, takeover: true },
    }).catch(() => undefined)
    if (!result?.data?.owner) {
      setError("This browser could not take over the softphone.")
      return
    }
    setLease(result.data)
    await connectMedia()
  }

  async function pauseCalling() {
    rememberAvailabilityIntent(false)
    await updateReadiness(false)
  }

  async function resumeCalling() {
    rememberAvailabilityIntent(true)
    await updateReadiness(true)
  }

  async function acceptOffer(offer: CallingOffer) {
    const token = await getAccessToken()
    if (!token) return
    setError("")
    const result = await acceptCallingOffer({
      client: portalClient(token),
      path: { callId: offer.id },
      body: { sessionId: sessionID },
    }).catch(() => undefined)
    if (!result?.data) {
      setError("The offer could not be claimed. It may have changed.")
      await refreshOffers()
      return
    }
    if (result.data.status !== "ACCEPTED") {
      setError(acceptanceMessage(result.data.status))
      await refreshOffers()
      return
    }
    setExpectedCallID(result.data.callId)
    expectedCallRef.current = result.data.callId
    setAvailable(false)
    availabilityRef.current = false
    ringtoneRef.current?.()
    ringtoneRef.current = null
    setOffers((current) => current.filter((item) => item.id !== offer.id))
    await refreshCall()
  }

  async function hangup() {
    if (!activeCall) return
    const token = await getAccessToken()
    if (!token) return
    const result = await requestCallingHangup({
      client: portalClient(token),
      path: { callId: activeCall.id },
      body: { sessionId: sessionID },
    }).catch(() => undefined)
    if (!result?.data) {
      setError("The hangup request could not be committed.")
      return
    }
    setActiveCall(result.data)
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
    setMediaAttached(false)
    setMuted(false)
    expectedCallRef.current = result.data.id
    setExpectedCallID(result.data.id)
    setActiveCall(result.data)
    setAvailable(false)
    availabilityRef.current = false
    await connectMedia()
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
    if (!activeCall) return
    const token = await getAccessToken()
    if (!token) return
    const result = await recordCallingDisposition({
      client: portalClient(token),
      path: { callId: activeCall.id },
      body: { sessionId: sessionID, outcome },
    }).catch(() => undefined)
    if (!result?.data) {
      setError("The Call disposition could not be recorded.")
      return
    }
    onDisposition(result.data)
    setActiveCall(undefined)
    setExpectedCallID("")
    expectedCallRef.current = ""
    mediaLegRef.current = null
    setMediaAttached(false)
    setMuted(false)
    if (mediaState === "ready" && ownerRef.current) {
      const nextAvailable = availabilityIntentRef.current
      setAvailable(nextAvailable)
      availabilityRef.current = nextAvailable
      const readiness = await updateReadiness(nextAvailable)
      availabilityRef.current = readiness?.available ?? false
    }
  }

  if (platformOperator) return <OperatorCallInspector />

  const earliest = offers[0]
  return (
    <section
      aria-label="Call Center"
      className="border-b bg-muted/20 px-4 py-2"
    >
      <audio id="acuity-calling-remote-audio" autoPlay className="hidden" />
      <div className="flex min-h-9 flex-wrap items-center gap-2">
        <HeadphonesIcon className="size-4 text-primary" />
        <span className="text-xs font-semibold uppercase tracking-[0.16em]">
          Call Center
        </span>
        {!lease?.owner && (
          <>
            <Badge variant="outline">Inactive in this browser</Badge>
            <Button size="sm" onClick={() => void (lease ? takeOver() : connectMedia())}>
              {lease ? "Take over softphone" : "Enable calling"}
            </Button>
          </>
        )}
        {lease?.owner && mediaState !== "ready" && (
          <>
            <Badge variant="outline">
              {mediaState === "reconnecting" ? "Reconnecting" : "Audio not ready"}
            </Badge>
            <Button size="sm" onClick={() => void connectMedia()}>
              Enable calling
            </Button>
          </>
        )}
        {lease?.owner && mediaState === "ready" && (
          <>
            <Badge variant={available ? "secondary" : "outline"}>
              {available ? "Available" : "Paused"}
            </Badge>
            <Button
              size="sm"
              variant="ghost"
              onClick={() => void (available ? pauseCalling() : resumeCalling())}
            >
              {available ? "Pause calls" : "Become available"}
            </Button>
            {!activeCall && (
              <Button
                size="sm"
                variant="outline"
                onClick={() => setShowDialer((current) => !current)}
              >
                <PhoneCallIcon />
                New call
              </Button>
            )}
          </>
        )}
        {earliest && !activeCall && (
          <div className="ml-auto flex min-w-0 items-center gap-2">
            <PhoneCallIcon className="size-4 animate-pulse motion-reduce:animate-none" />
            <span className="truncate text-sm font-medium">
              {earliest.displayName || "Incoming caller"} · {earliest.locationName}
              {earliest.nameSource ? ` · ${earliest.nameSource}` : ""}
            </span>
            {earliest.transferReason && (
              <span className="hidden max-w-64 truncate text-xs text-muted-foreground md:inline">
                {earliest.transferReason}
                {earliest.reasonSource ? ` · ${earliest.reasonSource}` : ""}
              </span>
            )}
            {offers.length > 1 && (
              <Badge variant="secondary">{offers.length} incoming</Badge>
            )}
            <Badge variant="outline">
              {secondsRemaining(earliest.deadline, now)}s
            </Badge>
            <Button size="sm" onClick={() => void acceptOffer(earliest)}>
              Accept
            </Button>
          </div>
        )}
      </div>
      {showDialer && lease?.owner && mediaState === "ready" && !activeCall && (
        <form
          aria-label="Standalone outbound call"
          className="mt-2 flex flex-wrap items-end gap-2 rounded-md border bg-background p-2"
          onSubmit={(event) => {
            event.preventDefault()
            void commitOutbound(window.crypto.randomUUID(), {
              practiceId: practiceID,
              locationId: resolvedDialLocationID,
              destination: dialDestination.trim(),
            }).then((requestError) => {
              if (requestError) setError(requestError)
            })
          }}
        >
          <label className="grid gap-1 text-xs">
            <span className="text-muted-foreground">Call office</span>
            <NativeSelect
              aria-label="Call office"
              className="h-8 min-w-48"
              value={resolvedDialLocationID}
              onChange={(event) => setDialLocationID(event.target.value)}
            >
              {locations.map((location) => (
                <NativeSelectOption key={location.id} value={location.id}>
                  {location.name}
                </NativeSelectOption>
              ))}
            </NativeSelect>
          </label>
          <label className="grid min-w-56 flex-1 gap-1 text-xs">
            <span className="text-muted-foreground">
              Destination · US +1 E.164
            </span>
            <Input
              aria-label="Outbound destination"
              className="h-8 font-mono"
              placeholder="+15555550100"
              value={dialDestination}
              onChange={(event) => setDialDestination(event.target.value)}
            />
          </label>
          <Button
            size="sm"
            type="submit"
            disabled={
              outboundPending ||
              !resolvedDialLocationID ||
              !/^\+1[2-9][0-9]{2}[2-9][0-9]{6}$/.test(dialDestination.trim())
            }
          >
            {outboundPending ? "Preparing…" : "Call"}
          </Button>
        </form>
      )}
      {offers.length > 1 && !activeCall && (
        <div className="mt-2 flex gap-2 overflow-x-auto pb-1">
          {offers.slice(1).map((offer) => (
            <button
              key={offer.id}
              className="flex shrink-0 items-center gap-2 rounded-md border bg-background px-3 py-2 text-left text-xs"
              onClick={() => void acceptOffer(offer)}
            >
              <span className="font-medium">
                {offer.displayName || "Incoming caller"}
                {offer.nameSource ? ` · ${offer.nameSource}` : ""}
              </span>
              <span className="text-muted-foreground">{offer.locationName}</span>
              <span className="font-mono">
                {secondsRemaining(offer.deadline, now)}s
              </span>
            </button>
          ))}
        </div>
      )}
      {activeCall && (
        <ActiveCallControls
          call={activeCall}
          mediaState={mediaState}
          mediaAttached={mediaAttached}
          owner={Boolean(lease?.owner)}
          now={now}
          muted={muted}
          onMute={toggleMute}
          onDTMF={sendDTMF}
          onHangup={() => void hangup()}
          onDispose={(outcome) => void dispose(outcome)}
          onRetry={() => void retryCall()}
          retryPending={outboundPending}
          onClose={() => {
            setActiveCall(undefined)
            setExpectedCallID("")
            expectedCallRef.current = ""
            mediaLegRef.current = null
            setMediaAttached(false)
          }}
        />
      )}
      {error && (
        <Alert variant="destructive" className="mt-2 py-2">
          <ShieldAlertIcon />
          <AlertTitle>Calling needs attention</AlertTitle>
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
    </section>
  )
}

function OperatorCallInspector() {
  const [callID, setCallID] = useState("")
  const [timeline, setTimeline] = useState<OperatorCallingTimeline>()
  const [error, setError] = useState("")

  async function inspect() {
    const token = await getAccessToken()
    if (!token || !callID.trim()) return
    setError("")
    const result = await getOperatorCallingTimeline({
      client: portalClient(token),
      path: { callId: callID.trim() },
    }).catch(() => undefined)
    if (!result?.data) {
      setTimeline(undefined)
      setError("That Call timeline is unavailable to this operator.")
      return
    }
    setTimeline(result.data)
  }

  return (
    <section
      aria-label="Call diagnostics"
      className="border-b bg-muted/30 px-4 py-3"
    >
      <form
        className="flex flex-wrap items-center gap-2"
        onSubmit={(event) => {
          event.preventDefault()
          void inspect()
        }}
      >
        <HeadphonesIcon className="size-4" />
        <span className="text-xs font-semibold uppercase tracking-[0.16em]">
          Call diagnostics
        </span>
        <Input
          aria-label="Call ID"
          className="h-8 min-w-72 flex-1 font-mono text-xs"
          placeholder="Call UUID"
          value={callID}
          onChange={(event) => setCallID(event.target.value)}
        />
        <Button size="sm" type="submit">
          Inspect
        </Button>
      </form>
      {error && <p className="mt-2 text-sm text-destructive">{error}</p>}
      {timeline && (
        <div className="mt-3 rounded-md border bg-background p-3">
          <div className="flex flex-wrap items-center gap-2">
            <Badge variant="outline">{timeline.state}</Badge>
            <span className="font-mono text-xs">{timeline.callId}</span>
            <span className="text-xs text-muted-foreground">
              version {timeline.version}
            </span>
          </div>
          <ol className="mt-2 max-h-64 space-y-2 overflow-y-auto">
            {timeline.entries.map((entry, index) => (
              <li
                key={`${entry.occurredAt}:${entry.kind}:${index}`}
                className="grid gap-1 border-t pt-2 text-xs sm:grid-cols-[minmax(12rem,1fr)_2fr]"
              >
                <span className="font-mono">{entry.kind}</span>
                <span className="text-muted-foreground">
                  {entry.commandAction &&
                    `${entry.commandAction} ${entry.commandState} · ${entry.commandAttempts} attempts`}
                  {entry.receiptState && `receipt ${entry.receiptState}`}
                  {entry.errorCode && ` · ${entry.errorCode}`}
                  {` · age ${entry.ageSeconds}s`}
                  {entry.opaqueReference && ` · ref ${entry.opaqueReference}`}
                  {` · ${new Date(entry.occurredAt).toLocaleString()}`}
                </span>
              </li>
            ))}
          </ol>
        </div>
      )}
    </section>
  )
}

function ActiveCallControls({
  call,
  mediaState,
  mediaAttached,
  owner,
  now,
  muted,
  onMute,
  onDTMF,
  onHangup,
  onDispose,
  onRetry,
  retryPending,
  onClose,
}: {
  call: CallingCall
  mediaState: MediaState
  mediaAttached: boolean
  owner: boolean
  now: number
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
  onClose: () => void
}) {
  const [keypadOpen, setKeypadOpen] = useState(false)
  const ended = call.state === "NEEDS_DISPOSITION"
  const terminal =
    call.state === "RESOLVED" || call.state === "FOLLOW_UP_REQUIRED"
  const closedWithoutDisposition =
    call.state === "UNANSWERED" ||
    call.state === "VOICEMAIL" ||
    call.state === "MISSED"
  const keypadEligible =
    owner &&
    call.state === "CONNECTED" &&
    mediaState === "ready" &&
    mediaAttached
  const dispositionChoices = callDispositionChoices(call)
  return (
    <div className="mt-2 flex flex-wrap items-center gap-2 rounded-md border bg-background px-3 py-2">
      <Badge variant={call.state === "CONNECTED" ? "secondary" : "outline"}>
        {callStateLabel(call.state)}
      </Badge>
      <div className="min-w-0 flex-1">
        <p className="truncate text-sm font-medium">
          {call.displayName ||
            (call.direction === "OUTBOUND" ? "Outbound call" : "Caller")}{" "}
          · {call.locationName}
        </p>
        <p className="truncate text-xs text-muted-foreground">
          {call.phone || "Phone unavailable"}
          {call.phoneSource ? ` (${call.phoneSource})` : ""} ·{" "}
          {call.transferReason || "No transfer note"}
          {call.reasonSource ? ` (${call.reasonSource})` : ""}
        </p>
        <p className="truncate text-xs text-muted-foreground">
          {call.nameSource ? `Name: ${call.nameSource} · ` : ""}
          Audio: {mediaAttached ? "attached" : mediaState === "ready" ? "waiting for exact leg" : mediaState}
          {call.connectedAt
            ? ` · ${Math.max(0, Math.floor((now - new Date(call.connectedAt).getTime()) / 1000))}s`
            : ""}
          {call.callerId ? ` · Caller ID ${call.callerId}` : ""}
          {call.providerTermination
            ? ` · ${providerOutcomeLabel(call.providerTermination)}`
            : ""}
        </p>
      </div>
      {owner && (call.state === "CONNECTING" ||
        call.state === "RECONCILING" ||
        call.state === "CONNECTED") && (
        <Button
          size="sm"
          variant="outline"
          disabled={!mediaAttached}
          onClick={onMute}
        >
          {muted ? <MicOffIcon /> : <MicIcon />}
          {muted ? "Unmute" : "Mute"}
        </Button>
      )}
      {owner && call.state === "CONNECTED" && (
        <>
          <Button
            size="sm"
            variant="outline"
            disabled={!keypadEligible}
            onClick={() => setKeypadOpen((current) => !current)}
          >
            Keypad
          </Button>
          <Button size="sm" variant="destructive" onClick={onHangup}>
            <PhoneOffIcon />
            Hang up
          </Button>
        </>
      )}
      {owner && ended && (
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
      {call.retryAllowed && (
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
      {(terminal || closedWithoutDisposition) && (
        <>
          {terminal && (
            <span className="text-xs text-muted-foreground">
              Disposition saved · recording {call.recording?.state.toLowerCase() ?? "not reported"}
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
      { outcome: "FOLLOW_UP_REQUIRED", label: "Create task" },
    ]
  }
  if (call.entryPoint === "TASK") {
    return [
      { outcome: "COMPLETE_TASK", label: "Complete task" },
      { outcome: "KEEP_OPEN", label: "Keep open" },
    ]
  }
  if (call.connectedAt) {
    return [
      { outcome: "RESOLVED", label: "Resolved" },
      { outcome: "CREATE_TASK", label: "Create task" },
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

function acceptanceMessage(status: string) {
  if (status === "ALREADY_CLAIMED") return "Another available User claimed this Call."
  if (status === "EXPIRED") return "The offer expired before it could be claimed."
  return "Your current Access or technical readiness no longer permits this Call."
}

function callStateLabel(state: CallingCall["state"]) {
  switch (state) {
    case "PREPARING":
      return "Preparing"
    case "RINGING":
      return "Ringing"
    case "CONNECTING":
      return "Connecting"
    case "RECONCILING":
      return "Confirming provider state"
    case "CONNECTED":
      return "Connected"
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
