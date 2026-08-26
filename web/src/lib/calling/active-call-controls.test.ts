import assert from "node:assert/strict"
import test from "node:test"

import type { CallingCall } from "../api/generated/types.gen.ts"
import {
  activeCallEndPending,
  endingCallIDAfterProjection,
  showActiveCallEndControl,
} from "./active-call-controls.ts"

function outboundCall(state: CallingCall["state"]): CallingCall {
  return {
    id: "11111111-1111-4111-8111-111111111111",
    practiceId: "22222222-2222-4222-8222-222222222222",
    locationId: "33333333-3333-4333-8333-333333333333",
    locationName: "Synthetic Location",
    direction: "OUTBOUND",
    entryPoint: "STANDALONE",
    state,
    phone: "+15555550123",
    callerId: "+14843336938",
    phoneSource: "",
    displayName: "",
    nameSource: "",
    transferReason: "",
    reasonSource: "",
    providerTermination: "",
    endRequested: false,
    version: 1,
    retryOfCallId: "",
    retryAllowed: false,
  }
}

test("staff-owned outbound Calls expose End throughout every active stage", () => {
  for (const state of [
    "PREPARING",
    "RINGING",
    "CONNECTING",
    "CONNECTED",
  ] as const) {
    assert.equal(showActiveCallEndControl(outboundCall(state), true), true, state)
  }
})

test("End is not projected for another browser or a terminal outbound Call", () => {
  assert.equal(showActiveCallEndControl(outboundCall("RINGING"), false), false)
  assert.equal(showActiveCallEndControl(outboundCall("UNANSWERED"), true), false)
})

test("committed End stays Ending until the authoritative Call settles", () => {
  const callID = "11111111-1111-4111-8111-111111111111"
  for (const state of [
    "PREPARING",
    "RINGING",
    "CONNECTING",
    "CONNECTED",
  ] as const) {
    assert.equal(
      endingCallIDAfterProjection(callID, outboundCall(state)),
      callID,
      state,
    )
  }
  assert.equal(
    endingCallIDAfterProjection(callID, outboundCall("UNANSWERED")),
    "",
  )
  assert.equal(endingCallIDAfterProjection(callID, undefined), "")
})

test("a restored Call keeps durable End intent pending without local state", () => {
  const call = {
    ...outboundCall("RINGING"),
    endRequested: true,
  }
  assert.equal(activeCallEndPending(call, ""), true)
})
