import assert from "node:assert/strict"
import test from "node:test"

import {
  mergeFirstPage,
  newerFirst,
  newestFirst,
} from "./workspace-ordering.ts"

type Row = { id: string; updatedAt: string; groupMembers?: Array<{ id: string }> }

function row(id: string, minute: number, groupMembers?: string[]): Row {
  return {
    id,
    updatedAt: `2026-08-16T12:${String(minute).padStart(2, "0")}:00Z`,
    ...(groupMembers ? { groupMembers: groupMembers.map((member) => ({ id: member })) } : {}),
  }
}

const recent = newerFirst<Row>((item) => item.updatedAt)

test("a fresh first page replaces its range and keeps deeper loaded rows once", () => {
  const loaded = [row("a", 50), row("b", 40), row("c", 30), row("d", 20), row("e", 10)]
  const merged = mergeFirstPage({
    loaded,
    covered: 2,
    page: [row("new", 59), row("c", 55)],
    complete: false,
    sortsAfter: recent,
  })
  assert.deepEqual(merged.map((item) => item.id), ["new", "c", "a", "b", "d", "e"])
})

test("rows that left the first page range are dropped and grouped members are not duplicated", () => {
  const loaded = [row("gone", 50), row("old-lead", 45), row("deep", 10)]
  const merged = mergeFirstPage({
    loaded,
    covered: 2,
    page: [row("new-lead", 58, ["new-lead", "old-lead"]), row("kept", 40)],
    complete: false,
    sortsAfter: recent,
  })
  assert.deepEqual(merged.map((item) => item.id), ["new-lead", "kept", "deep"])
})

test("with nothing loaded beyond the first page the fresh page is the whole window", () => {
  const merged = mergeFirstPage({
    loaded: [row("a", 50), row("pushed", 40)],
    covered: 2,
    page: [row("new", 59), row("a", 50)],
    complete: false,
    sortsAfter: recent,
  })
  assert.deepEqual(merged.map((item) => item.id), ["new", "a"])
})

test("a complete first page is the whole window", () => {
  const merged = mergeFirstPage({
    loaded: [row("a", 50), row("deep", 10)],
    covered: 1,
    page: [row("a", 50)],
    complete: true,
    sortsAfter: recent,
  })
  assert.deepEqual(merged.map((item) => item.id), ["a"])
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
