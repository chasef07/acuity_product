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

test("a fresh first page merges into loaded rows", () => {
  const cases = [
    {
      name: "replaces its range and keeps deeper rows once",
      loaded: [row("a", 50), row("b", 40), row("c", 30), row("d", 20), row("e", 10)],
      covered: 2,
      page: [row("new", 59), row("c", 55)],
      pageCursor: "page",
      want: ["new", "c", "a", "b", "d", "e"],
      cursor: "loaded",
    },
    {
      name: "drops rows that left its range and members of a new group",
      loaded: [row("gone", 50), row("old-lead", 45), row("deep", 10)],
      covered: 2,
      page: [row("new-lead", 58, ["new-lead", "old-lead"]), row("kept", 40)],
      pageCursor: "page",
      want: ["new-lead", "kept", "deep"],
      cursor: "loaded",
    },
    {
      name: "drops deeper rows that moved to the other window",
      loaded: [row("a", 50), row("done", 20), row("group", 10, ["group", "done-member"])],
      covered: 1,
      page: [row("a", 50)],
      pageCursor: "page",
      moved: [row("done", 5), row("done-member", 5)],
      want: ["a"],
      cursor: "loaded",
    },
    {
      name: "is the whole window when nothing deeper is loaded",
      loaded: [row("a", 50), row("pushed", 40)],
      covered: 2,
      page: [row("new", 59), row("a", 50)],
      pageCursor: "page",
      want: ["new", "a"],
      cursor: "page",
    },
    {
      name: "is the whole window when complete",
      loaded: [row("a", 50), row("deep", 10)],
      covered: 1,
      page: [row("a", 50)],
      pageCursor: "",
      want: ["a"],
      cursor: "",
    },
  ]
  for (const item of cases) {
    const merged = mergeFirstPage({ ...item, loadedCursor: "loaded", sortsAfter: recent })
    assert.deepEqual(merged.items.map((entry) => entry.id), item.want, item.name)
    assert.equal(merged.nextCursor, item.cursor, item.name)
  }
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
