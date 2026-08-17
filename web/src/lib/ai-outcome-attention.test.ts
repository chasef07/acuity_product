import assert from "node:assert/strict"
import test from "node:test"

import {
  appendOutcomePage,
  appointmentActionForFolder,
  appointmentFolderForAction,
  categorizeAIOutcomes,
  decrementOutcomeCount,
  mergeOutcomePages,
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

test("refreshing outcomes preserves loaded older pages without duplicates", () => {
  const loaded = [{ id: "current" }, { id: "older" }]
  const refreshed = [{ id: "new" }, { id: "current" }]

  assert.deepEqual(mergeOutcomePages(loaded, refreshed), [
    { id: "new" },
    { id: "current" },
    { id: "older" },
  ])
})

test("appending repeated pages keeps each Task visible exactly once", () => {
  const firstPage = [{ id: "task-1" }, { id: "task-2" }]

  assert.deepEqual(
    appendOutcomePage(firstPage, [
      { id: "task-2" },
      { id: "task-3" },
      { id: "task-3" },
    ]),
    [{ id: "task-1" }, { id: "task-2" }, { id: "task-3" }],
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
