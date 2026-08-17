import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import test from "node:test"

import {
  applyMicrophoneFence,
  callingClientOptions,
	classifyTelnyxError,
  createCallingMediaAdapter,
  type IncomingMediaLeg,
  rejectMediaCall,
} from "./media-adapter.ts"

test("Telnyx errors distinguish authentication network and provider failures", () => {
	assert.equal(classifyTelnyxError({ code: 401 }, true), "authentication")
	assert.equal(classifyTelnyxError(new Error("socket closed"), false), "network")
	assert.equal(classifyTelnyxError(new Error("unknown"), true), "provider")
})

class FakeAudioElement {
  id = ""
  autoplay = false
  muted = false
  volume = 1
  className = ""
  srcObject: unknown = null
  plays = 0
  playImplementation?: () => Promise<void>

  async play() {
    this.plays += 1
    await this.playImplementation?.()
  }

  remove() {}
}

class FakePeerConnection {
  private readonly listeners = new Map<string, Set<() => void>>()
  connectionState: RTCPeerConnectionState
  iceConnectionState: RTCIceConnectionState
  signalingState: RTCSignalingState

  constructor(
    connectionState: RTCPeerConnectionState = "connected",
    iceConnectionState: RTCIceConnectionState = "connected",
    signalingState: RTCSignalingState = "stable",
  ) {
    this.connectionState = connectionState
    this.iceConnectionState = iceConnectionState
    this.signalingState = signalingState
  }

  addEventListener(event: string, listener: () => void) {
    const listeners = this.listeners.get(event) ?? new Set()
    listeners.add(listener)
    this.listeners.set(event, listeners)
  }

  removeEventListener(event: string, listener: () => void) {
    this.listeners.get(event)?.delete(listener)
  }

  connect() {
    this.connectionState = "connected"
    this.iceConnectionState = "connected"
    for (const listener of this.listeners.get("connectionstatechange") ?? []) {
      listener()
    }
  }
}

function installMediaDOM() {
  const output = new FakeAudioElement()
  output.id = "remote"
  const elements = new Map<string, FakeAudioElement>([[output.id, output]])
  Object.defineProperty(globalThis, "window", {
    configurable: true,
    value: {},
  })
  Object.defineProperty(globalThis, "HTMLMediaElement", {
    configurable: true,
    value: FakeAudioElement,
  })
  Object.defineProperty(globalThis, "document", {
    configurable: true,
    value: {
      body: {
        append: (element: FakeAudioElement) => {
          elements.set(element.id, element)
        },
      },
      createElement: () => new FakeAudioElement(),
      getElementById: (id: string) => elements.get(id),
    },
  })
  return output
}

function fakeClient() {
  const listeners = new Map<string, (value?: unknown) => void>()
  const logins: string[] = []
  const client = {
    remoteElement: "",
    connect: async () => {},
    serverDisconnect: async () => {},
    login: async ({ creds }: { creds: { login_token: string } }) => {
      logins.push(creds.login_token)
    },
    on: (event: string, callback: (value?: unknown) => void) => {
      listeners.set(event, callback)
      return client
    },
  }
  return {
    client,
    logins,
    emit: (event: string, value?: unknown) => listeners.get(event)?.(value),
  }
}

function fakeCall(
  providerLegID: string,
  mediaToken: string,
  actions: string[],
  hasRemoteStream = true,
  peerConnection = new FakePeerConnection(),
) {
  return {
    state: "ringing",
    remoteStream: hasRemoteStream ? {} : undefined,
    telnyxIDs: {
      telnyxLegId: providerLegID,
    },
    options: {
      customHeaders: [{ name: "X-Acuity-Media-Token", value: mediaToken }],
    },
    peer: { instance: peerConnection },
    answer: async () => {
      actions.push("answer")
    },
    hangup: async () => {},
    muteAudio: () => actions.push("mute"),
    unmuteAudio: () => actions.push("unmute"),
    dtmf: (digit: string) => actions.push(`dtmf:${digit}`),
  }
}

test("media attachment waits for secure peer connectivity", async () => {
  const output = installMediaDOM()
  const sdk = fakeClient()
  const legs: IncomingMediaLeg[] = []
  const peer = new FakePeerConnection("connecting", "checking")
  const call = fakeCall("leg-1", "a".repeat(43), [], true, peer)
  const adapter = createCallingMediaAdapter(async () => sdk.client)

  await adapter.connect("jwt", output.id, {
    onState: () => {},
    onIncoming: (leg) => legs.push(leg),
  })
  sdk.emit("telnyx.notification", { type: "callUpdate", call })

  let attached = false
  const attachment = legs[0].answer().then(() => {
    attached = true
  })
  await new Promise((resolve) => setTimeout(resolve, 0))
  assert.equal(attached, false)

  peer.connect()
  await attachment
  assert.equal(attached, true)
})

