import assert from "node:assert/strict"
import test from "node:test"

import {
  filterTasksByCategory,
  recoveryGroupKey,
  taskCountForCategory,
  taskFolderCursor,
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

test("Task category totals do not depend on the loaded page", () => {
  const counts = {
    tasks: 11,
    missedCalls: 39,
    bookings: 2,
    cancellations: 1,
    reschedules: 0,
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

test("Task folders keep pagination available until their total is loaded", () => {
  assert.equal(taskFolderCursor("next-page", 0, 3), "next-page")
  assert.equal(taskFolderCursor("next-page", 3, 3), "")
  assert.equal(taskFolderCursor("", 0, 3), "")
})
