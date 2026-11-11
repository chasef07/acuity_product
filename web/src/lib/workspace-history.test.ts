import assert from "node:assert/strict"
import test from "node:test"
import type { ConversationTimelineItem, Task } from "./api/generated/types.gen.ts"
import { formatClockTime } from "./format-time.ts"
import {
  callbackCompletionLabel,
  callHistoryPresentation,
  linkedTaskLabel,
  linkedTaskStatus,
  openTaskCallbackNotice,
  presentTimeline,
  taskActivityDetail,
} from "./workspace-history.ts"

const base = { id: "item", occurredAt: "2026-08-13T12:00:00Z" } as const

test("history presents activity oldest to newest, keeps the latest copy of an overlapping item, and does not mutate the API page", () => {
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

  const earlier = { ...base, type: "CALL_HISTORY", entries: [] } as ConversationTimelineItem
  const updated = { ...earlier, entries: [{ ...base, type: "TASK" }] } as ConversationTimelineItem
  assert.deepEqual(presentTimeline([earlier, updated]), [updated])
})

const office = { locationId: "office", locationName: "Office" } as const
const aiEntry = (aiInteraction: Record<string, unknown>) =>
  ({ ...base, id: "ai", type: "AI_INTERACTION", aiInteraction: { id: "ai", ...office, status: "COMPLETED", appointmentOutcome: "INDETERMINATE", ...aiInteraction } }) as ConversationTimelineItem
const callEntry = (call: Record<string, unknown>) =>
  ({ ...base, id: String(call.id ?? "call"), type: "CALL", call: { id: "call", type: "CALL", direction: "INBOUND", ...office, durationSeconds: 0, answeredByEmail: "", transferReason: "", outcome: "MISSED", ...call } }) as ConversationTimelineItem
const taskEntry = (task: Record<string, unknown>) =>
  ({ ...base, id: `created-${String(task.id ?? "task")}`, type: "TASK", taskActivity: "TASK_CREATED", task: { id: "task", origin: "ABITA_AI", title: "Review instructions", state: "OPEN", ...task } }) as ConversationTimelineItem
const lineOf = (...entries: ConversationTimelineItem[]) =>
  callHistoryPresentation({ ...base, type: "CALL_HISTORY", entries } as ConversationTimelineItem)!
const outcomeOf = (...entries: ConversationTimelineItem[]) => lineOf(...entries).outcome

