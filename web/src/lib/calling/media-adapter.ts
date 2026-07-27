export type MediaState =
  | "registering"
  | "reconnecting"
  | "ready"
  | "unavailable"

export type IncomingMediaLeg = {
  callID: string
  providerLegID: string
  answer: () => Promise<void>
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
  options: { clientState?: string; telnyxLegId?: string }
  answer: (options?: { remoteElement?: string }) => Promise<void>
  muteAudio: () => void
  unmuteAudio: () => void
}

type SDKNotification = {
  type: string
  call?: SDKCall
}

type SDKClient = {
  remoteElement: string
  connect: () => Promise<void>
  serverDisconnect: () => Promise<void>
  on: (event: string, callback: (value?: unknown) => void) => SDKClient
}

export function createCallingMediaAdapter(): CallingMediaAdapter {
  return window.__acuityCallingMediaFactory?.() ?? new TelnyxMediaAdapter()
}

function correlatedCallID(clientState?: string): string | undefined {
  if (!clientState) return
  try {
    const decoded = JSON.parse(window.atob(clientState)) as {
      v?: number
      call?: string
      leg?: string
    }
    if (decoded.v !== 1 || decoded.leg !== "staff" || !decoded.call) return
    return decoded.call
  } catch {
    return
  }
}

class TelnyxMediaAdapter implements CallingMediaAdapter {
  private client?: SDKClient

  async connect(
    token: string,
    remoteElement: string,
    callbacks: CallingMediaCallbacks,
  ) {
    callbacks.onState("registering")
    const { TelnyxRTC } = await import("@telnyx/webrtc")
    const client = new TelnyxRTC({
      login_token: token,
      hangupOnBeforeUnload: false,
    }) as SDKClient
    client.remoteElement = remoteElement
    this.client = client
    client.on("telnyx.socket.close", () => callbacks.onState("reconnecting"))
    client.on("telnyx.ready", () => callbacks.onState("ready"))
    client.on("telnyx.error", () => callbacks.onState("unavailable"))
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
      const callID = correlatedCallID(call.options.clientState)
      const providerLegID = call.options.telnyxLegId
      if (!callID || !providerLegID) return
      callbacks.onIncoming({
        callID,
        providerLegID,
        answer:
          call.state === "ringing"
            ? () => call.answer({ remoteElement })
            : async () => undefined,
        mute: () => call.muteAudio(),
        unmute: () => call.unmuteAudio(),
      })
    })
    await client.connect()
  }

  async disconnect() {
    const client = this.client
    this.client = undefined
    // TelnyxRTC.disconnect() sends BYE for every active Call. serverDisconnect
    // purges local state without BYE and disables reconnect, so server Call
    // Control remains the sole termination owner.
    if (client) await client.serverDisconnect()
  }
}
