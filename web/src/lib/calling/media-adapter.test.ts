import assert from "node:assert/strict"
import test from "node:test"

import {
  applyMicrophoneFence,
  callingClientOptions,
  rejectMediaCall,
} from "./media-adapter.ts"

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
