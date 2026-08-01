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
  sendDTMF: (digit: string) => boolean
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
  telnyxIDs: {
    telnyxLegId?: string
    telnyxSessionId?: string
  }
  options: {
    customHeaders?: Array<{ name: string; value: string }>
  }
  answer: (options?: { remoteElement?: string }) => Promise<void>
  hangup: (
    options?: { initiator?: string },
    execute?: boolean,
  ) => Promise<void>
  muteAudio: () => void
  unmuteAudio: () => void
  dtmf: (digit: string) => void
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

type SDKClientFactory = (token: string) => Promise<SDKClient>

export function createCallingMediaAdapter(
  createClient?: SDKClientFactory,
): CallingMediaAdapter {
  return (
    window.__acuityCallingMediaFactory?.() ??
    new TelnyxMediaAdapter(createClient)
  )
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

type ActiveMediaSession = {
  call: SDKCall
  providerLegID: string
  mediaToken: string
  desiredMuted: boolean
  attachmentCurrent: boolean
}

function matchesMediaSession(
  session: ActiveMediaSession | undefined,
  providerLegID: string,
  mediaToken: string,
): session is ActiveMediaSession {
  return (
    session?.providerLegID === providerLegID &&
    session.mediaToken === mediaToken
  )
}

class TelnyxMediaAdapter implements CallingMediaAdapter {
  private client?: SDKClient
  private activeSession?: ActiveMediaSession
  private output?: HTMLMediaElement
  private quarantine?: HTMLAudioElement
  private readonly createClient: SDKClientFactory

  constructor(
    createClient: SDKClientFactory = async (token) => {
      const { TelnyxRTC } = await import("@telnyx/webrtc")
      return new TelnyxRTC(callingClientOptions(token)) as SDKClient
    },
  ) {
    this.createClient = createClient
  }

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
    const client = await this.createClient(token)
    client.remoteElement = quarantine
    this.client = client
    client.on("telnyx.socket.close", () => {
      const session = this.activeSession
      if (session) {
        this.activeSession = { ...session, attachmentCurrent: false }
        applyMicrophoneFence(session.call, false, session.desiredMuted)
      }
      callbacks.onState("reconnecting")
    })
    client.on("telnyx.ready", () => callbacks.onState("ready"))
    client.on("telnyx.error", () => {
      const session = this.activeSession
      if (session) {
        this.activeSession = { ...session, attachmentCurrent: false }
        applyMicrophoneFence(session.call, false, session.desiredMuted)
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
      if (
        call === this.activeSession?.call &&
        this.activeSession.attachmentCurrent &&
        call.state !== "recovering"
      ) {
        return
      }
      const providerLegID = call.telnyxIDs.telnyxLegId
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
          const current = this.activeSession
          const recoversActiveLeg = matchesMediaSession(
            current,
            providerLegID,
            mediaToken,
          )
          const desiredMuted = recoversActiveLeg
            ? current.desiredMuted
            : false
          if (call.state === "ringing") {
            await call.answer({ remoteElement: quarantine.id })
          }
          if (!call.remoteStream) {
            throw new Error("browser audio stream is unavailable")
          }
          output.srcObject = call.remoteStream
          await output.play()
          applyMicrophoneFence(call, true, desiredMuted)
          this.activeSession = {
            call,
            providerLegID,
            mediaToken,
            desiredMuted,
            attachmentCurrent: true,
          }
        },
        reject: () => rejectMediaCall(call),
        mute: () => {
          const current = this.activeSession
          if (matchesMediaSession(current, providerLegID, mediaToken)) {
            this.activeSession = { ...current, desiredMuted: true }
          }
          call.muteAudio()
        },
        unmute: () => {
          const current = this.activeSession
          if (matchesMediaSession(current, providerLegID, mediaToken)) {
            this.activeSession = { ...current, desiredMuted: false }
          }
          call.unmuteAudio()
        },
        sendDTMF: (digit) => {
          const current = this.activeSession
          if (
            !/^[0-9A-D*#]$/.test(digit) ||
            call.state !== "active" ||
            !current?.attachmentCurrent ||
            !matchesMediaSession(current, providerLegID, mediaToken)
          ) {
            return false
          }
          call.dtmf(digit)
          return true
        },
      })
    })
    await client.connect()
  }

  async disconnect() {
    const client = this.client
    this.client = undefined
    this.activeSession = undefined
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
