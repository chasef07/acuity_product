import assert from "node:assert/strict"
import test from "node:test"

import {
  filterTasksByCategory,
  filterTaskQueue,
  sortRecoveryQueue,
  taskCountForCategory,
  taskFolderCursor,
} from "./workspace-triage.ts"

test("Tasks keep appointment work while recovery calls stay in Missed Calls", () => {
  const appointmentTask = {
    id: "appointment-task",
    origin: "ABITA_AI" as const,
    category: "appointments" as const,
  }
  const missedCall = {
    id: "missed-call",
    origin: "MISSED_CALL_RECOVERY" as const,
    category: "other" as const,
  }

  assert.deepEqual(filterTaskQueue([appointmentTask, missedCall]), [
    appointmentTask,
  ])
})

test("Task categories filter only the supplied Task rows", () => {
  const tasks = [
    { id: "billing", category: "billing" as const },
    { id: "medication", category: "medication" as const },
  ]

  assert.deepEqual(filterTasksByCategory(tasks, "all"), tasks)
  assert.deepEqual(filterTasksByCategory(tasks, "billing"), [tasks[0]])
})

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

test("Task folders keep pagination available until their total is loaded", () => {
  assert.equal(taskFolderCursor("next-page", 0, 3), "next-page")
  assert.equal(taskFolderCursor("next-page", 3, 3), "")
  assert.equal(taskFolderCursor("", 0, 3), "")
})

test("Missed Calls keep newest activity first across pages", () => {
  const recentlyUpdated = {
    id: "recently-updated",
    createdAt: "2026-08-16T08:00:00Z",
    updatedAt: "2026-08-16T12:00:00Z",
  }
  const recentlyCreated = {
    id: "recently-created",
    createdAt: "2026-08-16T10:00:00Z",
    updatedAt: "2026-08-16T10:00:00Z",
  }

  assert.deepEqual(
    sortRecoveryQueue([recentlyCreated, recentlyUpdated]),
    [recentlyUpdated, recentlyCreated],
  )
})
