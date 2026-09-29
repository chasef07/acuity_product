import assert from "node:assert/strict"
import test from "node:test"
import { formatPercent } from "./booking-analytics.ts"

test("zero conversion and no availability checks remain distinct", () => {
  assert.equal(formatPercent(0), "0.0%")
  assert.equal(formatPercent(null), "—")
})
