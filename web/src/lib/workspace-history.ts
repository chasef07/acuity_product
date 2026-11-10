import type { CallState, ConversationTimelineItem, Task, TaskCloseReason } from "@/lib/api/generated/types.gen"
import { isSettledCallState } from "./calling/call-state.ts"
import { sameLocalDay } from "./format-time.ts"
import {
  appointmentOutcomeTitles,
  callDirectionLabels,
  callLineLabels,
  callStateLabels,
  formatClock,
  formatDuration,
  taskCloseReasonLabels,
  taskCloseSummary,
  type threadTabLabels,
} from "./vocabulary.ts"
import { newestFirst, oldestFirst } from "./workspace-ordering.ts"

export type ThreadTab = keyof typeof threadTabLabels

const threadTabTypes: Record<Exclude<ThreadTab, "all">, ConversationTimelineItem["type"][]> = {
  texts: ["MESSAGE"],
  calls: ["CALL_HISTORY", "CALL", "AI_INTERACTION"],
}

export function presentTimeline(items: ConversationTimelineItem[], tab: ThreadTab = "all") {
  return oldestFirst(
    [...new Map(items.map((item) => [`${item.type}:${item.id}`, item])).values()]
      .filter((item) => tab === "all" || threadTabTypes[tab].includes(item.type))
      .filter((item) => !(item.type === "TASK" && item.task?.origin === "INBOUND_MESSAGE_REVIEW")),
    (item) => item.occurredAt,
  )
}

type HistoryCall = NonNullable<ConversationTimelineItem["call"]>
type HistoryAI = NonNullable<ConversationTimelineItem["aiInteraction"]>

export type CallLineTone = "success" | "warning" | "destructive" | "muted"

type CallLineOutcomeKind =
  | "resolved-by-ai"
  | "message-taken"
  | "answered"
  | "missed"
  | "voicemail"
  | "in-progress"
  | "failed"
  | "outbound-answered"
  | "no-answer"
  | "unknown"

type CallLineOutcome = { kind: CallLineOutcomeKind; label: string; tone: CallLineTone }

export type CallLine = {
  direction: HistoryCall["direction"]
  lead: string
  about: string
  outcome: CallLineOutcome
  voicemail?: { callID: string; seconds: number }
  steps: { tone: CallLineTone; text: string }[]
  tasks: Task[]
  sources: { kind: "ai" | "call" | "voicemail"; id: string }[]
  locations: { id: string; name: string }[]
}

const outcomeTones = {
  "resolved-by-ai": "success",
  "message-taken": "muted",
  answered: "success",
  missed: "destructive",
  voicemail: "warning",
  "in-progress": "muted",
  failed: "destructive",
  "outbound-answered": "success",
  "no-answer": "warning",
  unknown: "muted",
} satisfies Record<CallLineOutcomeKind, CallLineTone>

const aiOutcomeKinds = {
  IN_PROGRESS: "in-progress",
  COMPLETED: "resolved-by-ai",
  ESCALATED: "unknown",
  FAILED: "failed",
} satisfies Record<HistoryAI["status"], CallLineOutcomeKind>

const callOutcomeKinds = {
  INBOUND: {
    PREPARING: "in-progress",
    RINGING: "in-progress",
    CONNECTING: "in-progress",
    VOICEMAIL_GREETING: "in-progress",
    VOICEMAIL_RECORDING: "in-progress",
    CONNECTED: "answered",
    NEEDS_DISPOSITION: "answered",
    RESOLVED: "answered",
    FOLLOW_UP_REQUIRED: "answered",
    VOICEMAIL: "voicemail",
    MISSED: "missed",
    UNANSWERED: "missed",
  },
  OUTBOUND: {
    PREPARING: "in-progress",
    RINGING: "in-progress",
    CONNECTING: "in-progress",
    VOICEMAIL_GREETING: "unknown",
    VOICEMAIL_RECORDING: "unknown",
    CONNECTED: "outbound-answered",
    NEEDS_DISPOSITION: "outbound-answered",
    RESOLVED: "outbound-answered",
    FOLLOW_UP_REQUIRED: "outbound-answered",
    VOICEMAIL: "unknown",
    MISSED: "no-answer",
    UNANSWERED: "no-answer",
  },
} satisfies Record<HistoryCall["direction"], Record<CallState, CallLineOutcomeKind>>

