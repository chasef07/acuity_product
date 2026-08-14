import assert from "node:assert/strict"
import test from "node:test"

import {
  callIsSettled,
  dispositionWindowIsOpen,
  hangupFailure,
  providerOutcomeLabel,
} from "./outcomes.ts"

const outcomes = {
  COMPLETED: "Completed",
  NO_ANSWER: "No answer",
  BUSY: "Busy",
  DECLINED: "Declined",
  FAILED: "Failed",
  STATUS_UNKNOWN: "Status unknown",
  MEDIA_READINESS_FAILED: "Media failure",
  MEDIA_FAILURE: "Media failure",
}

for (const [providerOutcome, label] of Object.entries(outcomes)) {
  test(`renders ${providerOutcome} as ${label}`, () => {
    assert.equal(providerOutcomeLabel(providerOutcome), label)
  })
}

test("disposition popup closes at its authoritative deadline", () => {
  const deadline = "2026-08-09T18:40:30Z"

  assert.equal(
    dispositionWindowIsOpen(deadline, Date.parse("2026-08-09T18:40:29.999Z")),
    true,
  )
  assert.equal(
    dispositionWindowIsOpen(deadline, Date.parse("2026-08-09T18:40:30Z")),
    false,
  )
})

test("Hang up conflicts reconcile authoritative terminal Call state", () => {
  assert.equal(
    hangupFailure({
      status: 409,
      code: "CALL_CONFLICT",
    }),
    "conflict",
  )
  assert.equal(callIsSettled("NEEDS_DISPOSITION"), true)
  assert.equal(callIsSettled("RESOLVED"), true)
  assert.equal(callIsSettled("CONNECTED"), false)
})

test("Hang up connection copy is limited to transport and unavailable failures", () => {
  assert.equal(hangupFailure(undefined), "retry")
  assert.equal(hangupFailure({ status: 503, code: "UNAVAILABLE" }), "retry")
  assert.equal(hangupFailure({ status: 401, code: "UNAUTHORIZED" }), "authentication")
  assert.equal(hangupFailure({ status: 403, code: "ACCESS_DENIED" }), "authentication")
  assert.equal(hangupFailure({ status: 400, code: "INVALID_REQUEST" }), "request")
})
