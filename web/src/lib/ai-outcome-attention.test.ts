import assert from "node:assert/strict"
import test from "node:test"

import {
  appointmentFolderForAction,
  decrementOutcomeCount,
  mergeOutcomePages,
} from "./ai-outcome-attention.ts"

test("appointment actions have one authoritative review folder", () => {
  assert.equal(appointmentFolderForAction("BOOKED"), "bookings")
  assert.equal(appointmentFolderForAction("CANCELLED"), "cancellations")
  assert.equal(appointmentFolderForAction("RESCHEDULED"), "reschedules")
  assert.equal(appointmentFolderForAction(), undefined)
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