const decidingOutcomes: CallLineOutcomeKind[] = [
  "answered",
  "outbound-answered",
  "voicemail",
  "in-progress",
  "missed",
  "no-answer",
]

function outcome(kind: CallLineOutcomeKind, label: string): CallLineOutcome {
  return { kind, label, tone: outcomeTones[kind] }
}

function callOutcomeKind(call: HistoryCall): CallLineOutcomeKind {
  return callOutcomeKinds[call.direction]?.[call.outcome] ?? "unknown"
}

function callStateLabel(call: HistoryCall) {
  return callStateLabels[call.outcome] ?? callLineLabels.unknown
}

function callDuration(call: HistoryCall) {
  return call.durationSeconds > 0 ? formatDuration(call.durationSeconds) : ""
}

function answeredBy(call: HistoryCall) {
  return call.answeredByEmail ? `${call.answeredByEmail} answered` : callLineLabels.staffAnswered
}

function callOutcome(call: HistoryCall) {
  const kind = callOutcomeKind(call)
  switch (kind) {
    case "answered":
      return outcome(kind, answeredBy(call))
    case "outbound-answered":
      return outcome(kind, [callLineLabels.answered, callDuration(call)].filter(Boolean).join(" · "))
    case "missed":
      return outcome(kind, callLineLabels.missed)
    case "no-answer":
      return outcome(kind, callStateLabels.UNANSWERED)
    default:
      return outcome(kind, callStateLabel(call))
  }
}

function aiOutcome(interaction: HistoryAI, tasks: Task[]) {
  const kind: CallLineOutcomeKind = aiOutcomeKinds[interaction.status] ?? "unknown"
  switch (kind) {
    case "resolved-by-ai":
      if (interaction.appointmentOutcome === "PARTIAL") return outcome("unknown", appointmentOutcomeTitles.PARTIAL)
      return tasks.some((task) => task.origin === "ABITA_AI")
        ? outcome("message-taken", callLineLabels.messageTaken)
        : outcome(kind, callLineLabels.resolvedByAI)
    case "failed":
      return outcome(kind, callLineLabels.aiFailed)
    case "in-progress":
      return outcome(kind, callLineLabels.aiInProgress)
    default:
      return outcome("unknown", interaction.status === "ESCALATED" ? callLineLabels.transferRequested : callLineLabels.unknown)
  }
}

const aiStepLabels = {
  IN_PROGRESS: callLineLabels.aiInProgress,
  COMPLETED: callLineLabels.aiAnswered,
  ESCALATED: callLineLabels.aiTransferred,
  FAILED: callLineLabels.aiFailed,
} satisfies Record<HistoryAI["status"], string>

function aiSteps(interaction: HistoryAI, transferReason: string): CallLine["steps"] {
  const label = aiStepLabels[interaction.status] ?? callLineLabels.unknown
  const summary = interaction.summary?.trim()
  return [
    {
      tone: interaction.status === "FAILED" ? "destructive" : "muted",
      text: interaction.status === "ESCALATED" && transferReason ? `${label}: ${transferReason}` : label,
    },
    ...(interaction.appointmentOutcome === "INDETERMINATE" ? [] : [{
      tone: interaction.appointmentOutcome === "PARTIAL" ? "warning" as const : "success" as const,
      text: appointmentOutcomeTitles[interaction.appointmentOutcome],
    }]),
    ...(summary ? [{ tone: "muted" as const, text: summary }] : []),
  ]
}

function callSteps(call: HistoryCall): CallLine["steps"] {
  const kind = callOutcomeKind(call)
  const withDuration = (label: string) => [label, callDuration(call)].filter(Boolean).join(", ")
  const afterCall: CallLine["steps"] = call.outcome === "CONNECTED" ? [] : [{
    tone: call.outcome === "NEEDS_DISPOSITION" ? "warning" : "muted",
    text: callStateLabel(call),
  }]
  switch (kind) {
    case "answered":
      return [{ tone: "success", text: withDuration(answeredBy(call)) }, ...afterCall]
    case "outbound-answered":
      return [{ tone: "success", text: withDuration(callLineLabels.answered) }, ...afterCall]
    case "missed":
      return [{ tone: "destructive", text: callLineLabels.nobodyAnswered }]
    case "voicemail":
      return [{
        tone: "warning",
        text: [callLineLabels.leftVoicemail, call.voicemailDurationSeconds ? formatClock(call.voicemailDurationSeconds) : ""].filter(Boolean).join(", "),
      }]
    case "no-answer":
      return [{ tone: "warning", text: callStateLabels.UNANSWERED }]
    default:
      return [{ tone: "muted", text: callStateLabel(call) }]
  }
}

