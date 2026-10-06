import assert from "node:assert/strict"
import test from "node:test"
import type { ConversationTimelineItem, Task } from "./api/generated/types.gen.ts"
import {
  callbackCompletionLabel,
  callHistoryPresentation,
  conversationDateLabel,
  linkedTaskLabel,
  openTaskCallbackNotice,
  presentTimeline,
  taskActivityDetail,
} from "./workspace-history.ts"

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

const followUp = {
  id: "follow-up", origin: "ABITA_AI", title: "Synthetic refill question", state: "OPEN",
  createdAt: "2026-09-08T13:00:00Z",
} as Task
const review = {
  id: "review", origin: "MISSED_CALL_RECOVERY", title: "Return missed call", state: "COMPLETED",
  createdAt: "2026-09-08T12:50:00Z", completedAt: "2026-09-08T13:56:00Z",
} as Task
const callbackCompletion = {
  id: "completion", type: "TASK", occurredAt: "2026-09-08T13:56:00.000Z", task: review,
  taskActivity: "TASK_AUTO_COMPLETED_CALLBACK_ATTEMPT",
  taskActivityDetails: { callId: "callback", callerSubject: "caller", callerEmail: "caller@example.test" },
} as ConversationTimelineItem
const unansweredCallback = {
  id: "callback-history", type: "CALL_HISTORY", occurredAt: "2026-09-08T13:55:00Z",
  entries: [{ id: "callback", type: "CALL", occurredAt: "2026-09-08T13:55:00Z", call: {
    id: "callback", type: "CALL", direction: "OUTBOUND", startedAt: "2026-09-08T13:55:00Z",
    durationSeconds: 0, locationId: "office", locationName: "Office", answeredByEmail: "",
    placedByEmail: "caller@example.test", transferReason: "", outcome: "UNANSWERED",
  } }],
} as ConversationTimelineItem
const at = (value: string) => value.slice(11, 16)

test("thread Task entries say which Task they describe and who placed the callback", () => {
  assert.equal(taskActivityDetail(callbackCompletion, followUp.id), "Missed call review completed after caller@example.test's callback attempt")
  assert.equal(taskActivityDetail({ ...callbackCompletion, taskActivityDetails: { callId: "callback" } }, followUp.id), "Missed call review completed after callback attempt")
  const created = { id: "created", type: "TASK", occurredAt: followUp.createdAt, task: followUp, taskActivity: "TASK_CREATED" } as ConversationTimelineItem
  assert.equal(taskActivityDetail(created, followUp.id), "This Task created by AI")
  assert.equal(taskActivityDetail(created), "Task created by AI")
  assert.equal(linkedTaskLabel(review, followUp.id), "Missed call review: Return missed call — Completed")
  assert.equal(linkedTaskLabel(followUp, followUp.id), "This Task: Synthetic refill question — Open")
  assert.equal(linkedTaskLabel(followUp), "Synthetic refill question — Open")
})

test("an unanswered outbound call reads as no answer instead of a missed call", () => {
  const result = callHistoryPresentation(unansweredCallback)
  assert.equal(result.title, "Outbound call")
  assert.deepEqual(result.details, ["No answer", "Office"])
})

test("a callback-attempt completion credits the caller only with its exact Activity evidence", () => {
  assert.equal(callbackCompletionLabel([callbackCompletion], review), "Missed call review completed after caller@example.test's callback attempt")
  assert.equal(callbackCompletionLabel([], review), undefined)
  assert.equal(callbackCompletionLabel([callbackCompletion], { ...review, completedAt: "2026-09-08T14:30:00Z" }), undefined)
  assert.equal(callbackCompletionLabel([callbackCompletion], { ...review, state: "OPEN" }), undefined)
})

test("an open Task names a later callback attempt only when that call is loaded", () => {
  assert.equal(
    openTaskCallbackNotice([unansweredCallback, callbackCompletion], followUp, at),
    "caller@example.test called this number at 13:55 (no answer). This Task is still open.",
  )
  assert.equal(openTaskCallbackNotice([callbackCompletion], followUp, at), undefined)
  assert.equal(openTaskCallbackNotice([unansweredCallback], { ...followUp, createdAt: "2026-09-08T14:00:00Z" }, at), undefined)
  assert.equal(openTaskCallbackNotice([unansweredCallback], { ...followUp, state: "COMPLETED" }, at), undefined)
  const reopened = { id: "reopened", type: "TASK", occurredAt: "2026-09-08T13:58:00Z", task: followUp, taskActivity: "TASK_REOPENED" } as ConversationTimelineItem
  assert.equal(openTaskCallbackNotice([unansweredCallback, reopened], followUp, at), undefined)
  const ringing = structuredClone(unansweredCallback)
  ringing.entries![0]!.call!.outcome = "RINGING"
  assert.equal(openTaskCallbackNotice([ringing], followUp, at), undefined)
  const unattributed = structuredClone(unansweredCallback)
  delete unattributed.entries![0]!.call!.placedByEmail
  assert.equal(
    openTaskCallbackNotice([unattributed], followUp, at),
    "This number was called at 13:55 (no answer). This Task is still open.",
  )
})

test("thread Task Activity names who acted and keeps automation automatic", () => {
  const activity = (taskActivity: ConversationTimelineItem["taskActivity"], taskActivityActor: ConversationTimelineItem["taskActivityActor"]) =>
    ({ id: "activity", type: "TASK", occurredAt: followUp.createdAt, task: followUp, taskActivity, taskActivityActor }) as ConversationTimelineItem
  const staff = { kind: "HUMAN", subject: "staff", email: "staff@example.test" } as const
  const service = { kind: "SERVICE", subject: "text-delivery" } as const
  assert.equal(taskActivityDetail(activity("TASK_COMPLETED", staff)), "Task completed by staff@example.test")
  assert.equal(taskActivityDetail(activity("TASK_REOPENED", staff), followUp.id), "This Task reopened by staff@example.test")
  assert.equal(taskActivityDetail(activity("TASK_REOPENED", service)), "Task reopened automatically")
  assert.equal(taskActivityDetail(activity("TASK_CREATED", { kind: "SERVICE", subject: "abita-ai" })), "Task created by AI")
  assert.equal(taskActivityDetail(activity("TASK_COMPLETED", undefined)), "Task completed")
  const automatic = { ...callbackCompletion, taskActivityActor: { kind: "SERVICE", subject: "work-recovery-resolution" } } as ConversationTimelineItem
  assert.equal(taskActivityDetail(automatic), "Missed call review completed after caller@example.test's callback attempt")
})
