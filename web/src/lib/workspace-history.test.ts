import assert from "node:assert/strict"
import test from "node:test"
import type { ConversationTimelineItem } from "./api/generated/types.gen.ts"
import { callHistoryPresentation, conversationDateLabel, presentTimeline } from "./workspace-history.ts"

const base = { id: "item", occurredAt: "2026-08-13T12:00:00Z" } as const

test("history presents activity oldest to newest without mutating the API page", () => {
  const items = [
    { ...base, id: "newest", occurredAt: "2026-08-16T12:00:00Z" },
    { ...base, id: "oldest", occurredAt: "2026-08-14T08:00:00Z" },
    { ...base, id: "middle", occurredAt: "2026-08-15T10:00:00Z" },
  ] as ConversationTimelineItem[]

  assert.deepEqual(presentTimeline(items).map((item) => item.id), [
    "oldest",
    "middle",
    "newest",
  ])
  assert.deepEqual(items.map((item) => item.id), ["newest", "oldest", "middle"])
})

test("conversation date labels keep recent boundaries subtle and readable", () => {
  const now = new Date("2026-08-16T12:00:00")

  assert.equal(conversationDateLabel("2026-08-16T08:00:00", now), "Today")
  assert.equal(conversationDateLabel("2026-08-15T08:00:00", now), "Yesterday")
  assert.match(
    conversationDateLabel("2026-08-13T08:00:00", now),
    /Aug 13/,
  )
})


test("overlapping history pages replace a call with its latest complete evidence", () => {
  const earlier = { ...base, type: "CALL_HISTORY", entries: [] } as ConversationTimelineItem
  const updated = { ...earlier, entries: [{ ...base, type: "TASK" }] } as ConversationTimelineItem
  assert.deepEqual(presentTimeline([earlier, updated]), [updated])
})

test("call summary preserves booking and failed transfer without claiming staff connection", () => {
  const history = { ...base, type: "CALL_HISTORY", entries: [
    { ...base, type: "AI_INTERACTION", aiInteraction: { status: "ESCALATED", appointmentOutcome: "BOOKING", locationName: "Office" } },
    { ...base, type: "CALL", call: { direction: "INBOUND", outcome: "MISSED", locationName: "Office" } },
    { ...base, type: "TASK", task: { id: "task", title: "Review instructions", state: "OPEN" } },
  ] } as ConversationTimelineItem
  const result = callHistoryPresentation(history)
  assert.equal(result.title, "AI call")
  assert.deepEqual(result.details, ["Appointment booked", "Staff not reached", "Office"])
  assert.equal(result.tasks[0]?.title, "Review instructions")
})

test("AI transfer intent and no appointment action do not imply resolution", () => {
  const history = { ...base, type: "CALL_HISTORY", entries: [
    { ...base, type: "AI_INTERACTION", aiInteraction: { status: "ESCALATED", appointmentOutcome: "INDETERMINATE", locationName: "Office" } },
  ] } as ConversationTimelineItem
  assert.deepEqual(callHistoryPresentation(history).details, ["Transfer requested · Staff outcome unknown", "Office"])
  history.entries![0]!.aiInteraction!.status = "COMPLETED"
  assert.deepEqual(callHistoryPresentation(history).details, ["No appointment action recorded", "Office"])
})
