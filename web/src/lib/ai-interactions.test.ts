import assert from "node:assert/strict"
import test from "node:test"

import { aiCallTitle } from "./ai-interactions.ts"

test("an AI call title leads with meaningful outcomes and exceptions", () => {
  assert.equal(aiCallTitle("BOOKING", "COMPLETED"), "Appointment booked")
  assert.equal(aiCallTitle("BOOKING", "FAILED"), "Appointment booked")
  assert.equal(aiCallTitle("INDETERMINATE", "COMPLETED"), "AI call")
  assert.equal(aiCallTitle("INDETERMINATE", "ESCALATED"), "Transfer requested")
  assert.equal(aiCallTitle("INDETERMINATE", "FAILED"), "AI call failed")
  assert.equal(aiCallTitle("INDETERMINATE", "IN_PROGRESS"), "AI call in progress")
})