test("each call history root reads as one outcome", () => {
  const cases: [string, ConversationTimelineItem[], ReturnType<typeof outcomeOf>][] = [
    ["AI finished the call alone and nothing says the caller was helped", [aiEntry({})],
      { kind: "handled-by-ai", label: "Handled by AI", tone: "muted" }],
    ["AI finished the call alone and the stored call label says resolved", [aiEntry({ resolved: true })],
      { kind: "resolved-by-ai", label: "Resolved by AI", tone: "success" }],
    ["AI finished and left a Task", [aiEntry({}), taskEntry({})],
      { kind: "message-taken", label: "Message taken", tone: "muted" }],
    ["AI left a Task on a call labelled resolved", [aiEntry({ resolved: true }), taskEntry({})],
      { kind: "message-taken", label: "Message taken", tone: "muted" }],
    ["a call labelled resolved whose booking check is still open", [aiEntry({ resolved: true }), taskEntry({ origin: "APPOINTMENT_REVIEW", title: "Check appointment booking" })],
      { kind: "needs-review", label: "Appointment change needs review", tone: "warning" }],
    ["an unlabelled call whose booking check is still open", [aiEntry({}), taskEntry({ origin: "APPOINTMENT_REVIEW" })],
      { kind: "needs-review", label: "Appointment change needs review", tone: "warning" }],
    ["a call labelled resolved whose booking check staff closed", [aiEntry({ resolved: true }), taskEntry({ origin: "APPOINTMENT_REVIEW", state: "COMPLETED" })],
      { kind: "resolved-by-ai", label: "Resolved by AI", tone: "success" }],
    ["a call labelled resolved with an open staff follow-up", [aiEntry({ resolved: true }), taskEntry({ origin: "HUMAN_CALL_FOLLOW_UP" })],
      { kind: "handled-by-ai", label: "Handled by AI", tone: "muted" }],
    ["a call labelled resolved with an open text follow-up", [aiEntry({ resolved: true }), taskEntry({ origin: "STAFF_MESSAGE_FOLLOW_UP" })],
      { kind: "handled-by-ai", label: "Handled by AI", tone: "muted" }],
    ["a call labelled resolved whose staff follow-up is closed", [aiEntry({ resolved: true }), taskEntry({ origin: "HUMAN_CALL_FOLLOW_UP", state: "COMPLETED" })],
      { kind: "resolved-by-ai", label: "Resolved by AI", tone: "success" }],
    ["a call labelled resolved whose AI Task staff closed", [aiEntry({ resolved: true }), taskEntry({ state: "COMPLETED" })],
      { kind: "resolved-by-ai", label: "Resolved by AI", tone: "success" }],
    ["a call labelled resolved with one closed and one open Task", [aiEntry({ resolved: true }), taskEntry({ state: "COMPLETED" }), taskEntry({ id: "second", origin: "HUMAN_CALL_FOLLOW_UP" })],
      { kind: "handled-by-ai", label: "Handled by AI", tone: "muted" }],
    ["an unlabelled call whose AI Task staff closed", [aiEntry({}), taskEntry({ state: "COMPLETED" })],
      { kind: "message-taken", label: "Message taken", tone: "muted" }],
    ["lines busy, AI took a message", [aiEntry({ status: "ESCALATED" }), taskEntry({})],
      { kind: "message-taken", label: "Message taken", tone: "muted" }],
    ["AI booked and staff verify the visit", [aiEntry({ appointmentOutcome: "BOOKING" }), taskEntry({ origin: "APPOINTMENT_REVIEW" })],
      { kind: "appointment-by-ai", label: "Booked by AI", tone: "success" }],
    ["AI rescheduled", [aiEntry({ appointmentOutcome: "RESCHEDULE" })],
      { kind: "appointment-by-ai", label: "Rescheduled by AI", tone: "success" }],
    ["AI cancelled", [aiEntry({ appointmentOutcome: "CANCELLATION" })],
      { kind: "appointment-by-ai", label: "Cancelled by AI", tone: "success" }],
    ["AI booked on a call that then failed", [aiEntry({ status: "FAILED", appointmentOutcome: "BOOKING" })],
      { kind: "failed", label: "AI call failed", tone: "destructive" }],
    ["transferred and answered", [aiEntry({ status: "ESCALATED" }), callEntry({ outcome: "RESOLVED", answeredByEmail: "dana@example.test", durationSeconds: 362 })],
      { kind: "answered", label: "dana@example.test answered", tone: "success" }],
    ["answered by a staff member the reader cannot resolve", [aiEntry({ status: "ESCALATED" }), callEntry({ outcome: "CONNECTED" })],
      { kind: "answered", label: "Staff answered", tone: "success" }],
    ["transferred, nobody answered, no voicemail", [aiEntry({ status: "ESCALATED" }), callEntry({ outcome: "MISSED" })],
      { kind: "missed", label: "Missed", tone: "destructive" }],
    ["transferred, nobody answered, voicemail left", [aiEntry({ status: "ESCALATED" }), callEntry({ outcome: "VOICEMAIL", voicemailDurationSeconds: 22 })],
      { kind: "voicemail", label: "Voicemail", tone: "warning" }],
    ["transfer still ringing", [aiEntry({ status: "ESCALATED" }), callEntry({ outcome: "RINGING" })],
      { kind: "in-progress", label: "Ringing", tone: "muted" }],
    ["AI call still running", [aiEntry({ status: "IN_PROGRESS" })],
      { kind: "in-progress", label: "AI call in progress", tone: "muted" }],
    ["AI call failed", [aiEntry({ status: "FAILED" })],
      { kind: "failed", label: "AI call failed", tone: "destructive" }],
    ["outbound answered", [callEntry({ direction: "OUTBOUND", outcome: "NEEDS_DISPOSITION", durationSeconds: 192 })],
      { kind: "outbound-answered", label: "Answered · 3m 12s", tone: "success" }],
    ["outbound not answered", [callEntry({ direction: "OUTBOUND", outcome: "UNANSWERED" })],
      { kind: "no-answer", label: "No answer", tone: "warning" }],
    ["outbound still dialing", [callEntry({ direction: "OUTBOUND", outcome: "PREPARING" })],
      { kind: "in-progress", label: "Preparing call", tone: "muted" }],
    ["transfer requested with no staff call on record", [aiEntry({ status: "ESCALATED" })],
      { kind: "unknown", label: "Transfer requested, staff outcome not recorded", tone: "muted" }],
    ["an appointment change the AI could not finish", [aiEntry({ appointmentOutcome: "PARTIAL" })],
      { kind: "needs-review", label: "Appointment change needs review", tone: "warning" }],
    ["an unfinished appointment change on a call labelled resolved", [aiEntry({ appointmentOutcome: "PARTIAL", resolved: true })],
      { kind: "needs-review", label: "Appointment change needs review", tone: "warning" }],
    ["an unfinished appointment change that staff then answered", [aiEntry({ status: "ESCALATED", appointmentOutcome: "PARTIAL" }), callEntry({ outcome: "RESOLVED", answeredByEmail: "dana@example.test" })],
      { kind: "needs-review", label: "Appointment change needs review", tone: "warning" }],
    ["an unfinished appointment change that staff missed", [aiEntry({ status: "ESCALATED", appointmentOutcome: "PARTIAL" }), callEntry({ outcome: "MISSED" })],
      { kind: "missed", label: "Missed", tone: "destructive" }],
    ["a state an outbound call should never report", [callEntry({ direction: "OUTBOUND", outcome: "VOICEMAIL" })],
      { kind: "unknown", label: "Voicemail", tone: "muted" }],
  ]
  for (const [name, entries, expected] of cases) assert.deepEqual(outcomeOf(...entries), expected, name)
})

