"use client"

import {
  createContext,
  type ReactNode,
  useContext,
  useEffect,
  useRef,
  useState,
  useSyncExternalStore,
} from "react"
import { ShieldAlertIcon } from "lucide-react"

import { CallingCard } from "@/components/workspace/calling-card"
import { Spinner } from "@/components/ui/spinner"
import { Switch } from "@/components/ui/switch"
import type {
  CallingCall,
  CallingDispositionResult,
} from "@/lib/api/generated/types.gen"
import {
  createCallingMediaAdapter,
  type CallingMediaAdapter,
} from "@/lib/calling/media-adapter"
import { createSoftphoneHTTPAdapter } from "@/lib/calling/softphone-http-adapter"
import {
  createSoftphoneRuntime,
  type RuntimeMediaCorrelation,
  type RuntimeOffer,
  type SoftphoneAttention,
  type SoftphoneMicrophone,
  type SoftphoneRuntime,
} from "@/lib/calling/softphone-runtime"

type CallingDockProps = {
  children: (
    activeCall: CallingCall | undefined,
    callingOccupied: boolean,
  ) => ReactNode
  callingEnabled: boolean
  practiceID: string
  taskCallRequest?: { id: string; taskID: string }
  onTaskCallHandled: (requestID: string, error?: string) => void
  onCallConnected: (call: CallingCall) => void
  onDisposition: (result: CallingDispositionResult) => void
}

type CallingNavigationContext = {
  activeCall: CallingCall | undefined
  callingOccupied: boolean
  availabilityError: string
  availabilityPending: boolean
  available: boolean
  ownsSoftphone: boolean
  outboundPending: boolean
  callingEnabled: boolean
  setAvailability: (available: boolean) => void
  startOutbound: (
    locationID: string,
    destination: string,
  ) => Promise<string | undefined>
}

const CallingNavigationContext = createContext<CallingNavigationContext | null>(
  null,
)
const sessionStorageKey = "acuity.callingSession"
const availabilityIntentStorageKey = "acuity.callingAvailabilityIntent"
const mediaCorrelationStorageKey = "acuity.callingMediaCorrelation"
const remoteAudioElementID = "acuity-calling-remote-audio"

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
      {availabilityPending ? (
        <Spinner aria-label="Updating availability" />
      ) : availabilityError ? (
        <ShieldAlertIcon
          aria-label={availabilityError}
          className="text-destructive"
        />
      ) : null}
      <Switch
        aria-label="Availability"
        className="data-checked:bg-success"
        size="sm"
        checked={available}
        disabled={availabilityPending || (Boolean(activeCall) && ownsSoftphone)}
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
  onCallConnected,
  onDisposition,
}: CallingDockProps) {
  const [runtime] = useState(createBrowserSoftphoneRuntime)
  const snapshot = useSyncExternalStore(
    runtime.subscribe,
    runtime.getSnapshot,
    runtime.getSnapshot,
  )
  const handledTaskCallRef = useRef("")

  useEffect(() => {
    if (!callingEnabled) {
      void runtime.stop()
      return
    }
    void runtime.start()
    return () => {
      void runtime.stop()
    }
  }, [callingEnabled, runtime])

  useEffect(() => {
    if (snapshot.activeCall?.state === "CONNECTED") {
      onCallConnected(snapshot.activeCall)
    }
  }, [onCallConnected, snapshot.activeCall])

  useEffect(() => {
    if (
      !callingEnabled ||
      !taskCallRequest ||
      handledTaskCallRef.current === taskCallRequest.id
    ) {
      return
    }
    handledTaskCallRef.current = taskCallRequest.id
    void runtime
      .startOutbound({
        idempotencyKey: taskCallRequest.id,
        taskId: taskCallRequest.taskID,
      })
      .then(() => {
        onTaskCallHandled(
          taskCallRequest.id,
          runtime.getSnapshot().failure?.message,
        )
      })
  }, [callingEnabled, onTaskCallHandled, runtime, taskCallRequest])

  async function startNumberCall(locationID: string, destination: string) {
    await runtime.startOutbound({
      idempotencyKey: window.crypto.randomUUID(),
      practiceId: practiceID,
      locationId: locationID,
      destination,
    })
    return runtime.getSnapshot().failure?.message
  }

  async function dispose(
    outcome: Parameters<SoftphoneRuntime["dispose"]>[0],
  ) {
    const result = await runtime.dispose(outcome)
    if (result) onDisposition(result)
  }

  return (
    <CallingNavigationContext.Provider
      value={{
        activeCall: snapshot.activeCall,
        callingOccupied: snapshot.occupied,
        availabilityError: snapshot.failure?.message ?? "",
        availabilityPending: snapshot.pending.availability,
        available: snapshot.lease?.available ?? false,
        ownsSoftphone: snapshot.lease?.owner ?? false,
        outboundPending: snapshot.pending.outbound || snapshot.pending.retry,
        callingEnabled,
        setAvailability: (available) => void runtime.setAvailability(available),
        startOutbound: startNumberCall,
      }}
    >
      {children(snapshot.activeCall, snapshot.occupied)}
      <audio id={remoteAudioElementID} autoPlay className="hidden" />
      {(callingEnabled || snapshot.lease?.owner) && (
        <div className="fixed inset-x-3 bottom-3 z-40 md:left-auto md:right-4 md:w-[26rem]">
          <CallingCard
            snapshot={snapshot}
            onAnswer={(callLegID) => void runtime.answer(callLegID)}
            onEnd={() => void runtime.hangup()}
            onMute={runtime.toggleMute}
            onDTMF={(digit) => void runtime.sendDTMF(digit)}
            onDisposition={(outcome) => void dispose(outcome)}
            onRetry={() => void runtime.retry(window.crypto.randomUUID())}
            onRecover={() => void runtime.recover()}
            onClose={runtime.dismissOutcome}
          />
        </div>
      )}
    </CallingNavigationContext.Provider>
  )
}

