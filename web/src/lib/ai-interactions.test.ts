import assert from "node:assert/strict"
import test from "node:test"

import {
  aiCallCompletionLabel,
  appointmentFolder,
  appointmentOutcomeLabel,
  appointmentOutcomeTitle,
  transcriptTurns,
} from "./ai-interactions.ts"

test("routes verified outcomes to their operator folders", () => {
  assert.equal(appointmentFolder("BOOKING"), "bookings")
  assert.equal(appointmentFolder("CANCELLATION"), "cancellations")
  assert.equal(appointmentFolder("RESCHEDULE"), "reschedules")
  assert.equal(appointmentFolder("PARTIAL"), undefined)
  assert.equal(appointmentFolder("INDETERMINATE"), undefined)
})

test("keeps ambiguous outcomes distinguishable for operator review", () => {
  assert.equal(appointmentOutcomeLabel("PARTIAL"), "Partially completed")
  assert.equal(
    appointmentOutcomeLabel("INDETERMINATE"),
    "No verified appointment outcome",
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
})

test("projects caller and AI messages from a LiveKit session report", () => {
  assert.deepEqual(
    transcriptTurns({
      chat_history: {
        items: [
          { id: "system", role: "system", content: ["private prompt"] },
          { id: "caller", role: "user", content: ["I need to reschedule."] },
          {
            id: "assistant",
            role: "assistant",
            content: [{ type: "text", text: "I can help with that." }],
          },
          { id: "tool", role: "tool", content: ["provider receipt"] },
        ],
      },
    }),
    [
      { id: "caller", speaker: "Caller", text: "I need to reschedule." },
      {
        id: "assistant",
        speaker: "AI",
        text: "I can help with that.",
      },
    ],
  )
})