test("incoming media does not require a shared Call Control session", async () => {
  const output = installMediaDOM()
  const sdk = fakeClient()
  const legs: IncomingMediaLeg[] = []
  const call = fakeCall("webrtc-leg", "a".repeat(43), [])
  const adapter = createCallingMediaAdapter(async () => sdk.client)

  await adapter.connect("jwt", output.id, {
    onState: () => {},
    onIncoming: (leg) => legs.push(leg),
  })
  sdk.emit("telnyx.notification", { type: "callUpdate", call })

  assert.equal(legs[0].providerLegID, "webrtc-leg")
  assert.equal(legs[0].mediaToken, "a".repeat(43))
})

test("Telnyx media starts with the microphone fenced", () => {
  assert.equal(callingClientOptions("token").mutedMicOnStart, true)
  assert.equal(callingClientOptions("token").maxReconnectAttempts, 0)
})

test("pinned Telnyx SDK treats zero reconnect attempts as unlimited", () => {
  const sdkPackage = JSON.parse(
    readFileSync("node_modules/@telnyx/webrtc/package.json", "utf8"),
  ) as { version: string }
  const sdkBundle = readFileSync(
    "node_modules/@telnyx/webrtc/lib/bundle.mjs",
    "utf8",
  )
  assert.equal(sdkPackage.version, "2.27.8")
  assert.match(
    sdkBundle,
    /maxReconnectAttempts[\s\S]{0,300}e>0&&this\._reconnectAttempts>e/,
  )
})

test("warning 34001 refreshes login on the existing client", async () => {
  const output = installMediaDOM()
  const sdk = fakeClient()
  const adapter = createCallingMediaAdapter(async () => sdk.client)
  await adapter.connect("jwt-old", output.id, {
    onState: () => {},
    onIncoming: () => {},
    refreshToken: async () => "jwt-new",
  })

  sdk.emit("telnyx.warning", { warning: { code: 34001 } })
  await new Promise((resolve) => setTimeout(resolve, 0))
  assert.deepEqual(sdk.logins, ["jwt-new"])
})

test("low inbound RTP warnings surface an audio issue", async () => {
  const output = installMediaDOM()
  const sdk = fakeClient()
  const legs: IncomingMediaLeg[] = []
  const issues: string[] = []
  const call = fakeCall("leg-1", "d".repeat(43), [])
  const adapter = createCallingMediaAdapter(async () => sdk.client)
  await adapter.connect("jwt", output.id, {
    onState: () => {},
    onIncoming: (leg) => legs.push(leg),
    onAudioIssue: () => issues.push("low-bytes-received"),
  })

  sdk.emit("telnyx.notification", { type: "callUpdate", call })
  await legs[0].answer()
  sdk.emit("telnyx.warning", { warning: { code: 32001 } })

  assert.deepEqual(issues, ["low-bytes-received"])
})

test("microphone authorization restores only the intended state", () => {
  const actions: string[] = []
  const call = {
    muteAudio: () => actions.push("mute"),
    unmuteAudio: () => actions.push("unmute"),
  }
  applyMicrophoneFence(call, false, false)
  applyMicrophoneFence(call, true, true)
  applyMicrophoneFence(call, true, false)
  assert.deepEqual(actions, ["mute", "mute", "unmute"])
})

for (const state of ["active", "recovering"]) {
  test(`rejecting ${state} media purges locally without provider BYE`, async () => {
    let execute: boolean | undefined
    await rejectMediaCall({
      state,
      hangup: async (_options, nextExecute) => {
        execute = nextExecute
      },
    })
    assert.equal(execute, false)
  })
}

test("rejecting a ringing invite tells the provider to end that invite", async () => {
  let execute: boolean | undefined
  await rejectMediaCall({
    state: "ringing",
    hangup: async (_options, nextExecute) => {
      execute = nextExecute
    },
  })
  assert.equal(execute, true)
})

test("same-instance recovery is reauthorized before audio resumes", async () => {
  const output = installMediaDOM()
  const sdk = fakeClient()
  const legs: IncomingMediaLeg[] = []
  const actions: string[] = []
  const call = fakeCall("leg-1", "a".repeat(43), actions)
  const adapter = createCallingMediaAdapter(async () => sdk.client)

  await adapter.connect("jwt", output.id, {
    onState: () => {},
    onIncoming: (leg) => legs.push(leg),
  })
  sdk.emit("telnyx.notification", { type: "callUpdate", call })
  assert.equal(legs.length, 1)
  await legs[0].answer()

  sdk.emit("telnyx.socket.close")
  call.state = "recovering"
  sdk.emit("telnyx.notification", { type: "callUpdate", call })
  assert.equal(legs.length, 2)
  await legs[1].answer()

  assert.deepEqual(actions, ["answer", "unmute", "mute", "unmute"])
  assert.equal(output.plays, 2)
})

