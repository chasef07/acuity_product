import assert from "node:assert/strict"
import test from "node:test"

import {
  appendUniqueByID,
  newestFirst,
  oldestFirst,
} from "./workspace-ordering.ts"

test("history rows can be presented from oldest to newest", () => {
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

test("queues appear from newest to oldest", () => {
  const rows = [
    { id: "middle", occurredAt: "2026-08-16T10:00:00Z" },
    { id: "oldest", occurredAt: "2026-08-16T08:00:00Z" },
    { id: "newest", occurredAt: "2026-08-16T12:00:00Z" },
  ]

  assert.deepEqual(
    newestFirst(rows, (row) => row.occurredAt).map((row) => row.id),
    ["newest", "middle", "oldest"],
  )
  assert.deepEqual(rows.map((row) => row.id), ["middle", "oldest", "newest"])
})

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

test("appending pages keeps each row visible exactly once", () => {
  const firstPage = [{ id: "row-1" }, { id: "row-2" }]

  assert.deepEqual(
    appendUniqueByID(firstPage, [
      { id: "row-2" },
      { id: "row-3" },
      { id: "row-3" },
    ]),
    [{ id: "row-1" }, { id: "row-2" }, { id: "row-3" }],
  )
})
