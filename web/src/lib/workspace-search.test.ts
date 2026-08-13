import assert from "node:assert/strict"
import test from "node:test"

import { resolveWorkspaceSearch } from "./workspace-search.ts"

test("workspace search opens complete phone numbers and filters all other terms", () => {
  assert.deepEqual(resolveWorkspaceSearch("(727) 555-0199"), {
    kind: "phone",
    value: "+17275550199",
  })
  assert.deepEqual(resolveWorkspaceSearch("Jane Doe"), {
    kind: "tasks",
    value: "Jane Doe",
  })
  assert.deepEqual(resolveWorkspaceSearch("72755"), {
    kind: "tasks",
    value: "72755",
  })
})