const aboutLimit = 80

function firstSentence(text: string) {
  const sentence = text.trim().split(/(?<!\b(?:Dr|Mr|Mrs|Ms|St))[.!?]\s+/)[0]!.replace(/[.!?]+$/, "")
  return sentence.length <= aboutLimit
    ? sentence
    : `${sentence.slice(0, aboutLimit).replace(/\s+\S*$/, "")}…`
}

export function callHistoryPresentation(item: ConversationTimelineItem): CallLine | undefined {
  const entries = item.type === "CALL_HISTORY" ? item.entries ?? [] : [item]
  const ai = entries.flatMap((entry) => (entry.aiInteraction ? [entry.aiInteraction] : []))
  const calls = entries.flatMap((entry) => (entry.call ? [entry.call] : []))
  const tasks = [...new Map(entries.flatMap((entry) => (entry.task ? [[entry.task.id, entry.task] as const] : []))).values()]
  const interaction = ai.at(-1)
  const call = decidingOutcomes
    .map((kind) => calls.find((candidate) => callOutcomeKind(candidate) === kind))
    .find(Boolean) ?? calls[0]
  if (!call && !interaction) return undefined
  const outbound = !interaction && call?.direction === "OUTBOUND"
  const transferReason = calls.find((candidate) => candidate.transferReason)?.transferReason ?? ""
  const voicemail = calls.find((candidate) => candidate.outcome === "VOICEMAIL")
  const appointment = interaction && interaction.appointmentOutcome !== "INDETERMINATE"
    ? appointmentOutcomeTitles[interaction.appointmentOutcome]
    : ""
  return {
    direction: outbound ? "OUTBOUND" : "INBOUND",
    lead: !outbound
      ? callLineLabels.calledIn
      : call.placedByEmail ? `${call.placedByEmail} called` : callDirectionLabels.OUTBOUND,
    about: outbound
      ? call.taskTitle ? `about "${call.taskTitle}"` : ""
      : transferReason || firstSentence(interaction?.summary ?? "") || appointment,
    outcome: call ? callOutcome(call) : aiOutcome(interaction!, tasks),
    ...(voicemail ? { voicemail: { callID: voicemail.id, seconds: voicemail.voicemailDurationSeconds ?? 0 } } : {}),
    steps: [
      ...ai.flatMap((value) => aiSteps(value, transferReason)),
      ...calls.flatMap(callSteps),
    ],
    tasks,
    sources: [
      ...ai.map((value) => ({ kind: "ai" as const, id: value.id })),
      ...calls.map((value) => ({ kind: value.outcome === "VOICEMAIL" ? "voicemail" as const : "call" as const, id: value.id })),
    ],
    locations: [...new Map([...ai, ...calls].map((value) => [value.locationId, { id: value.locationId, name: value.locationName }])).values()],
  }
}

const noticeOutcomes: Partial<Record<CallState, CallState>> = {
  NEEDS_DISPOSITION: "CONNECTED",
  MISSED: "UNANSWERED",
}

export type NumberHistory = {
  phone: string
  items: ConversationTimelineItem[]
}

function taskKindLabel(task: Pick<Task, "origin">) {
  switch (task.origin) {
    case "MISSED_CALL_RECOVERY":
      return "Missed call review"
    case "VOICEMAIL_RECOVERY":
      return "Voicemail review"
    case "APPOINTMENT_REVIEW":
      return "Appointment review"
    case "INBOUND_MESSAGE_REVIEW":
      return "Text review"
    default:
      return "Task"
  }
}

function taskReference(task: Pick<Task, "id" | "origin">, selectedTaskID?: string) {
  return task.id === selectedTaskID ? "This Task" : taskKindLabel(task)
}

export function linkedTaskLabel(task: Pick<Task, "id" | "origin" | "title">, selectedTaskID?: string) {
  const reference = taskReference(task, selectedTaskID)
  return reference === "Task" ? task.title : `${reference}: ${task.title}`
}

export function linkedTaskStatus(task: Pick<Task, "state" | "closeReason" | "completedBy">) {
  return task.state === "OPEN" ? "Open" : taskCloseSummary(task) ?? "Completed"
}

