import assert from "node:assert/strict"
import test from "node:test"

import {
  parseWorkspaceLocation,
  selectedWorkspaceLocation,
  workspaceLocationSearch,
} from "./workspace-location.ts"

test("workspace URLs name a Task or page view by opaque identifier only", () => {
  assert.deepEqual(parseWorkspaceLocation("?task=task-1"), { view: "task", taskID: "task-1" })
  assert.deepEqual(parseWorkspaceLocation("?view=work"), { view: "work" })
  assert.deepEqual(parseWorkspaceLocation("?task=task-1&view=analytics"), { view: "task", taskID: "task-1" })
  assert.equal(workspaceLocationSearch({ view: "task", taskID: "task 1" }), "?task=task+1")
  assert.equal(workspaceLocationSearch({ view: "operator-analytics" }), "?view=operator-analytics")
  assert.equal(workspaceLocationSearch({ view: "work" }), "?view=work")
})

test("the bare workspace URL opens the agent Home", () => {
  for (const search of ["", "?view=manage-agent", "?view=engagement", "?task=", "?view=insurance-rules"]) {
    assert.deepEqual(parseWorkspaceLocation(search), { view: "manage-agent" }, search)
  }
  assert.equal(workspaceLocationSearch({ view: "manage-agent" }), "")
  assert.deepEqual(selectedWorkspaceLocation({ view: "manage-agent", agentPage: "home" }), { view: "manage-agent" })
})

test("Manage agent keeps its other pages in the URL", () => {
  assert.deepEqual(parseWorkspaceLocation("?view=manage-agent&page=insurance"), { view: "manage-agent", page: "insurance" })
  assert.deepEqual(parseWorkspaceLocation("?view=manage-agent&page=knowledge"), { view: "manage-agent", page: "knowledge" })
  assert.deepEqual(parseWorkspaceLocation("?view=manage-agent&page=transcripts"), { view: "manage-agent", page: "transcripts" })
  assert.deepEqual(parseWorkspaceLocation("?view=manage-agent&page=other"), { view: "manage-agent" })
  assert.deepEqual(parseWorkspaceLocation("?view=analytics&page=insurance"), { view: "analytics" })
  assert.equal(workspaceLocationSearch({ view: "manage-agent", page: "insurance" }), "?view=manage-agent&page=insurance")
  assert.deepEqual(selectedWorkspaceLocation({ view: "manage-agent", agentPage: "knowledge" }), { view: "manage-agent", page: "knowledge" })
  assert.deepEqual(selectedWorkspaceLocation({ view: "manage-agent", agentPage: "transcripts" }), { view: "manage-agent", page: "transcripts" })
  assert.deepEqual(selectedWorkspaceLocation({ view: "analytics", agentPage: "insurance" }), { view: "analytics" })
})

test("Work without a selected Task is its own URL", () => {
  assert.deepEqual(selectedWorkspaceLocation({ view: "engagement", task: { id: "task-1" } }), { view: "task", taskID: "task-1" })
  assert.deepEqual(selectedWorkspaceLocation({ view: "engagement" }), { view: "work" })
  assert.deepEqual(selectedWorkspaceLocation({ view: "analytics", task: { id: "task-1" } }), { view: "analytics" })
  assert.deepEqual(selectedWorkspaceLocation({ view: "none" }), { view: "work" })
})
