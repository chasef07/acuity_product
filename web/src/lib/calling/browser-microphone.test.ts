import assert from "node:assert/strict"
import test from "node:test"

import { createBrowserMicrophone } from "./browser-microphone.ts"

test("abort during browser audio startup releases microphone capture", async () => {
  const originalNavigator = Object.getOwnPropertyDescriptor(globalThis, "navigator")
  const originalAudioContext = Object.getOwnPropertyDescriptor(
    globalThis,
    "AudioContext",
  )
  const microphone = new EventTarget() as EventTarget & {
    readyState: MediaStreamTrackState
    stop(): void
  }
  microphone.readyState = "live"
  let trackStops = 0
  microphone.stop = () => {
    trackStops += 1
  }
  let deviceListeners = 0
  const mediaDevices = {
    getUserMedia: async () => ({
      getAudioTracks: () => [microphone],
      getTracks: () => [microphone],
    }),
    enumerateDevices: async () => [{ kind: "audioinput" }],
    addEventListener: () => {
      deviceListeners += 1
    },
    removeEventListener: () => {
      deviceListeners -= 1
    },
  }
  const resumeStarted = Promise.withResolvers<void>()
  let closeCalls = 0
  class HangingAudioContext {
    state: AudioContextState = "running"
    async resume() {
      resumeStarted.resolve()
      return new Promise<void>(() => undefined)
    }
    async close() {
      closeCalls += 1
      this.state = "closed"
    }
  }

  Object.defineProperty(globalThis, "navigator", {
    configurable: true,
    value: { mediaDevices },
  })
  Object.defineProperty(globalThis, "AudioContext", {
    configurable: true,
    value: HangingAudioContext,
  })

  try {
    const controller = new AbortController()
    const starting = createBrowserMicrophone().start(
      () => assert.fail("microphone should not become unavailable"),
      controller.signal,
    )
    await resumeStarted.promise
    controller.abort()

    await assert.rejects(starting, { name: "AbortError" })
    assert.equal(trackStops, 1)
    assert.equal(deviceListeners, 0)
    assert.equal(closeCalls, 1)
  } finally {
    if (originalNavigator) {
      Object.defineProperty(globalThis, "navigator", originalNavigator)
    } else {
      Reflect.deleteProperty(globalThis, "navigator")
    }
    if (originalAudioContext) {
      Object.defineProperty(globalThis, "AudioContext", originalAudioContext)
    } else {
      Reflect.deleteProperty(globalThis, "AudioContext")
    }
  }
})
