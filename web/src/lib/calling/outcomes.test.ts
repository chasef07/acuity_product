import assert from "node:assert/strict"
import test from "node:test"

import {
  dispositionWindowIsOpen,
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
