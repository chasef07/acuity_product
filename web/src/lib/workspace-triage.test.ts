import assert from "node:assert/strict"
import test from "node:test"

import {
  filterTasksByCategory,
  outboundLocationPreferenceKey,
  recoveryGroupKey,
  resolveOutboundLocation,
} from "./workspace-triage.ts"

test("Task categories filter only the supplied Task rows", () => {
  const tasks = [
    { id: "billing", category: "billing" as const },
    { id: "medication", category: "medication" as const },
  ]

  assert.deepEqual(filterTasksByCategory(tasks, "all"), tasks)
  assert.deepEqual(filterTasksByCategory(tasks, "billing"), [tasks[0]])
})

test("outbound preferences are isolated by User and Practice", () => {
  assert.notEqual(
    outboundLocationPreferenceKey("user-1", "practice-1"),
    outboundLocationPreferenceKey("user-2", "practice-1"),
  )
  assert.notEqual(
    outboundLocationPreferenceKey("user-1", "practice-1"),
    outboundLocationPreferenceKey("user-1", "practice-2"),
  )
})

test("outbound Location resolution rejects stale choices and bypasses one Location", () => {
  const locations = [{ id: "location-1" }, { id: "location-2" }]

  assert.equal(resolveOutboundLocation(locations, "location-2"), "location-2")
  assert.equal(resolveOutboundLocation(locations, "location-3"), "")
  assert.equal(resolveOutboundLocation([{ id: "location-1" }], ""), "location-1")
  assert.equal(resolveOutboundLocation([{ id: "location-1" }], "", 2), "")
})

test("recovery groups distinguish the same phone at different Locations", () => {
  assert.notEqual(
    recoveryGroupKey("location-1", "+17275550199"),
    recoveryGroupKey("location-2", "+17275550199"),
  )
})
