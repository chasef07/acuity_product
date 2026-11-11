import assert from "node:assert/strict"
import test from "node:test"

import type { Task } from "./api/generated/types.gen.ts"
import { otherOpenTasks } from "./patient-tasks.ts"

function task(id: string, state: Task["state"] = "OPEN") {
  return { id, state } as Task
}

const returned = Array.from({ length: 50 }, (_, index) => task(`returned-${index + 1}`))
const capped = { openTasks: returned, openTaskCount: 55 }

test("a selected Task among the returned rows leaves the list and the count", () => {
  const others = otherOpenTasks(capped, [returned[0]])
  assert.equal(others.tasks.length, 49)
  assert.ok(!others.tasks.includes(returned[0]))
  assert.equal(others.count, 54)
})

test("a selected open Task the server held back still leaves the count", () => {
  const others = otherOpenTasks(capped, [task("held-back")])
  assert.equal(others.tasks.length, 50)
  assert.equal(others.count, 54)
})

test("a selected group counts every open member, returned or held back", () => {
  const others = otherOpenTasks(capped, [returned[4], task("held-back-1"), task("held-back-2")])
  assert.equal(others.tasks.length, 49)
  assert.equal(others.count, 52)
})

test("a selected Task that is already closed takes nothing from the count", () => {
  const others = otherOpenTasks(capped, [task("closed", "COMPLETED")])
  assert.equal(others.tasks.length, 50)
  assert.equal(others.count, 55)
})

test("a returned row is open whatever the selection's older copy says", () => {
  const others = otherOpenTasks(capped, [task(returned[0].id, "COMPLETED")])
  assert.equal(others.tasks.length, 49)
  assert.equal(others.count, 54)
})

test("the count never falls below the rows listed", () => {
  const three = returned.slice(0, 3)
  const others = otherOpenTasks({ openTasks: three, openTaskCount: 3 }, [task("closed-elsewhere")])
  assert.deepEqual(others, { tasks: three, count: 3 })
})