export function taskActivityDetail(item: ConversationTimelineItem, selectedTaskID?: string) {
  const task = item.task
  if (!task) return ""
  return `${taskReference(task, selectedTaskID)} ${taskActivityPhrase(item, task)}`
}

function attributed(phrase: string, actor: ConversationTimelineItem["taskActivityActor"]) {
  if (actor?.kind === "HUMAN" && actor.email) return `${phrase} by ${actor.email}`
  if (actor?.kind === "SERVICE") return `${phrase} automatically`
  return phrase
}

function taskActivityPhrase(item: ConversationTimelineItem, task: Task) {
  const actor = item.taskActivityActor
  switch (item.taskActivity) {
    case "TASK_CREATED":
      return task.origin === "ABITA_AI" ? "created by AI" : attributed("created", actor)
    case "SOURCE_UPDATED":
      return "has a new message to review"
    case "CATEGORY_CHANGED":
      return attributed("group changed", actor)
    case "TITLE_CHANGED":
      return attributed("title changed", actor)
    case "TASK_COMPLETED":
      return actor?.kind === "HUMAN" && !actor.email
        ? `${completedPhrase(item)} by the team`
        : attributed(completedPhrase(item), actor)
    case "TASK_REOPENED":
      return attributed("reopened", actor)
    case "INTERACTION_ATTACHED":
      return "has new activity"
    case "TASK_AUTO_COMPLETED_INBOUND_CALL":
      return "completed after connected call"
    case "TASK_AUTO_COMPLETED_CALLBACK_ATTEMPT":
      return callbackCompletionPhrase(callbackCallerEmail(item))
    case "TASK_AUTO_COMPLETED_BOOKING":
      return "completed after booking"
    case "TASK_AUTO_COMPLETED_DUPLICATE":
      return "resolved as duplicate"
    default:
      return task.state === "OPEN" ? "open" : "completed"
  }
}

function completedPhrase(item: ConversationTimelineItem) {
  const reason = item.taskActivityDetails?.closeReason
  return typeof reason === "string" && reason !== "DONE" && Object.hasOwn(taskCloseReasonLabels, reason)
    ? `closed as ${taskCloseReasonLabels[reason as TaskCloseReason]}`
    : "completed"
}

function callbackCompletionPhrase(callerEmail: string | undefined) {
  return callerEmail
    ? `completed after ${callerEmail}'s callback attempt`
    : "completed after callback attempt"
}

function callbackCallerEmail(item: ConversationTimelineItem) {
  const email = item.taskActivityDetails?.callerEmail
  return typeof email === "string" && email ? email : undefined
}

function historyEvidence(items: readonly ConversationTimelineItem[]) {
  return items.flatMap((item) => [item, ...(item.entries ?? [])])
}

export function callbackCompletionLabel(task: Pick<Task, "origin" | "state" | "callbackCompletion">) {
  if (task.state !== "COMPLETED" || !task.callbackCompletion) return undefined
  return `${taskKindLabel(task)} ${callbackCompletionPhrase(task.callbackCompletion.callerEmail)}`
}

export function openTaskCallbackNotice(
  items: readonly ConversationTimelineItem[],
  task: Pick<Task, "id" | "state" | "createdAt">,
  formatTime: (value: string) => string,
) {
  if (task.state !== "OPEN") return undefined
  const evidence = historyEvidence(items)
  const openedAt = Math.max(
    Date.parse(task.createdAt),
    ...evidence
      .filter((item) => item.type === "TASK" && item.task?.id === task.id && item.taskActivity === "TASK_REOPENED")
      .map((item) => Date.parse(item.occurredAt)),
  )
  const call = newestFirst(
    evidence.flatMap((item) =>
      item.call &&
      item.call.direction === "OUTBOUND" &&
      isSettledCallState(item.call.outcome) &&
      Date.parse(item.call.startedAt) > openedAt
        ? [item.call]
        : [],
    ),
    (value) => value.startedAt,
  )[0]
  if (!call) return undefined
  const attempt = `(${callStateLabels[noticeOutcomes[call.outcome] ?? call.outcome].toLowerCase()}) · ${formatTime(call.startedAt)}`
  return call.placedByEmail
    ? `${call.placedByEmail} called this number ${attempt}. This Task is still open.`
    : `This number was called ${attempt}. This Task is still open.`
}

export function sameConversationDate(left: string, right: string) {
  return sameLocalDay(new Date(left), new Date(right))
}
