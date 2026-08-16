import assert from "node:assert/strict"
import test from "node:test"

import { oldestFirst } from "./workspace-ordering.ts"

test("attention rows appear from oldest to newest", () => {
  const rows = [
    { id: "newest", occurredAt: "2026-08-16T12:00:00Z" },
    { id: "oldest", occurredAt: "2026-08-16T08:00:00Z" },
    { id: "middle", occurredAt: "2026-08-16T10:00:00Z" },
  ]

  assert.deepEqual(
    oldestFirst(rows, (row) => row.occurredAt).map((row) => row.id),
    ["oldest", "middle", "newest"],
  )
  assert.deepEqual(rows.map((row) => row.id), ["newest", "oldest", "middle"])
})
