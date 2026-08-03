import assert from "node:assert/strict"
import test from "node:test"

import { providerOutcomeLabel } from "./outcomes.ts"

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
