import assert from "node:assert/strict"
import test from "node:test"

import { workspaceScopeForCall } from "./workspace-scope.ts"

const authority = {
  practices: [
    { id: "practice-a", locations: [{ id: "location-a" }] },
    { id: "practice-b", locations: [{ id: "location-b" }] },
  ],
}

test("a connected Call selects its authorized Practice and Location", () => {
  assert.deepEqual(
    workspaceScopeForCall(authority, "practice-a", "location-a", {
      practiceId: "practice-b",
      locationId: "location-b",
    }),
    { practiceID: "practice-b", locationID: "location-b" },
  )
})

test("an all-office scope already contains Calls for that Practice", () => {
  assert.equal(
    workspaceScopeForCall(authority, "practice-b", "", {
      practiceId: "practice-b",
      locationId: "location-b",
    }),
    undefined,
  )
})

test("a location-scoped workspace follows a Call in the same Practice", () => {
  const multiLocationAuthority = {
    practices: [
      {
        id: "practice-a",
        locations: [{ id: "location-a" }, { id: "location-a-2" }],
      },
    ],
  }
  assert.deepEqual(
    workspaceScopeForCall(
      multiLocationAuthority,
      "practice-a",
      "location-a",
      { practiceId: "practice-a", locationId: "location-a-2" },
    ),
    { practiceID: "practice-a", locationID: "location-a-2" },
  )
})

test("a Call cannot move the workspace outside current authority", () => {
  assert.equal(
    workspaceScopeForCall(authority, "practice-a", "location-a", {
      practiceId: "practice-c",
      locationId: "location-c",
    }),
    undefined,
  )
})
