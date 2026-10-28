import assert from "node:assert/strict"
import test from "node:test"
import { intentBookingDay, intentBookingTotals, overviewDayValue } from "./overview-trend.tsx"

const intents = { newPatientIntentCalls: 5, newPatientIntentBookings: 3, existingPatientIntentCalls: 2, existingPatientIntentBookings: 0 }
const day = { date: "2026-10-17", totalCalls: 4, callMinutes: 10, transferCount: 1, bookingCount: 2, cancellationCount: 1, rescheduleCount: 1, toolCallCount: 8, toolErrorCount: 2, ...intents }

test("overview daily trends calculate averages and rates from the day's denominator", () => {
  assert.equal(overviewDayValue(day, "average"), 2.5)
  assert.equal(overviewDayValue(day, "transfers"), 0.25)
  assert.equal(overviewDayValue(day, "failures"), 0.25)
  assert.equal(overviewDayValue(day, "booked"), 2)
})

test("empty and missing daily evidence remains unknown for rates and new activity fields", () => {
  const empty = { date: day.date, totalCalls: 0, callMinutes: 0, transferCount: 0, newPatientIntentCalls: 0, newPatientIntentBookings: 0, existingPatientIntentCalls: 0, existingPatientIntentBookings: 0 }
  for (const metric of ["average", "transfers", "failures", "booked", "cancelled", "rescheduled"] as const) assert.equal(overviewDayValue(empty, metric), null)
  assert.equal(overviewDayValue(empty, "calls"), 0)
  assert.equal(overviewDayValue({ ...day, toolErrorCount: 0 }, "failures"), 0)
})

test("intent booking trends split each intent's calls into booked and not booked", () => {
  assert.deepEqual(intentBookingDay(day, "newPatientBookings"), { date: "2026-10-17", booked: 3, notBooked: 2 })
  assert.deepEqual(intentBookingDay(day, "existingPatientBookings"), { date: "2026-10-17", booked: 0, notBooked: 2 })
  const quiet = { ...day, date: "2026-10-18", newPatientIntentCalls: 0, newPatientIntentBookings: 0 }
  assert.deepEqual(intentBookingTotals([day, quiet], "newPatientBookings"), { calls: 5, booked: 3 })
  assert.deepEqual(intentBookingTotals([quiet], "newPatientBookings"), { calls: 0, booked: 0 })
})
