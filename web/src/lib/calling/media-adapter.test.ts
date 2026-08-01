import assert from "node:assert/strict"
import test from "node:test"

import {
  applyMicrophoneFence,
  callingClientOptions,
  createCallingMediaAdapter,
  type IncomingMediaLeg,
  rejectMediaCall,
} from "./media-adapter.ts"

class FakeAudioElement {
  id = ""
  autoplay = false
  muted = false
  volume = 1
  className = ""
  srcObject: unknown = null
  plays = 0

  async play() {
    this.plays += 1
  }

  remove() {}
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
  const client = {
    remoteElement: "",
    connect: async () => {},
    serverDisconnect: async () => {},
    on: (event: string, callback: (value?: unknown) => void) => {
      listeners.set(event, callback)
      return client
    },
  }
  return {
    client,
    emit: (event: string, value?: unknown) => listeners.get(event)?.(value),
  }
}

function fakeCall(
  providerLegID: string,
  mediaToken: string,
  actions: string[],
  hasRemoteStream = true,
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
    answer: async () => {
      actions.push("answer")
    },
    hangup: async () => {},
    muteAudio: () => actions.push("mute"),
    unmuteAudio: () => actions.push("unmute"),
    dtmf: (digit: string) => actions.push(`dtmf:${digit}`),
  }
}

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
