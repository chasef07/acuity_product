import assert from "node:assert/strict"
import test from "node:test"

import {
  aiAppointmentDetails,
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

test("projects Acuity-style booking details from structured closeout evidence", () => {
  assert.deepEqual(
    aiAppointmentDetails({
      appointmentOutcome: "BOOKING",
      closeoutPayload: {
        appointmentActions: [
          {
            action: "booked",
            appointment: {
              patientName: "Jane Doe",
              appointmentDate: "2026-08-12",
              appointmentTime: "9:00 AM",
              providerName: "Dr. Bach",
              locationName: "Spring Hill",
              appointmentTypeName: "New medical patient",
              careLane: "medical_md",
            },
          },
        ],
      },
      newAppointmentId: "appointment-new",
    }),
    {
      primary: {
        appointmentDate: "2026-08-12",
        appointmentId: "appointment-new",
        appointmentTime: "9:00 AM",
        appointmentTypeName: "New medical patient",
        careLane: "medical_md",
        locationName: "Spring Hill",
        patientName: "Jane Doe",
        providerName: "Dr. Bach",
      },
    },
  )
})

test("shows both appointments for a reschedule", () => {
  assert.deepEqual(
    aiAppointmentDetails({
      appointmentOutcome: "RESCHEDULE",
      newAppointmentId: "appointment-new",
      oldAppointmentId: "appointment-old",
      closeoutPayload: {
        appointmentActions: [
          {
            action: "rescheduled",
            appointment: {
              appointmentDate: "2026-08-20",
              appointmentTime: "2:30 PM",
            },
            cancelledAppointment: {
              appointmentDate: "2026-08-12",
              appointmentTime: "9:00 AM",
            },
          },
        ],
      },
    }),
    {
      primary: {
        appointmentDate: "2026-08-20",
        appointmentId: "appointment-new",
        appointmentTime: "2:30 PM",
      },
      previous: {
        appointmentDate: "2026-08-12",
        appointmentId: "appointment-old",
        appointmentTime: "9:00 AM",
      },
    },
  )
})

test("falls back to receipt fields before rich closeout arrives", () => {
  assert.deepEqual(
    aiAppointmentDetails({
      appointmentOutcome: "CANCELLATION",
      oldAppointmentId: "appointment-old",
      cancellationResult: {
        status: "cancelled",
        patientName: "Jane Doe",
        providerName: "Dr. Bach",
      },
    }),
    {
      primary: {
        appointmentId: "appointment-old",
        patientName: "Jane Doe",
        providerName: "Dr. Bach",
      },
    },
  )
})