test("an AI call with no transfer and no appointment action does not read as success", () => {
  for (const status of ["COMPLETED", "ESCALATED", "IN_PROGRESS", "FAILED"]) {
    for (const summary of [undefined, "Caller hung up before asking anything."]) {
      const result = outcomeOf(aiEntry({ status, summary }))
      assert.notEqual(result.tone, "success", `${status} ${summary ?? "no summary"}`)
      assert.notEqual(result.label, "Resolved by AI", `${status} ${summary ?? "no summary"}`)
    }
  }
  for (const origin of ["HUMAN_CALL_FOLLOW_UP", "ABITA_AI", "STAFF_MESSAGE_FOLLOW_UP", "VOICEMAIL_RECOVERY", "MISSED_CALL_RECOVERY", "APPOINTMENT_REVIEW", "INBOUND_MESSAGE_REVIEW"]) {
    const result = outcomeOf(aiEntry({ resolved: true }), taskEntry({ origin }))
    assert.notEqual(result.tone, "success", `open ${origin} Task`)
    assert.notEqual(result.label, "Resolved by AI", `open ${origin} Task`)
  }
  assert.equal(outcomeOf(aiEntry({ status: "ESCALATED", resolved: true })).tone, "muted")
  assert.equal(outcomeOf(aiEntry({ status: "FAILED", resolved: true })).tone, "destructive")
})

test("every call state has a line for both directions and never a blank label", () => {
  const states = ["PREPARING", "RINGING", "CONNECTING", "CONNECTED", "VOICEMAIL_GREETING", "VOICEMAIL_RECORDING", "UNANSWERED", "VOICEMAIL", "MISSED", "NEEDS_DISPOSITION", "RESOLVED", "FOLLOW_UP_REQUIRED"]
  for (const direction of ["INBOUND", "OUTBOUND"]) {
    for (const outcome of states) {
      const line = lineOf(callEntry({ direction, outcome }))
      assert.ok(line.outcome.label, `${direction} ${outcome}`)
      assert.ok(line.steps.length > 0, `${direction} ${outcome} steps`)
    }
  }
  assert.equal(outcomeOf(callEntry({ outcome: "VOICEMAIL_GREETING" })).label, "Voicemail greeting")
  assert.equal(outcomeOf(callEntry({ outcome: "VOICEMAIL_RECORDING" })).label, "Recording voicemail")
  assert.equal(outcomeOf(callEntry({ outcome: "UNANSWERED" })).kind, "missed")
  assert.equal(outcomeOf(callEntry({ direction: "OUTBOUND", outcome: "MISSED" })).kind, "no-answer")
  assert.deepEqual(outcomeOf(callEntry({ outcome: "ARCHIVED" })), { kind: "unknown", label: "Outcome unknown", tone: "muted" })
  assert.deepEqual(outcomeOf(aiEntry({ status: "ARCHIVED" })), { kind: "unknown", label: "Outcome unknown", tone: "muted" })
})