test("a new authoritative Call starts with fresh unmuted intent", async () => {
  const output = installMediaDOM()
  const sdk = fakeClient()
  const legs: IncomingMediaLeg[] = []
  const firstActions: string[] = []
  const secondActions: string[] = []
  const firstCall = fakeCall("leg-1", "a".repeat(43), firstActions)
  const secondCall = fakeCall("leg-2", "b".repeat(43), secondActions)
  const adapter = createCallingMediaAdapter(async () => sdk.client)

  await adapter.connect("jwt", output.id, {
    onState: () => {},
    onIncoming: (leg) => legs.push(leg),
  })
  sdk.emit("telnyx.notification", { type: "callUpdate", call: firstCall })
  await legs[0].answer()
  legs[0].mute()

  sdk.emit("telnyx.notification", { type: "callUpdate", call: secondCall })
  await legs[1].answer()

  assert.deepEqual(secondActions, ["answer", "unmute"])
})

test("a failed attachment remains eligible for a later SDK update", async () => {
  const output = installMediaDOM()
  const sdk = fakeClient()
  const legs: IncomingMediaLeg[] = []
  const actions: string[] = []
  const call = fakeCall("leg-1", "a".repeat(43), actions, false)
  const adapter = createCallingMediaAdapter(async () => sdk.client)

  await adapter.connect("jwt", output.id, {
    onState: () => {},
    onIncoming: (leg) => legs.push(leg),
  })
  sdk.emit("telnyx.notification", { type: "callUpdate", call })
  await assert.rejects(legs[0].answer(), /audio stream is unavailable/)

  call.state = "active"
  call.remoteStream = {}
  sdk.emit("telnyx.notification", { type: "callUpdate", call })
  assert.equal(legs.length, 2)
  await legs[1].answer()

  assert.deepEqual(actions, ["answer", "unmute"])
  assert.equal(output.plays, 1)
})

test("a failed same-instance recovery remains eligible after becoming active", async () => {
  const output = installMediaDOM()
  const sdk = fakeClient()
  const legs: IncomingMediaLeg[] = []
  const actions: string[] = []
  const call = fakeCall("leg-1", "a".repeat(43), actions)
  const adapter = createCallingMediaAdapter(async () => sdk.client)

  await adapter.connect("jwt", output.id, {
    onState: () => {},
    onIncoming: (leg) => legs.push(leg),
  })
  sdk.emit("telnyx.notification", { type: "callUpdate", call })
  await legs[0].answer()

  sdk.emit("telnyx.socket.close")
  call.state = "recovering"
  call.remoteStream = undefined
  sdk.emit("telnyx.notification", { type: "callUpdate", call })
  await assert.rejects(legs[1].answer(), /audio stream is unavailable/)

  call.state = "active"
  call.remoteStream = {}
  sdk.emit("telnyx.notification", { type: "callUpdate", call })
  assert.equal(legs.length, 3)
  await legs[2].answer()

  assert.deepEqual(actions, ["answer", "unmute", "mute", "unmute"])
  assert.equal(output.plays, 2)
})

test("DTMF is sent only through the current healthy attachment", async () => {
  const output = installMediaDOM()
  const sdk = fakeClient()
  const legs: IncomingMediaLeg[] = []
  const actions: string[] = []
  const call = fakeCall("leg-1", "a".repeat(43), actions)
  const adapter = createCallingMediaAdapter(async () => sdk.client)

  await adapter.connect("jwt", output.id, {
    onState: () => {},
    onIncoming: (leg) => legs.push(leg),
  })
  sdk.emit("telnyx.notification", { type: "callUpdate", call })
  await legs[0].answer()
  call.state = "active"

  assert.equal(legs[0].sendDTMF("5"), true)
  assert.equal(legs[0].sendDTMF("12"), false)
  sdk.emit("telnyx.socket.close")
  assert.equal(legs[0].sendDTMF("6"), false)

  assert.deepEqual(actions, ["answer", "unmute", "dtmf:5", "mute"])
})

