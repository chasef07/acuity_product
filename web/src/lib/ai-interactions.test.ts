import assert from "node:assert/strict"
import test from "node:test"

import {
  aiCallTimelinePresentation,
} from "./ai-interactions.ts"

test("AI call timeline copy leads with meaningful outcomes and exceptions", () => {
  assert.deepEqual(aiCallTimelinePresentation("BOOKING", "COMPLETED"), {
    title: "Appointment booked",
    detail: "AI call",
  })
  assert.deepEqual(aiCallTimelinePresentation("BOOKING", "FAILED"), {
    title: "Appointment booked",
    detail: "AI call failed",
  })
  assert.deepEqual(aiCallTimelinePresentation("INDETERMINATE", "ESCALATED"), {
    title: "Transfer requested",
    detail: "AI call",
  })
  assert.deepEqual(aiCallTimelinePresentation("INDETERMINATE", "FAILED"), {
    title: "AI call failed",
    detail: "",
  })
})