test("when several staff legs share a call, an answer outranks a voicemail and a voicemail outranks a miss", () => {
  const missed = callEntry({ id: "first", outcome: "MISSED" })
  const voicemail = callEntry({ id: "second", outcome: "VOICEMAIL" })
  const answered = callEntry({ id: "third", outcome: "RESOLVED", answeredByEmail: "dana@example.test" })
  assert.equal(outcomeOf(missed, voicemail).kind, "voicemail")
  assert.equal(outcomeOf(answered, missed, voicemail).label, "dana@example.test answered")
})

test("several staff legs keep the answer as the outcome and list every leg in time order, each named by who and when", () => {
  const answeredAt = "2026-08-13T12:01:00Z"
  const missedAt = "2026-08-13T12:09:00Z"
  const voicemailAt = "2026-08-13T12:12:00Z"
  const line = lineOf(
    aiEntry({ status: "ESCALATED" }),
    callEntry({ id: "voicemail", outcome: "VOICEMAIL", startedAt: voicemailAt, voicemailDurationSeconds: 38 }),
    callEntry({ id: "missed", outcome: "MISSED", startedAt: missedAt }),
    callEntry({ id: "answered", outcome: "RESOLVED", startedAt: answeredAt, answeredByEmail: "maria@example.test", durationSeconds: 60 }),
  )
  assert.deepEqual(line.outcome, { kind: "answered", label: "maria@example.test answered", tone: "success" })
  assert.deepEqual(line.steps, [
    { tone: "muted", text: "AI answered and transferred" },
    { tone: "success", text: `${formatClockTime(answeredAt)} · maria@example.test answered, 1m 0s` },
    { tone: "muted", text: "Resolved on call" },
    { tone: "destructive", text: `${formatClockTime(missedAt)} · Nobody answered, no voicemail` },
    { tone: "warning", text: `${formatClockTime(voicemailAt)} · Nobody answered, caller left a voicemail, 0:38` },
  ])
  assert.deepEqual(line.sources, [
    { kind: "ai", id: "ai", label: "View AI call" },
    { kind: "call", id: "answered", label: `View call · maria@example.test · ${formatClockTime(answeredAt)}` },
    { kind: "call", id: "missed", label: `View call · ${formatClockTime(missedAt)}` },
    { kind: "voicemail", id: "voicemail", label: `View voicemail · ${formatClockTime(voicemailAt)}` },
  ])
  const callbacks = lineOf(
    callEntry({ id: "second", direction: "OUTBOUND", outcome: "UNANSWERED", startedAt: missedAt, placedByEmail: "omar@example.test" }),
    callEntry({ id: "first", direction: "OUTBOUND", outcome: "UNANSWERED", startedAt: answeredAt, placedByEmail: "maria@example.test" }),
  )
  assert.deepEqual(callbacks.sources.map((source) => source.label), [
    `View call · maria@example.test · ${formatClockTime(answeredAt)}`,
    `View call · omar@example.test · ${formatClockTime(missedAt)}`,
  ])
})

test("a lone call and a lone AI call read the same as their call history", () => {
  const call = callEntry({ outcome: "VOICEMAIL", voicemailDurationSeconds: 38 })
  assert.deepEqual(callHistoryPresentation(call), lineOf(call))
  const ai = aiEntry({ summary: "Caller asked about Saturday hours." })
  assert.deepEqual(callHistoryPresentation(ai), lineOf(ai))
  assert.equal(callHistoryPresentation({ ...base, type: "CALL_HISTORY", entries: [taskEntry({})] } as ConversationTimelineItem), undefined)
  assert.equal(callHistoryPresentation({ ...base, type: "MESSAGE" } as ConversationTimelineItem), undefined)
})

