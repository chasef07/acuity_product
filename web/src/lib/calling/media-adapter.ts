export type MediaState =
  | "registering"
  | "reconnecting"
  | "ready"
  | "unavailable"

export type MediaFailure = "authentication" | "network" | "provider"
export type MediaAttachmentOutcome = "attached" | "ended"

export type IncomingMediaLeg = {
  providerLegID: string
  mediaToken: string
  recovery: boolean
  answer: () => Promise<MediaAttachmentOutcome>
  reject: () => Promise<void>
  mute: () => void
  unmute: () => void
  sendDTMF: (digit: string) => boolean
}

type CallingMediaCallbacks = {
  onState: (state: MediaState) => void
  onIncoming: (leg: IncomingMediaLeg) => void
  onEnded?: (
    leg: Pick<IncomingMediaLeg, "providerLegID" | "mediaToken">,
  ) => void
  onAudioIssue?: () => void
  onFailure?: (failure: MediaFailure) => void
  refreshToken?: () => Promise<string | undefined>
}

export function classifyTelnyxError(value: unknown, online = navigator.onLine) {
  if (!online) return "network" as const
  const error = value as { code?: number | string; message?: string }
  const description = `${error?.code ?? ""} ${error?.message ?? ""}`.toLowerCase()
  if (/\b(401|403)\b|auth|credential|login|token/.test(description)) {
    return "authentication" as const
  }
  return "provider" as const
}

export interface CallingMediaAdapter {
  connect(
    token: string,
    remoteElement: string,
    callbacks: CallingMediaCallbacks,
    signal?: AbortSignal,
  ): Promise<void>
  disconnect(signal?: AbortSignal): Promise<void>
}

declare global {
  interface Window {
    __acuityCallingMediaFactory?: () => CallingMediaAdapter
  }
}

type SDKCall = {
  state: string
  remoteStream?: MediaStream
  peer?: {
    instance?: RTCPeerConnection | null
  }
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
  login: (options: { creds: { login_token: string } }) => Promise<void>
  on: (event: string, callback: (value?: unknown) => void) => SDKClient
}

type SDKClientFactory = (token: string) => Promise<SDKClient>

