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

test("Manage agent keeps its page in the URL and defaults to Transcripts", () => {
  assert.deepEqual(parseWorkspaceLocation("?view=manage-agent&page=insurance"), { view: "manage-agent", page: "insurance" })
  assert.deepEqual(parseWorkspaceLocation("?view=manage-agent&page=knowledge"), { view: "manage-agent", page: "knowledge" })
  assert.deepEqual(parseWorkspaceLocation("?view=manage-agent&page=transcripts"), { view: "manage-agent" })
  assert.deepEqual(parseWorkspaceLocation("?view=manage-agent&page=other"), { view: "manage-agent" })
  assert.deepEqual(parseWorkspaceLocation("?view=analytics&page=insurance"), { view: "analytics" })
  assert.deepEqual(parseWorkspaceLocation("?view=insurance-rules"), { view: "none" })
  assert.equal(workspaceLocationSearch({ view: "manage-agent", page: "insurance" }), "?view=manage-agent&page=insurance")
  assert.equal(workspaceLocationSearch({ view: "manage-agent" }), "?view=manage-agent")
  assert.deepEqual(selectedWorkspaceLocation({ view: "manage-agent", agentPage: "knowledge" }), { view: "manage-agent", page: "knowledge" })
  assert.deepEqual(selectedWorkspaceLocation({ view: "manage-agent", agentPage: "transcripts" }), { view: "manage-agent" })
  assert.deepEqual(selectedWorkspaceLocation({ view: "analytics", agentPage: "insurance" }), { view: "analytics" })
})

test("only a selected Task or page view becomes a workspace URL", () => {
  assert.deepEqual(selectedWorkspaceLocation({ view: "engagement", task: { id: "task-1" } }), { view: "task", taskID: "task-1" })
  assert.deepEqual(selectedWorkspaceLocation({ view: "engagement" }), { view: "none" })
  assert.deepEqual(selectedWorkspaceLocation({ view: "analytics", task: { id: "task-1" } }), { view: "analytics" })
  assert.deepEqual(selectedWorkspaceLocation({ view: "none" }), { view: "none" })
})