test("a line says who called and what it was about", () => {
  const summary = "Caller asked which drops to stop before surgery. The AI could not answer."
  const heading = ({ direction, lead, about }: ReturnType<typeof lineOf>) => ({ direction, lead, about })
  assert.deepEqual(
    heading(lineOf(aiEntry({ status: "ESCALATED", summary }), callEntry({ transferReason: "Clinical question" }))),
    { direction: "INBOUND", lead: "Called in", about: "Clinical question" },
  )
  assert.equal(lineOf(aiEntry({ summary })).about, "Caller asked which drops to stop before surgery")
  assert.equal(lineOf(aiEntry({ summary: "Caller asked about hours" })).about, "Caller asked about hours")
  assert.equal(lineOf(aiEntry({})).about, "")
  assert.equal(lineOf(aiEntry({ appointmentOutcome: "RESCHEDULE" })).about, "Appointment rescheduled")
  assert.equal(lineOf(aiEntry({ appointmentOutcome: "RESCHEDULE", summary })).about, "Caller asked which drops to stop before surgery")
  assert.equal(lineOf(aiEntry({ summary: "   " })).about, "")
  const long = lineOf(aiEntry({ summary: `Caller ${"asked again and ".repeat(10)}hung up.` })).about
  assert.ok(long.length <= 81 && long.endsWith("…") && !long.endsWith(" …"), long)
  assert.equal(lineOf(aiEntry({ summary: "Caller asked for Dr. Shah's hours. The AI gave them." })).about, "Caller asked for Dr. Shah's hours")
  const about = (summary: string) => lineOf(aiEntry({ summary })).about
  assert.equal(about("Caller wants 3 p.m. Thursday. The AI booked it."), "Caller wants 3 p.m. Thursday")
  assert.equal(about("Caller can come at 9 a.m. or 3 p.m."), "Caller can come at 9 a.m. or 3 p.m.")
  assert.equal(about("Is Dr. Shah in at 9 a.m.? The AI said yes."), "Is Dr. Shah in at 9 a.m.")
  assert.equal(about("Caller owes $4.50 for lens wipes. Paid by card."), "Caller owes $4.50 for lens wipes")
  assert.equal(about("Caller asked about the U.S. Open week closure! Told closed."), "Caller asked about the U.S. Open week closure")
  assert.equal(about("Caller asked about hours. then hung up"), "Caller asked about hours. then hung up")
  const abbreviated = about(`Caller wants 3 p.m. ${"on some weekday ".repeat(8)}soon. Booked.`)
  assert.ok(abbreviated.length <= 81 && abbreviated.endsWith("…") && abbreviated.startsWith("Caller wants 3 p.m. on some"), abbreviated)

  const callback = { direction: "OUTBOUND", outcome: "UNANSWERED", placedByEmail: "caller@example.test" }
  assert.deepEqual(
    heading(lineOf(callEntry({ ...callback, taskId: "task", taskTitle: "Reschedule post-op visit" }))),
    { direction: "OUTBOUND", lead: "caller@example.test called", about: 'about "Reschedule post-op visit"' },
  )
  assert.equal(lineOf(callEntry(callback)).about, "")
  assert.equal(lineOf(callEntry({ direction: "OUTBOUND", outcome: "UNANSWERED" })).lead, "Outbound call")
})