test("a terminal SDK update detaches the losing media leg", async () => {
  const output = installMediaDOM()
  const sdk = fakeClient()
  const legs: IncomingMediaLeg[] = []
  const actions: string[] = []
  const call = fakeCall("losing-leg", "c".repeat(43), actions)
  const adapter = createCallingMediaAdapter(async () => sdk.client)

  await adapter.connect("jwt", output.id, {
    onState: () => {},
    onIncoming: (leg) => legs.push(leg),
  })
  sdk.emit("telnyx.notification", { type: "callUpdate", call })
  await legs[0].answer()
  assert.notEqual(output.srcObject, null)

  call.state = "destroy"
  sdk.emit("telnyx.notification", { type: "callUpdate", call })

  assert.equal(output.srcObject, null)
  assert.equal(legs[0].sendDTMF("5"), false)
  assert.deepEqual(actions, ["answer", "unmute"])
})

test("a terminal SDK update during answer never attaches the losing media leg", async () => {
  const output = installMediaDOM()
  const sdk = fakeClient()
  const legs: IncomingMediaLeg[] = []
  const ended: Array<{ providerLegID: string; mediaToken: string }> = []
  const actions: string[] = []
  let finishAnswer = () => {}
  let markAnswerStarted = () => {}
  const answerStarted = new Promise<void>((resolve) => {
    markAnswerStarted = resolve
  })
  const answerPending = new Promise<void>((resolve) => {
    finishAnswer = resolve
  })
  const call = fakeCall("losing-leg", "e".repeat(43), actions)
  call.answer = async () => {
    actions.push("answer")
    markAnswerStarted()
    await answerPending
  }
  const adapter = createCallingMediaAdapter(async () => sdk.client)

  await adapter.connect("jwt", output.id, {
    onState: () => {},
    onIncoming: (leg) => legs.push(leg),
    onEnded: (leg) => ended.push(leg),
  })
  sdk.emit("telnyx.notification", { type: "callUpdate", call })
  const attachment = legs[0].answer()
  await answerStarted

  call.state = "destroy"
  sdk.emit("telnyx.notification", { type: "callUpdate", call })
  finishAnswer()

  assert.equal(await attachment, "ended")
  assert.equal(output.srcObject, null)
  assert.equal(legs[0].sendDTMF("5"), false)
  assert.deepEqual(ended, [
    { providerLegID: "losing-leg", mediaToken: "e".repeat(43) },
  ])
  assert.deepEqual(actions, ["answer"])
})

test("a terminal SDK update during audio playback resolves the answer as ended", async () => {
  const output = installMediaDOM()
  const sdk = fakeClient()
  const legs: IncomingMediaLeg[] = []
  let rejectPlayback = () => {}
  let markPlaybackStarted = () => {}
  const playbackStarted = new Promise<void>((resolve) => {
    markPlaybackStarted = resolve
  })
  output.playImplementation = () =>
    new Promise<void>((_resolve, reject) => {
      rejectPlayback = () => reject(new DOMException("interrupted", "AbortError"))
      markPlaybackStarted()
    })
  const call = fakeCall("losing-leg", "g".repeat(43), [])
  const adapter = createCallingMediaAdapter(async () => sdk.client)

  await adapter.connect("jwt", output.id, {
    onState: () => {},
    onIncoming: (leg) => legs.push(leg),
  })
  sdk.emit("telnyx.notification", { type: "callUpdate", call })
  const attachment = legs[0].answer()
  await playbackStarted

  call.state = "destroy"
  sdk.emit("telnyx.notification", { type: "callUpdate", call })
  rejectPlayback()

  assert.equal(await attachment, "ended")
  assert.equal(output.srcObject, null)
})

test("a terminal SDK update before answer makes a stale invite unanswerable", async () => {
  const output = installMediaDOM()
  const sdk = fakeClient()
  const legs: IncomingMediaLeg[] = []
  const ended: Array<{ providerLegID: string; mediaToken: string }> = []
  const actions: string[] = []
  const call = fakeCall("stale-leg", "f".repeat(43), actions)
  const adapter = createCallingMediaAdapter(async () => sdk.client)

  await adapter.connect("jwt", output.id, {
    onState: () => {},
    onIncoming: (leg) => legs.push(leg),
    onEnded: (leg) => ended.push(leg),
  })
  sdk.emit("telnyx.notification", { type: "callUpdate", call })
  call.state = "purge"
  sdk.emit("telnyx.notification", { type: "callUpdate", call })

  assert.equal(await legs[0].answer(), "ended")
  assert.equal(output.srcObject, null)
  assert.deepEqual(ended, [
    { providerLegID: "stale-leg", mediaToken: "f".repeat(43) },
  ])
  assert.deepEqual(actions, [])
})
