import assert from "node:assert/strict"
import test from "node:test"

import type { CallingCall } from "../api/generated/types.gen.ts"
import { isSettledCallState } from "./call-state.ts"

const expected: Record<CallingCall["state"], boolean> = {
  PREPARING: false,
  RINGING: false,
  CONNECTING: false,
  CONNECTED: false,
  VOICEMAIL_GREETING: false,
  VOICEMAIL_RECORDING: false,
  UNANSWERED: true,
  VOICEMAIL: true,
  MISSED: true,
  NEEDS_DISPOSITION: true,
  RESOLVED: true,
  FOLLOW_UP_REQUIRED: true,
}

test("every generated CallingCall state is classified as settled or live", () => {
  for (const state of Object.keys(expected) as CallingCall["state"][]) {
    assert.equal(isSettledCallState(state), expected[state], state)
  }
})
