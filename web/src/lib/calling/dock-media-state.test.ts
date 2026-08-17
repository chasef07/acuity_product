import assert from "node:assert/strict"
import test from "node:test"

import {
	confirmOutboundMediaWithRetry,
  answeredCallLegStatus,
  currentCallingStateCallID,
  mediaAttachmentAfterState,
	microphoneFailureMessage,
  routeIncomingMedia,
} from "./dock-media-state.ts"

test("answered CallLeg controls require the exact bridged winner", () => {
  const expected = { callId: "call-1", callLegId: "leg-loser" }

  assert.equal(
    answeredCallLegStatus(
      {
        ringing: [
          { callId: "call-1", callLegId: "leg-loser", state: "BRIDGE_PENDING" },
        ],
      },
      expected,
    ),
    "PENDING",
  )
  assert.equal(
    answeredCallLegStatus(
      { bridged: { callId: "call-1", callLegId: "leg-loser" } },
      expected,
    ),
    "BRIDGED",
  )
  assert.equal(
    answeredCallLegStatus(
      { bridged: { callId: "call-1", callLegId: "leg-winner" } },
      expected,
    ),
    "LOST",
  )
  assert.equal(answeredCallLegStatus({}, expected), "LOST")
})

test("calling state leaves provider voicemail out of staff call controls", () => {
  assert.equal(currentCallingStateCallID({}), undefined)
  assert.equal(
    currentCallingStateCallID({
      voicemail: { callId: "call-1", state: "VOICEMAIL_GREETING" },
    }),
    undefined,
  )
  assert.equal(
    currentCallingStateCallID({
      voicemail: { callId: "call-1", state: "VOICEMAIL_RECORDING" },
    }),
    undefined,
  )
  assert.equal(
    currentCallingStateCallID({
      voicemail: { callId: "call-1", state: "VOICEMAIL" },
    }),
    undefined,
  )
  assert.equal(
    currentCallingStateCallID({
      voicemail: { callId: "call-1", state: "VOICEMAIL" },
      disposition: { callId: "call-2", state: "NEEDS_DISPOSITION" },
    }),
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