test("an expanded line lists what the AI did, who answered and for how long, and the Tasks it left", () => {
  const task = taskEntry({ id: "follow-up" })
  const line = lineOf(
    aiEntry({ status: "ESCALATED", appointmentOutcome: "BOOKING", summary: "Caller booked a visit and asked about insurance." }),
    callEntry({ outcome: "FOLLOW_UP_REQUIRED", answeredByEmail: "dana@example.test", durationSeconds: 362, transferReason: "Insurance question" }),
    task, task,
  )
  assert.deepEqual(line.steps, [
    { tone: "muted", text: "AI answered and transferred: Insurance question" },
    { tone: "success", text: "Appointment booked" },
    { tone: "muted", text: "Caller booked a visit and asked about insurance." },
    { tone: "success", text: "dana@example.test answered, 6m 2s" },
    { tone: "muted", text: "Follow-up needed" },
  ])
  assert.deepEqual(line.tasks.map((value) => value.id), ["follow-up"])
  assert.deepEqual(line.sources, [{ kind: "ai", id: "ai", label: "View AI call" }, { kind: "call", id: "call", label: "View call" }])
  assert.deepEqual(line.locations, [{ id: "office", name: "Office" }])
  assert.equal(line.voicemail, undefined)

  const voicemail = lineOf(aiEntry({ status: "ESCALATED" }), callEntry({ outcome: "VOICEMAIL", voicemailDurationSeconds: 22 }))
  assert.deepEqual(voicemail.steps, [
    { tone: "muted", text: "AI answered and transferred" },
    { tone: "warning", text: "Nobody answered, caller left a voicemail, 0:22" },
  ])
  assert.deepEqual(voicemail.voicemail, { callID: "call", seconds: 22 })
  assert.deepEqual(voicemail.sources, [{ kind: "ai", id: "ai", label: "View AI call" }, { kind: "voicemail", id: "call", label: "View voicemail" }])
  assert.deepEqual(lineOf(callEntry({ outcome: "VOICEMAIL" })).voicemail, { callID: "call", seconds: 0 })

  assert.deepEqual(lineOf(aiEntry({ status: "ESCALATED" }), callEntry({ outcome: "MISSED" })).steps.at(-1),
    { tone: "destructive", text: "Nobody answered, no voicemail" })
  assert.deepEqual(lineOf(aiEntry({ appointmentOutcome: "PARTIAL" })).steps, [
    { tone: "muted", text: "AI answered" },
    { tone: "warning", text: "Appointment change needs review" },
  ])
  assert.deepEqual(lineOf(aiEntry({ status: "ESCALATED", summary: "Caller asked for a refill." }), taskEntry({ title: "Refill request" })).steps, [
    { tone: "muted", text: 'AI took a message: "Refill request"' },
    { tone: "muted", text: "Caller asked for a refill." },
  ])
  assert.deepEqual(lineOf(aiEntry({}), taskEntry({ title: "Refill request" })).steps, [
    { tone: "muted", text: 'AI took a message: "Refill request"' },
  ])
  assert.deepEqual(lineOf(aiEntry({ status: "ESCALATED" })).steps, [
    { tone: "muted", text: "Transfer requested, staff outcome not recorded" },
  ])
  assert.deepEqual(lineOf(aiEntry({ status: "ESCALATED" }), taskEntry({ origin: "MISSED_CALL_RECOVERY" }), callEntry({ outcome: "MISSED" })).steps, [
    { tone: "muted", text: "AI answered and transferred" },
    { tone: "destructive", text: "Nobody answered, no voicemail" },
  ])
  assert.deepEqual(lineOf(aiEntry({ status: "ESCALATED" }), taskEntry({ origin: "MISSED_CALL_RECOVERY" })).steps, [
    { tone: "muted", text: "Transfer requested, staff outcome not recorded" },
  ])
  assert.deepEqual(lineOf(callEntry({ direction: "OUTBOUND", outcome: "RESOLVED", durationSeconds: 192 })).steps, [
    { tone: "success", text: "Answered, 3m 12s" },
    { tone: "muted", text: "Resolved on call" },
  ])
  assert.deepEqual(lineOf(callEntry({ direction: "OUTBOUND", outcome: "UNANSWERED" })).steps, [{ tone: "warning", text: "No answer" }])
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
  assert.equal(linkedTaskLabel(review, followUp.id), "Missed call review: Return missed call")
  assert.equal(linkedTaskLabel(followUp, followUp.id), "This Task: Synthetic refill question")
  assert.equal(linkedTaskLabel(followUp), "Synthetic refill question")
  assert.equal(linkedTaskStatus(followUp), "Open")
  assert.equal(linkedTaskStatus(review), "Completed")
  assert.equal(linkedTaskStatus({ ...review, closeReason: "UNREACHABLE", completedBy: { kind: "HUMAN", subject: "staff", email: "staff@example.test" } }), "Couldn't reach, by staff@example.test")
  assert.equal(linkedTaskStatus({ ...review, closeReason: "EXPIRED", completedBy: { kind: "SERVICE", subject: "work-expiry" } }), "Expired, automatically")
})

test("a callback-attempt completion credits the caller from the Task itself", () => {
  const withCaller = { ...review, callbackCompletion: { callerEmail: "caller@example.test" } }
  assert.equal(callbackCompletionLabel(withCaller), "Missed call review completed after caller@example.test's callback attempt")
  assert.equal(callbackCompletionLabel({ ...review, callbackCompletion: {} }), "Missed call review completed after callback attempt")
  assert.equal(callbackCompletionLabel(review), undefined)
  assert.equal(callbackCompletionLabel({ ...withCaller, state: "OPEN" }), undefined)
})