function createBrowserSoftphoneRuntime() {
  return createSoftphoneRuntime({
    sessionID: browserSessionID(),
    backend: createSoftphoneHTTPAdapter(),
    media: browserMediaAdapter(),
    microphone: browserMicrophone(),
    attention: browserAttention(),
    remoteElementID: remoteAudioElementID,
    availabilityIntent: availabilityIntent(),
    persistAvailabilityIntent: (available) => {
      if (typeof window !== "undefined") {
        window.sessionStorage.setItem(
          availabilityIntentStorageKey,
          String(available),
        )
      }
    },
    loadMediaCorrelation: readMediaCorrelation,
    persistMediaCorrelation: (correlation) => {
      if (typeof window === "undefined") return
      if (!correlation) {
        window.sessionStorage.removeItem(mediaCorrelationStorageKey)
        return
      }
      window.sessionStorage.setItem(
        mediaCorrelationStorageKey,
        JSON.stringify(correlation),
      )
    },
  })
}

function browserAttention(): SoftphoneAttention | undefined {
  if (typeof window === "undefined") return undefined
  return {
    startRingtone,
    showIncomingNotification(offer: RuntimeOffer) {
      if (!("Notification" in window) || Notification.permission !== "granted") {
        return () => undefined
      }
      const notification = new Notification("Incoming Acuity transfer", {
        body: `${offer.locationName} · answer in Acuity`,
        tag: offer.callLegId,
      })
      return () => notification.close()
    },
  }
}

function browserMediaAdapter(): CallingMediaAdapter {
  if (typeof window !== "undefined") return createCallingMediaAdapter()
  return {
    connect: async () => undefined,
    disconnect: async () => undefined,
  }
}

function browserMicrophone(): SoftphoneMicrophone {
  return {
    async start(onUnavailable) {
      const mediaDevices = navigator.mediaDevices
      if (!mediaDevices?.getUserMedia) {
        throw new Error("browser microphone is unavailable")
      }
      const stream = await mediaDevices.getUserMedia({ audio: true })
      const microphone = stream.getAudioTracks()[0]
      if (!microphone || microphone.readyState !== "live") {
        stream.getTracks().forEach((track) => track.stop())
        throw new Error("browser microphone is unavailable")
      }
      let unavailable = false
      const reportUnavailable = () => {
        if (unavailable) return
        unavailable = true
        onUnavailable()
      }
      microphone.addEventListener("ended", reportUnavailable, { once: true })
      const verifyDevices = async () => {
        const devices = await mediaDevices.enumerateDevices().catch(() => [])
        if (!devices.some((device) => device.kind === "audioinput")) {
          reportUnavailable()
        }
      }
      mediaDevices.addEventListener?.("devicechange", verifyDevices)
      try {
        const audioContext = new AudioContext()
        await audioContext.resume()
        await audioContext.close()
      } catch (error) {
        mediaDevices.removeEventListener?.("devicechange", verifyDevices)
        stream.getTracks().forEach((track) => track.stop())
        throw error
      }
      return {
        stop() {
          mediaDevices.removeEventListener?.("devicechange", verifyDevices)
          stream.getTracks().forEach((track) => track.stop())
        },
      }
    },
  }
}

function availabilityIntent() {
  if (typeof window === "undefined") return true
  const stored = window.sessionStorage.getItem(availabilityIntentStorageKey)
  return stored === null ? true : stored === "true"
}

function readMediaCorrelation(): RuntimeMediaCorrelation | undefined {
  if (typeof window === "undefined") return
  const stored = window.sessionStorage.getItem(mediaCorrelationStorageKey)
  if (!stored) return
  try {
    const value = JSON.parse(stored) as Partial<RuntimeMediaCorrelation>
    if (
      typeof value.callID !== "string" ||
      typeof value.providerLegID !== "string" ||
      typeof value.mediaToken !== "string" ||
      (value.callLegID !== undefined && typeof value.callLegID !== "string")
    ) {
      return
    }
    return {
      callID: value.callID,
      ...(value.callLegID ? { callLegID: value.callLegID } : {}),
      providerLegID: value.providerLegID,
      mediaToken: value.mediaToken,
    }
  } catch {
    return
  }
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
    timer = window.setInterval(pulse, 1_200)
  } catch {
    return () => undefined
  }
  return () => {
    window.clearInterval(timer)
    void context?.close()
  }
}
