import assert from "node:assert/strict"
import test from "node:test"

import {
  applyOutcomePages,
  appointmentActionForFolder,
  appointmentFolderForAction,
  categorizeAIOutcomes,
  decrementOutcomeCount,
} from "./ai-outcome-attention.ts"

test("AI call outcomes are not classified as Tasks", () => {
  const booked = { id: "booked", appointmentAction: "BOOKED" }
  const transferred = { id: "transferred", appointmentAction: undefined }
  const failed = { id: "failed", appointmentAction: undefined }

  assert.deepEqual(
    categorizeAIOutcomes([booked, transferred, failed]),
    {
      bookings: [booked],
      cancellations: [],
      reschedules: [],
    },
  )
})

test("appointment actions have one authoritative review folder", () => {
  assert.equal(appointmentFolderForAction("BOOKED"), "bookings")
  assert.equal(appointmentFolderForAction("CANCELLED"), "cancellations")
  assert.equal(appointmentFolderForAction("RESCHEDULED"), "reschedules")
  assert.equal(appointmentFolderForAction(), undefined)
  assert.equal(appointmentActionForFolder("bookings"), "BOOKED")
  assert.equal(appointmentActionForFolder("cancellations"), "CANCELLED")
  assert.equal(appointmentActionForFolder("reschedules"), "RESCHEDULED")
})

test("refreshing outcomes resets to a contiguous page and its cursor", () => {
  const loaded = Array.from({ length: 10 }, (_, index) => ({
    id: `previous-${index + 1}`,
  }))
  const refreshed = Array.from({ length: 10 }, (_, index) => ({
    id: `new-${index + 1}`,
  }))

  assert.deepEqual(
    applyOutcomePages(
      loaded,
      [
        {
          folder: "bookings",
          items: refreshed,
          nextCursor: "after-new-10",
        },
      ],
      false,
    ),
    {
      items: refreshed,
      nextCursors: { bookings: "after-new-10" },
    },
  )
})

test("reviewing an outcome decrements only its authoritative folder total", () => {
  const counts = { tasks: 2, bookings: 2, cancellations: 1, reschedules: 1 }

  assert.deepEqual(decrementOutcomeCount(counts, "BOOKED"), {
    tasks: 2,
    bookings: 1,
    cancellations: 1,
    reschedules: 1,
  })
  assert.deepEqual(decrementOutcomeCount(counts), {
    ...counts,
    tasks: 1,
  })
})