function abortableMediaOperation<T>(
  operation: Promise<T>,
  signal?: AbortSignal,
  onLateValue?: (value: T) => void,
) {
  if (!signal) return operation
  return new Promise<T>((resolve, reject) => {
    let settled = false
    const cleanup = () => signal.removeEventListener("abort", onAbort)
    const onAbort = () => {
      if (settled) return
      settled = true
      cleanup()
      reject(new DOMException("Calling media operation aborted", "AbortError"))
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
          onLateValue?.(value)
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
    maxReconnectAttempts: 0,
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

type MediaSession = {
  call: SDKCall
  remoteStream?: MediaStream
  providerLegID: string
  mediaToken: string
  desiredMuted: boolean
  attachmentCurrent: boolean
}

type MediaIdentity = Pick<
  IncomingMediaLeg,
  "providerLegID" | "mediaToken"
>

function matchesMediaSession(
  session: MediaSession | undefined,
  providerLegID: string,
  mediaToken: string,
): session is MediaSession {
  return (
    session?.providerLegID === providerLegID &&
    session.mediaToken === mediaToken
  )
}

class TelnyxMediaAdapter implements CallingMediaAdapter {
  private client?: SDKClient
  private activeSession?: MediaSession
  private output?: HTMLMediaElement
  private quarantine?: HTMLAudioElement
  private readonly createClient: SDKClientFactory
  private tokenRefresh?: Promise<void>
  private readonly mediaIdentity = new WeakMap<SDKCall, MediaIdentity>()
  private readonly terminalCalls = new WeakSet<SDKCall>()

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
    signal?: AbortSignal,
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
    let client: SDKClient | undefined
    try {
      client = await abortableMediaOperation(
        this.createClient(token),
        signal,
        (lateClient) => void lateClient.serverDisconnect().catch(() => undefined),
      )
    } catch (error) {
      if (this.quarantine === quarantine) {
        quarantine.remove()
        this.quarantine = undefined
      }
      throw error
    }
    client.remoteElement = quarantine
    this.client = client
    const connectionCurrent = () =>
      this.client === client && signal?.aborted !== true
    client.on("telnyx.socket.close", () => {
      if (!connectionCurrent()) return
      const session = this.activeSession
      if (session) {
        session.attachmentCurrent = false
        applyMicrophoneFence(session.call, false, session.desiredMuted)
      }
      callbacks.onState("reconnecting")
    })
    client.on("telnyx.ready", () => {
      if (connectionCurrent()) callbacks.onState("ready")
    })
    client.on("telnyx.warning", (value) => {
      if (!connectionCurrent()) return
      const warning = value as { warning?: { code?: number } }
      if (
        warning.warning?.code === 32001 &&
        this.activeSession?.attachmentCurrent
      ) {
        callbacks.onAudioIssue?.()
      }
      if (warning.warning?.code !== 34001 || !callbacks.refreshToken) return
      if (!this.tokenRefresh) {
        this.tokenRefresh = callbacks
          .refreshToken()
          .then(async (token) => {
            if (!token || this.client !== client) return
            await client.login({ creds: { login_token: token } })
          })
          .finally(() => {
            this.tokenRefresh = undefined
          })
      }
    })
    client.on("telnyx.error", (value) => {
      if (!connectionCurrent()) return
      const session = this.activeSession
      if (session) {
        session.attachmentCurrent = false
        applyMicrophoneFence(session.call, false, session.desiredMuted)
      }
      callbacks.onFailure?.(classifyTelnyxError(value))
      callbacks.onState("unavailable")
    })
    client.on("telnyx.notification", (value) => {
      if (!connectionCurrent()) return
      const notification = value as SDKNotification
      const call = notification.call
      if (notification.type !== "callUpdate" || !call) {
        return
      }
      if (["hangup", "destroy", "purge"].includes(call.state)) {
        const firstTerminalUpdate = !this.terminalCalls.has(call)
        this.terminalCalls.add(call)
        const session = this.activeSession
        if (session?.call === call) {
          this.activeSession = undefined
          const output = this.output
          if (
            output &&
            session.remoteStream &&
            output.srcObject === session.remoteStream
          ) {
            output.srcObject = null
          }
        }
        const identity = this.mediaIdentity.get(call)
        if (firstTerminalUpdate && identity) callbacks.onEnded?.(identity)
        return
      }
      if (this.terminalCalls.has(call)) return
      if (!["ringing", "active", "recovering"].includes(call.state)) return
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
      this.mediaIdentity.set(call, { providerLegID, mediaToken })
      callbacks.onIncoming({
        providerLegID,
        mediaToken,
        recovery: call.state !== "ringing",
        answer: async () => {
          if (this.terminalCalls.has(call)) return "ended"
          const current = this.activeSession
          const recoversActiveLeg = matchesMediaSession(
            current,
            providerLegID,
            mediaToken,
          )
          const desiredMuted = recoversActiveLeg
            ? current.desiredMuted
            : false
          const session: MediaSession = {
            call,
            providerLegID,
            mediaToken,
            desiredMuted,
            attachmentCurrent: false,
          }
          this.activeSession = session
          try {
            if (call.state === "ringing") {
              await call.answer({ remoteElement: quarantine.id })
            }
            if (this.activeSession !== session) return "ended"
            const remoteStream = call.remoteStream
            if (!remoteStream) {
              throw new Error("browser audio stream is unavailable")
            }
            session.remoteStream = remoteStream
            output.srcObject = remoteStream
            await output.play()
            if (this.activeSession !== session) return "ended"
            try {
              await waitForSecureMedia(call)
            } catch (error) {
              if (this.activeSession !== session) return "ended"
              throw error
            }
            if (this.activeSession !== session) return "ended"
            applyMicrophoneFence(call, true, desiredMuted)
            session.attachmentCurrent = true
            return "attached"
          } catch (error) {
            if (this.activeSession !== session) return "ended"
            this.activeSession = undefined
            if (output.srcObject === session.remoteStream) {
              output.srcObject = null
            }
            throw error
          }
        },
        reject: () => rejectMediaCall(call),
        mute: () => {
          const current = this.activeSession
          if (matchesMediaSession(current, providerLegID, mediaToken)) {
            current.desiredMuted = true
          }
          call.muteAudio()
        },
        unmute: () => {
          const current = this.activeSession
          if (matchesMediaSession(current, providerLegID, mediaToken)) {
            current.desiredMuted = false
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
    try {
      await abortableMediaOperation(client.connect(), signal, () => {
        void client?.serverDisconnect().catch(() => undefined)
      })
    } catch (error) {
      if (this.client === client) this.client = undefined
      if (this.quarantine === quarantine) {
        quarantine.remove()
        this.quarantine = undefined
      }
      await client.serverDisconnect().catch(() => undefined)
      throw error
    }
  }

  async disconnect(signal?: AbortSignal) {
    const client = this.client
    this.client = undefined
    this.tokenRefresh = undefined
    this.activeSession = undefined
    if (this.output) this.output.srcObject = null
    this.output = undefined
    this.quarantine?.remove()
    this.quarantine = undefined
    // TelnyxRTC.disconnect() sends BYE for every active Call. serverDisconnect
    // purges local state without BYE and disables reconnect, so server Call
    // Control remains the sole termination owner.
    if (client) {
      await abortableMediaOperation(client.serverDisconnect(), signal)
    }
  }
}

function waitForSecureMedia(call: SDKCall) {
  const peer = call.peer?.instance
  if (!peer) {
    return Promise.reject(
      new Error("browser secure media transport is unavailable"),
    )
  }
  if (secureMediaConnected(peer)) return Promise.resolve()

  return new Promise<void>((resolve, reject) => {
    const timeout = setTimeout(
      () => finish(new Error("browser secure media transport did not connect")),
      10_000,
    )
    const events = [
      "connectionstatechange",
      "iceconnectionstatechange",
      "signalingstatechange",
    ] as const

    function check() {
      if (secureMediaConnected(peer!)) {
        finish()
        return
      }
      if (
        peer!.connectionState === "failed" ||
        peer!.connectionState === "closed" ||
        peer!.iceConnectionState === "failed" ||
        peer!.iceConnectionState === "closed" ||
        peer!.signalingState === "closed"
      ) {
        finish(new Error("browser secure media transport failed"))
      }
    }

    function finish(error?: Error) {
      clearTimeout(timeout)
      for (const event of events) peer!.removeEventListener(event, check)
      if (error) reject(error)
      else resolve()
    }

    for (const event of events) peer.addEventListener(event, check)
    check()
  })
}

function secureMediaConnected(peer: RTCPeerConnection) {
  return (
    peer.connectionState === "connected" &&
    (peer.iceConnectionState === "connected" ||
      peer.iceConnectionState === "completed") &&
    peer.signalingState !== "closed"
  )
}
