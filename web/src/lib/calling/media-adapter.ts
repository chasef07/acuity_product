export type MediaState =
  | "registering"
  | "reconnecting"
  | "ready"
  | "unavailable"

export type IncomingMediaLeg = {
  providerLegID: string
  mediaToken: string
  recovery: boolean
  answer: () => Promise<void>
  reject: () => Promise<void>
  mute: () => void
  unmute: () => void
}

type CallingMediaCallbacks = {
  onState: (state: MediaState) => void
  onIncoming: (leg: IncomingMediaLeg) => void
}

export interface CallingMediaAdapter {
  connect(
    token: string,
    remoteElement: string,
    callbacks: CallingMediaCallbacks,
  ): Promise<void>
  disconnect(): Promise<void>
}

declare global {
  interface Window {
    __acuityCallingMediaFactory?: () => CallingMediaAdapter
  }
}

type SDKCall = {
  state: string
  remoteStream?: MediaStream
  options: {
    telnyxLegId?: string
    customHeaders?: Array<{ name: string; value: string }>
  }
  answer: (options?: { remoteElement?: string }) => Promise<void>
  hangup: (
    options?: { initiator?: string },
    execute?: boolean,
  ) => Promise<void>
  muteAudio: () => void
  unmuteAudio: () => void
}

type SDKNotification = {
  type: string
  call?: SDKCall
}

type SDKClient = {
  remoteElement: string | HTMLMediaElement
  connect: () => Promise<void>
  serverDisconnect: () => Promise<void>
  on: (event: string, callback: (value?: unknown) => void) => SDKClient
}

export function createCallingMediaAdapter(): CallingMediaAdapter {
  return window.__acuityCallingMediaFactory?.() ?? new TelnyxMediaAdapter()
}

function mediaTokenFromHeaders(
  headers?: Array<{ name: string; value: string }>,
) {
  const matches = headers?.filter(
    (header) => header.name.toLowerCase() === "x-acuity-media-token",
  )
  if (matches?.length !== 1) return
  const token = matches[0].value.trim()
  if (!/^[A-Za-z0-9_-]{43}$/.test(token)) return
  return token
}

type RejectableMediaCall = Pick<SDKCall, "state" | "hangup">

export function rejectMediaCall(
  call: RejectableMediaCall,
  initiator = "acuity:media-fence",
) {
  return call.hangup({ initiator }, call.state === "ringing")
}

export function callingClientOptions(token: string) {
  return {
    login_token: token,
    hangupOnBeforeUnload: false,
    mutedMicOnStart: true,
  }
}

type MicrophoneMediaCall = Pick<SDKCall, "muteAudio" | "unmuteAudio">

export function applyMicrophoneFence(
  call: MicrophoneMediaCall,
  authorized: boolean,
  desiredMuted: boolean,
) {
  if (!authorized || desiredMuted) call.muteAudio()
  else call.unmuteAudio()
}

class TelnyxMediaAdapter implements CallingMediaAdapter {
  private client?: SDKClient
  private activeCall?: SDKCall
  private desiredMuted = false
  private output?: HTMLMediaElement
  private quarantine?: HTMLAudioElement

  async connect(
    token: string,
    remoteElement: string,
    callbacks: CallingMediaCallbacks,
  ) {
    callbacks.onState("registering")
    const output = document.getElementById(remoteElement)
    if (!(output instanceof HTMLMediaElement)) {
      throw new Error("browser audio output is unavailable")
    }
    const previousQuarantine = document.getElementById(
      "acuity-calling-quarantine-audio",
    )
    previousQuarantine?.remove()
    const quarantine = document.createElement("audio")
    quarantine.id = "acuity-calling-quarantine-audio"
    quarantine.autoplay = true
    quarantine.muted = true
    quarantine.volume = 0
    quarantine.className = "hidden"
    document.body.append(quarantine)
    this.output = output
    this.quarantine = quarantine
    const { TelnyxRTC } = await import("@telnyx/webrtc")
    const client = new TelnyxRTC(callingClientOptions(token)) as SDKClient
    client.remoteElement = quarantine
    this.client = client
    client.on("telnyx.socket.close", () => {
      if (this.activeCall) {
        applyMicrophoneFence(this.activeCall, false, this.desiredMuted)
      }
      callbacks.onState("reconnecting")
    })
    client.on("telnyx.ready", () => callbacks.onState("ready"))
    client.on("telnyx.error", () => {
      if (this.activeCall) {
        applyMicrophoneFence(this.activeCall, false, this.desiredMuted)
      }
      callbacks.onState("unavailable")
    })
    client.on("telnyx.notification", (value) => {
      const notification = value as SDKNotification
      const call = notification.call
      if (
        notification.type !== "callUpdate" ||
        !call ||
        !["ringing", "active", "recovering"].includes(call.state)
      ) {
        return
      }
      if (call === this.activeCall) return
      const providerLegID = call.options.telnyxLegId
      const mediaToken = mediaTokenFromHeaders(call.options.customHeaders)
      if (!providerLegID || !mediaToken) {
        void rejectMediaCall(call, "acuity:invalid-media-invite").catch(
          () => undefined,
        )
        return
      }
      callbacks.onIncoming({
        providerLegID,
        mediaToken,
        recovery: call.state !== "ringing",
        answer: async () => {
          this.activeCall = call
          if (call.state === "ringing") {
            await call.answer({ remoteElement: quarantine.id })
          }
          if (!call.remoteStream) {
            throw new Error("browser audio stream is unavailable")
          }
          output.srcObject = call.remoteStream
          await output.play()
          applyMicrophoneFence(call, true, this.desiredMuted)
        },
        reject: () => rejectMediaCall(call),
        mute: () => {
          this.desiredMuted = true
          call.muteAudio()
        },
        unmute: () => {
          this.desiredMuted = false
          call.unmuteAudio()
        },
      })
    })
    await client.connect()
  }

  async disconnect() {
    const client = this.client
    this.client = undefined
    this.activeCall = undefined
    this.desiredMuted = false
    if (this.output) this.output.srcObject = null
    this.output = undefined
    this.quarantine?.remove()
    this.quarantine = undefined
    // TelnyxRTC.disconnect() sends BYE for every active Call. serverDisconnect
    // purges local state without BYE and disables reconnect, so server Call
    // Control remains the sole termination owner.
    if (client) await client.serverDisconnect()
  }
}
