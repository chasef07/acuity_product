import assert from "node:assert/strict"
import test from "node:test"

import {
  taskCountForCategory,
} from "./workspace-triage.ts"

test("Task category totals do not depend on the loaded page", () => {
  const counts = {
    tasks: 11,
    missedCalls: 39,
    categories: {
      billing: 3,
      appointments: 2,
      documentation: 1,
      optical: 1,
      medication: 1,
      referrals: 1,
      other: 2,
    },
  }

  assert.equal(taskCountForCategory(counts, "all"), 11)
  assert.equal(taskCountForCategory(counts, "billing"), 3)
})
