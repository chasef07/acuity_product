import assert from "node:assert/strict"
import test from "node:test"

import {
  appointmentFolderForTask,
  filterTasksByCategory,
  projectTaskUpdate,
  refreshTaskWindowTarget,
  taskCountForCategory,
  taskFolderCursor,
} from "./workspace-triage.ts"

test("appointment Tasks use one folder classification in rail and history", () => {
  assert.equal(
    appointmentFolderForTask({
      category: "appointments",
      title: "Review request",
      sourceMessage: "Please move appointment to Friday",
    }),
    "reschedules",
  )
  assert.equal(
    appointmentFolderForTask({
      category: "appointments",
      title: "Schedule new appointment",
    }),
    "bookings",
  )
  assert.equal(
    appointmentFolderForTask({
      category: "billing",
      title: "Cancel balance reminder",
    }),
    undefined,
  )
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

test("live refresh refetches the expanded window plus one authoritative page", () => {
  assert.equal(refreshTaskWindowTarget(0), 50)
  assert.equal(refreshTaskWindowTarget(50), 50)
  assert.equal(refreshTaskWindowTarget(100), 150)
})

test("Task updates replace open rows and remove completed rows", () => {
  type Row = {
    id: string
    state: "OPEN" | "COMPLETED"
    version: number
  }
  const open: Row = { id: "task-1", state: "OPEN", version: 1 }
  const updated = { ...open, version: 2 }
  const completed: Row = { ...updated, state: "COMPLETED" }

  assert.deepEqual(projectTaskUpdate([open], updated), [updated])
  assert.deepEqual(projectTaskUpdate([updated], completed), [])
})
