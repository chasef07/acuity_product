import assert from "node:assert/strict"
import test from "node:test"

import {
  newestFirst,
} from "./workspace-ordering.ts"

test("queues order timestamps with mixed fractional precision", () => {
  const rows = [
    { id: "whole-second", occurredAt: "2026-08-16T12:00:00Z" },
    { id: "later-fraction", occurredAt: "2026-08-16T12:00:00.12Z" },
    { id: "latest-fraction", occurredAt: "2026-08-16T12:00:00.123Z" },
  ]

  assert.deepEqual(
    newestFirst(rows, (row) => row.occurredAt).map((row) => row.id),
    ["latest-fraction", "later-fraction", "whole-second"],
  )
})
