import assert from "node:assert/strict"
import test from "node:test"

import {
  decrementOutcomeCount,
  mergeOutcomePages,
} from "./ai-outcome-attention.ts"

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
  const counts = { bookings: 2, cancellations: 1, reschedules: 1 }

  assert.deepEqual(decrementOutcomeCount(counts, "BOOKING"), {
    bookings: 1,
    cancellations: 1,
    reschedules: 1,
  })
  assert.deepEqual(decrementOutcomeCount(counts, "NONE"), counts)
})
