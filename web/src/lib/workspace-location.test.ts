import assert from "node:assert/strict"
import test from "node:test"

import {
  parseWorkspaceLocation,
  selectedWorkspaceLocation,
  workspaceLocationSearch,
} from "./workspace-location.ts"

test("workspace URLs name a Task or page view by opaque identifier only", () => {
  assert.deepEqual(parseWorkspaceLocation("?task=task-1"), { view: "task", taskID: "task-1" })
  assert.deepEqual(parseWorkspaceLocation("?view=manage-agent"), { view: "manage-agent" })
  assert.deepEqual(parseWorkspaceLocation("?task=task-1&view=analytics"), { view: "task", taskID: "task-1" })
  assert.deepEqual(parseWorkspaceLocation("?view=engagement"), { view: "none" })
  assert.deepEqual(parseWorkspaceLocation("?task="), { view: "none" })
  assert.deepEqual(parseWorkspaceLocation(""), { view: "none" })
  assert.equal(workspaceLocationSearch({ view: "task", taskID: "task 1" }), "?task=task+1")
  assert.equal(workspaceLocationSearch({ view: "operator-analytics" }), "?view=operator-analytics")
  assert.equal(workspaceLocationSearch({ view: "none" }), "")
})

test("only a selected Task or page view becomes a workspace URL", () => {
  assert.deepEqual(selectedWorkspaceLocation({ view: "engagement", task: { id: "task-1" } }), { view: "task", taskID: "task-1" })
  assert.deepEqual(selectedWorkspaceLocation({ view: "engagement" }), { view: "none" })
  assert.deepEqual(selectedWorkspaceLocation({ view: "analytics", task: { id: "task-1" } }), { view: "analytics" })
  assert.deepEqual(selectedWorkspaceLocation({ view: "none" }), { view: "none" })
})
