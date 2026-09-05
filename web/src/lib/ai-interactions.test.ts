import assert from "node:assert/strict"
import test from "node:test"

import {
  aiCallCompletionLabel,
  aiCallTimelinePresentation,
  appointmentOutcomeLabel,
  appointmentOutcomeTitle,
} from "./ai-interactions.ts"

test("keeps ambiguous outcomes distinguishable without implying staff work", () => {
  assert.equal(appointmentOutcomeLabel("PARTIAL"), "Partially completed")
  assert.equal(
    appointmentOutcomeLabel("INDETERMINATE"),
    "No appointment actions",
  )
})

test("labels AI completion separately from staff transfer", () => {
  assert.equal(aiCallCompletionLabel("COMPLETED"), "Call completed")
  assert.equal(aiCallCompletionLabel("ESCALATED"), "Transferred to staff")
  assert.equal(aiCallCompletionLabel("FAILED"), "AI call failed")
})

test("routine AI calls keep timeline copy quiet", () => {
  assert.deepEqual(aiCallTimelinePresentation("INDETERMINATE", "COMPLETED"), {
    title: "AI call",
    detail: "",
  })
})

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
    title: "Transferred to staff",
    detail: "AI call",
  })
  assert.deepEqual(aiCallTimelinePresentation("INDETERMINATE", "FAILED"), {
    title: "AI call failed",
    detail: "",
  })
})

test("uses receipt-backed appointment language", () => {
  assert.equal(appointmentOutcomeTitle("BOOKING"), "Appointment booked")
  assert.equal(
    appointmentOutcomeTitle("RESCHEDULE"),
    "Appointment rescheduled",
  )
  assert.equal(
    appointmentOutcomeTitle("PARTIAL"),
    "Appointment change needs review",
  )
  assert.equal(appointmentOutcomeTitle("INDETERMINATE"), "AI call")
})
