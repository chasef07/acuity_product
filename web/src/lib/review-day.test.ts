import assert from "node:assert/strict"
import test from "node:test"

import { latestReviewDay } from "./review-day.ts"

test("the 4:00 PM cutoff follows Eastern daylight saving time", () => {
  assert.equal(latestReviewDay(new Date("2026-10-12T19:59:00Z")), "2026-10-11")
  assert.equal(latestReviewDay(new Date("2026-10-12T20:00:00Z")), "2026-10-12")
  assert.equal(latestReviewDay(new Date("2026-10-13T03:30:00Z")), "2026-10-12")
  assert.equal(latestReviewDay(new Date("2026-11-02T20:59:00Z")), "2026-11-01")
  assert.equal(latestReviewDay(new Date("2026-11-02T21:00:00Z")), "2026-11-02")
  assert.equal(latestReviewDay(new Date("2026-01-01T04:30:00Z")), "2025-12-31")
})