test("an open Task names a later callback attempt only when that call is loaded", () => {
  assert.equal(
    openTaskCallbackNotice([unansweredCallback, callbackCompletion], followUp, at),
    "caller@example.test called this number (no answer) · 13:55. This Task is still open.",
  )
  assert.equal(openTaskCallbackNotice([callbackCompletion], followUp, at), undefined)
  assert.equal(openTaskCallbackNotice([unansweredCallback], { ...followUp, createdAt: "2026-09-08T14:00:00Z" }, at), undefined)
  assert.equal(openTaskCallbackNotice([unansweredCallback], { ...followUp, state: "COMPLETED" }, at), undefined)
  const reopened = { id: "reopened", type: "TASK", occurredAt: "2026-09-08T13:58:00Z", task: followUp, taskActivity: "TASK_REOPENED" } as ConversationTimelineItem
  assert.equal(openTaskCallbackNotice([unansweredCallback, reopened], followUp, at), undefined)
  const ringing = structuredClone(unansweredCallback)
  ringing.entries![0]!.call!.outcome = "RINGING"
  assert.equal(openTaskCallbackNotice([ringing], followUp, at), undefined)
  const connected = structuredClone(unansweredCallback)
  connected.entries![0]!.call!.outcome = "NEEDS_DISPOSITION"
  assert.equal(
    openTaskCallbackNotice([connected], followUp, at),
    "caller@example.test called this number (connected) · 13:55. This Task is still open.",
  )
  const unattributed = structuredClone(unansweredCallback)
  delete unattributed.entries![0]!.call!.placedByEmail
  assert.equal(
    openTaskCallbackNotice([unattributed], followUp, at),
    "This number was called (no answer) · 13:55. This Task is still open.",
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
  const closedAs = (closeReason: string) => taskActivityDetail({ ...activity("TASK_COMPLETED", staff), taskActivityDetails: { closeReason } })
  assert.equal(closedAs("DONE"), "Task completed by staff@example.test")
  const anonymous = { kind: "HUMAN", subject: "staff-subject" } as const
  assert.equal(taskActivityDetail(activity("TASK_COMPLETED", anonymous)), "Task completed by the team")
  assert.equal(taskActivityDetail({ ...activity("TASK_COMPLETED", anonymous), taskActivityDetails: { closeReason: "UNREACHABLE" } }), "Task closed as Couldn't reach by the team")
  assert.equal(closedAs("NOT_NEEDED"), "Task closed as Not needed by staff@example.test")
  assert.equal(closedAs("UNREACHABLE"), "Task closed as Couldn't reach by staff@example.test")
  assert.equal(taskActivityDetail({ ...activity("TASK_COMPLETED", service), taskActivityDetails: { closeReason: "EXPIRED" } }), "Task closed as Expired automatically")
  const automatic = { ...callbackCompletion, taskActivityActor: { kind: "SERVICE", subject: "work-recovery-resolution" } } as ConversationTimelineItem
  assert.equal(taskActivityDetail(automatic), "Missed call review completed after caller@example.test's callback attempt")
})

test("the Texts tab keeps only messages and the Calls tab keeps only calls", () => {
  const item = (type: string, minute: number) =>
    ({ id: type.toLowerCase(), type, occurredAt: `2026-08-13T12:0${minute}:00Z` }) as ConversationTimelineItem
  const items = [item("TASK", 5), item("AI_INTERACTION", 4), item("CALL", 3), item("MESSAGE", 2), item("CALL_HISTORY", 1)]
  const shown = (tab?: "all" | "texts" | "calls") => presentTimeline(items, tab).map((value) => value.id)
  assert.deepEqual(shown(), ["call_history", "message", "call", "ai_interaction", "task"])
  assert.deepEqual(shown("all"), shown())
  assert.deepEqual(shown("texts"), ["message"])
  assert.deepEqual(shown("calls"), ["call_history", "call", "ai_interaction"])
})

test("the text review Task stays out of the thread", () => {
  const text = (id: string, occurredAt: string) =>
    ({ id, type: "MESSAGE", occurredAt, message: { id, direction: "INBOUND", createdAt: occurredAt } }) as ConversationTimelineItem
  const review = { ...base, id: "review", type: "TASK", occurredAt: "2026-08-13T12:00:30Z", task: { origin: "INBOUND_MESSAGE_REVIEW" } } as ConversationTimelineItem
  const followUp = { ...base, id: "follow-up", type: "TASK", occurredAt: "2026-08-13T12:00:40Z", task: { origin: "STAFF_MESSAGE_FOLLOW_UP" } } as ConversationTimelineItem
  assert.deepEqual(
    presentTimeline([text("first", "2026-08-13T12:00:00Z"), review, followUp, text("next", "2026-08-13T12:01:00Z")]).map((item) => item.id),
    ["first", "follow-up", "next"],
  )
})
