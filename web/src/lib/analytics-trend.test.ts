import assert from "node:assert/strict"
import { test } from "node:test"
import { dailyTotalComparison } from "./analytics-trend.ts"

test("daily comparisons use equal complete-day windows", () => {
  assert.equal(dailyTotalComparison([2, 2, 4, 4]), "Up 100.0% · Last 2 complete days vs preceding 2")
  assert.equal(dailyTotalComparison([999, 4, 4, 2, 2]), "Down 50.0% · Last 2 complete days vs preceding 2")
  assert.equal(dailyTotalComparison([2, 2]), "Unchanged · Last 1 complete day vs preceding 1")
})
test("daily comparisons distinguish zero, missing data, and insufficient history", () => {
  assert.equal(dailyTotalComparison([0, 1]), "Up from zero · Last 1 complete day vs preceding 1")
  assert.match(dailyTotalComparison([null, 1]), /missing usage/)
  assert.match(dailyTotalComparison([8]), /longer range/)
})

test("equal monetary totals ignore floating-point accumulation error", () => {
  assert.match(dailyTotalComparison([0.1, 0.2, 0.3, 0]), /^Unchanged/)
  assert.match(dailyTotalComparison([0.3, 0, 0.1, 0.2]), /^Unchanged/)
  assert.match(dailyTotalComparison([0, 0.000001]), /^Up from zero/)
  assert.match(dailyTotalComparison([1, 1.00001]), /^Up <0.1%/)
})
