import assert from "node:assert/strict"
import test from "node:test"
import {
  bookingConversionExplanation,
  formatPercent,
} from "./booking-analytics.ts"

test("conversion explanation names the full booked-call numerator and denominator", () => {
  assert.equal(
    bookingConversionExplanation({ converted: 496, searched: 1024 }),
    "496 of 1,024 calls booked after a completed availability search.",
  )
})

test("zero bookings and no availability checks remain distinct", () => {
  assert.equal(
    bookingConversionExplanation({ converted: 0, searched: 12 }),
    "0 of 12 calls booked after a completed availability search.",
  )
  assert.equal(
    bookingConversionExplanation({ converted: 0, searched: 0 }),
    "No completed availability searches in this period.",
  )
  assert.equal(formatPercent(0), "0.0%")
  assert.equal(formatPercent(null), "—")
})
