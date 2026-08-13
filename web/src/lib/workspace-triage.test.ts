import assert from "node:assert/strict"
import test from "node:test"

import {
  filterTasksByCategory,
  reconcileLoadedPage,
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

test("refresh keeps expanded rows while putting new rows first", () => {
  const current = Array.from({ length: 100 }, (_, index) => ({
    id: `task-${index + 1}`,
  }))
  const refreshed = [
    { id: "new-task" },
    ...current.slice(0, 49),
  ]

  const result = reconcileLoadedPage(
    current,
    refreshed,
    162,
    "after-expanded-page",
    "after-first-page",
  )

  assert.equal(result.items[0]?.id, "new-task")
  assert.equal(result.items.length, 101)
  assert.equal(result.items.at(-1)?.id, "task-100")
  assert.equal(result.cursor, "after-expanded-page")
})

test("refresh clears pagination when all rows are loaded", () => {
  const current = [{ id: "task-1" }, { id: "task-2" }]
  const result = reconcileLoadedPage(
    current,
    [{ id: "new-task" }, { id: "task-1" }],
    3,
    "after-expanded-page",
    "after-first-page",
  )

  assert.deepEqual(result.items, [
    { id: "new-task" },
    { id: "task-1" },
    { id: "task-2" },
  ])
  assert.equal(result.cursor, "")
})
