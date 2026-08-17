import assert from "node:assert/strict"
import test from "node:test"

import type {
  CallingState,
  CallingStateCall,
  RingingCallLeg,
} from "../api/generated/types.gen.ts"

import {
	confirmOutboundMediaWithRetry,
  answeredCallLegStatus,
  currentCallingStateCallID,
  mediaAttachmentAfterState,
	microphoneFailureMessage,
  routeIncomingMedia,
} from "./dock-media-state.ts"

function stateCall(
  callId: string,
  callLegId: string,
  state = "CONNECTED",
): CallingStateCall {
  return {
    callId,
    callLegId,
    practiceId: "practice-1",
    locationId: "location-1",
    locationName: "Location 1",
    state,
    version: 1,
  }
}

function ringingCallLeg(
  callId: string,
  callLegId: string,
): RingingCallLeg {
  return {
    ...stateCall(callId, callLegId, "BRIDGE_PENDING"),
    mediaToken: "a".repeat(43),
    displayName: "Caller",
    phone: "+15555550100",
    transferReason: "",
    state: "BRIDGE_PENDING",
    createdAt: "2026-08-17T00:00:00Z",
    deadline: "2026-08-17T00:00:20Z",
  }
}

function callingState(
  overrides: Partial<CallingState> = {},
): CallingState {
  return {
    softphone: {
      sessionId: "session-1",
      leaseExpiresAt: "2026-08-17T00:01:00Z",
      owner: true,
      available: false,
      activeCallId: "",
      pendingOutcomeCallId: "",
    },
    ringing: [],
    ...overrides,
  }
}

test("answered CallLeg controls require the exact bridged winner", () => {
  const expected = { callId: "call-1", callLegId: "leg-loser" }

  assert.equal(
    answeredCallLegStatus(
      callingState({
        ringing: [ringingCallLeg("call-1", "leg-loser")],
      }),
      expected,
    ),
    "PENDING",
  )
  assert.equal(
    answeredCallLegStatus(
      callingState({ bridged: stateCall("call-1", "leg-loser") }),
      expected,
    ),
    "BRIDGED",
  )
  assert.equal(
    answeredCallLegStatus(
      callingState({ bridged: stateCall("call-1", "leg-winner") }),
      expected,
    ),
    "LOST",
  )
  assert.equal(answeredCallLegStatus(callingState(), expected), "LOST")
})

test("calling state leaves provider voicemail out of staff call controls", () => {
  assert.equal(currentCallingStateCallID(callingState()), undefined)
  assert.equal(
    currentCallingStateCallID(
      callingState({
        voicemail: stateCall("call-1", "caller-leg", "VOICEMAIL_GREETING"),
      }),
    ),
    undefined,
  )
  assert.equal(
    currentCallingStateCallID(
      callingState({
        voicemail: stateCall("call-1", "caller-leg", "VOICEMAIL_RECORDING"),
      }),
    ),
    undefined,
  )
  assert.equal(
    currentCallingStateCallID(
      callingState({
        voicemail: stateCall("call-1", "caller-leg", "VOICEMAIL"),
      }),
    ),
    undefined,
  )
  assert.equal(
    currentCallingStateCallID(
      callingState({
        voicemail: stateCall("call-1", "caller-leg", "VOICEMAIL"),
        disposition: stateCall(
          "call-2",
          "staff-leg",
          "NEEDS_DISPOSITION",
        ),
      }),
    ),
    "call-2",
  )
})

test("inbound media recovery keeps its attachment across reconnect", () => {
  const attached = {
    providerLegID: "inbound-leg",
    mediaToken: "a".repeat(43),
  }
  const reconnecting = mediaAttachmentAfterState("reconnecting", attached)

  assert.equal(reconnecting, attached)
  assert.equal(
    routeIncomingMedia(
      reconnecting,
      { ...attached, recovery: true },
      true,
      "ready",
      "inbound-call-id",
    ),
    "RECOVER_ATTACHED",
  )
})

test("unavailable media discards its stale attachment", () => {
  assert.equal(
    mediaAttachmentAfterState("unavailable", {
      providerLegID: "stale-leg",
      mediaToken: "b".repeat(43),
    }),
    null,
  )
})

test("outbound media confirmation converges after projected answer evidence", async () => {
	let attempts = 0
	const result = await confirmOutboundMediaWithRetry(
		async () => {
			attempts += 1
			return attempts < 3
				? { status: 409 }
				: { status: 200, data: { callId: "outbound-call" } }
		},
		async () => {},
		3,
	)
	assert.deepEqual(result, { callId: "outbound-call" })
	assert.equal(attempts, 3)
})

test("outbound media confirmation does not retry terminal rejection", async () => {
	let attempts = 0
	const result = await confirmOutboundMediaWithRetry(
		async () => {
			attempts += 1
			return { status: 403 }
		},
		async () => {},
	)
	assert.equal(result, undefined)
	assert.equal(attempts, 1)
})

test("microphone failures provide device-specific recovery", () => {
	assert.match(
		microphoneFailureMessage(new DOMException("denied", "NotAllowedError")),
		/Allow microphone access/,
	)
	assert.match(
		microphoneFailureMessage(new DOMException("missing", "NotFoundError")),
		/No microphone was found/,
	)
	assert.match(
		microphoneFailureMessage(new DOMException("busy", "NotReadableError")),
		/microphone is busy/,
	)
})
