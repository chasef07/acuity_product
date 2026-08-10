import assert from "node:assert/strict"
import test from "node:test"

import {
  filterTasksByCategory,
  recoveryGroupKey,
} from "./workspace-triage.ts"

test("Task categories filter only the supplied Task rows", () => {
  const tasks = [
    { id: "billing", category: "billing" as const },
    { id: "medication", category: "medication" as const },
  ]

  assert.deepEqual(filterTasksByCategory(tasks, "all"), tasks)
  assert.deepEqual(filterTasksByCategory(tasks, "billing"), [tasks[0]])
})

test("recovery groups distinguish the same phone at different Locations", () => {
  assert.notEqual(
    recoveryGroupKey("location-1", "+17275550199"),
    recoveryGroupKey("location-2", "+17275550199"),
  )
})
