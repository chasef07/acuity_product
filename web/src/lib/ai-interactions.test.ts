import assert from "node:assert/strict"
import test from "node:test"

import {
  aiCallCompletionLabel,
  appointmentFolder,
  appointmentOutcomeLabel,
  appointmentOutcomeTitle,
} from "./ai-interactions.ts"

test("routes verified outcomes to their operator folders", () => {
  assert.equal(appointmentFolder("BOOKING"), "bookings")
  assert.equal(appointmentFolder("CANCELLATION"), "cancellations")
  assert.equal(appointmentFolder("RESCHEDULE"), "reschedules")
  assert.equal(appointmentFolder("PARTIAL"), undefined)
  assert.equal(appointmentFolder("INDETERMINATE"), undefined)
})

test("keeps ambiguous outcomes distinguishable without implying staff work", () => {
  assert.equal(appointmentOutcomeLabel("PARTIAL"), "Partially completed")
  assert.equal(
    appointmentOutcomeLabel("INDETERMINATE"),
    "No appointment actions",
  )
})

test("labels AI completion separately from staff transfer", () => {
  assert.equal(aiCallCompletionLabel("COMPLETED"), "Completed by AI")
  assert.equal(aiCallCompletionLabel("ESCALATED"), "Transferred to staff")
  assert.equal(aiCallCompletionLabel("FAILED"), "AI call failed")
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
