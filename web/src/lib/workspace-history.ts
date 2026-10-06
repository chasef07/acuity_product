import type { ConversationTimelineItem, Task } from "@/lib/api/generated/types.gen"
import { appointmentOutcomeTitle } from "./ai-interactions.ts"
import { newestFirst, oldestFirst } from "./workspace-ordering.ts"

export function presentTimeline(items: ConversationTimelineItem[]) {
  return oldestFirst(
    [...new Map(items.map((item) => [`${item.type}:${item.id}`, item])).values()],
    (item) => item.occurredAt,
  )
}

export function callHistoryPresentation(item: ConversationTimelineItem) {
  const entries = item.entries ?? []
  const ai = entries.flatMap((entry) =>
    entry.aiInteraction ? [entry.aiInteraction] : [],
  )
  const calls = entries.flatMap((entry) => (entry.call ? [entry.call] : []))
  const tasks = entries.flatMap((entry) => (entry.task ? [entry.task] : []))
  const details: string[] = []
  for (const interaction of ai) {
    if (interaction.appointmentOutcome !== "INDETERMINATE") {
      details.push(appointmentOutcomeTitle(interaction.appointmentOutcome))
    } else if (interaction.status === "COMPLETED") {
      details.push("No appointment action recorded")
    }
    if (interaction.status === "FAILED") details.push("AI call failed")
    if (interaction.status === "IN_PROGRESS") details.push("AI call in progress")
    if (interaction.status === "ESCALATED" && calls.length === 0) {
      details.push("Transfer requested · Staff outcome unknown")
    }
  }
  for (const call of calls) {
    const inbound = call.direction === "INBOUND"
    switch (call.outcome) {
      case "CONNECTED":
        details.push(inbound ? "Connected to staff" : "Connected")
        break
      case "NEEDS_DISPOSITION":
        details.push("Connected · Outcome needed")
        break
      case "RESOLVED":
        details.push("Resolved on call")
        break
      case "FOLLOW_UP_REQUIRED":
        details.push("Follow-up needed")
        break
      case "VOICEMAIL":
        details.push("Voicemail received")
        break
      case "MISSED":
      case "UNANSWERED":
        details.push(
          ai.length && inbound
            ? "Staff not reached"
            : inbound ? "Missed call" : "No answer",
        )
        break
      case "RINGING":
        details.push("Ringing")
        break
      case "CONNECTING":
        details.push("Connecting")
        break
      default:
        details.push("Call outcome unknown")
    }
  }
  return {
    title: ai.length
      ? "AI call"
      : calls[0]?.direction === "OUTBOUND" ? "Outbound call" : "Inbound call",
    details: [...new Set([
      ...details,
      ...ai.map((value) => value.locationName),
      ...calls.map((value) => value.locationName),
    ])].filter(Boolean),
    tasks: [...new Map(tasks.map((task) => [task.id, task])).values()],
  }
}

export type NumberHistory = {
  phone: string
  items: ConversationTimelineItem[]
}

type TimelineCall = NonNullable<ConversationTimelineItem["call"]>

const endedCallOutcomes: Partial<Record<TimelineCall["outcome"], string>> = {
  UNANSWERED: "no answer",
  MISSED: "no answer",
  VOICEMAIL: "voicemail",
  NEEDS_DISPOSITION: "connected",
  RESOLVED: "resolved on call",
  FOLLOW_UP_REQUIRED: "follow-up needed",
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

export function linkedTaskLabel(task: Pick<Task, "id" | "origin" | "title" | "state">, selectedTaskID?: string) {
  const reference = taskReference(task, selectedTaskID)
  const state = task.state === "OPEN" ? "Open" : "Completed"
  return reference === "Task" ? `${task.title} — ${state}` : `${reference}: ${task.title} — ${state}`
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
      return attributed("completed", actor)
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

export function callbackCompletionLabel(
  items: readonly ConversationTimelineItem[],
  task: Pick<Task, "id" | "origin" | "state" | "completedAt">,
) {
  if (task.state !== "COMPLETED" || !task.completedAt) return undefined
  const completedAt = Date.parse(task.completedAt)
  const completion = historyEvidence(items).find((item) =>
    item.type === "TASK" &&
    item.task?.id === task.id &&
    item.taskActivity === "TASK_AUTO_COMPLETED_CALLBACK_ATTEMPT" &&
    Date.parse(item.occurredAt) === completedAt,
  )
  if (!completion) return undefined
  return `${taskKindLabel(task)} ${callbackCompletionPhrase(callbackCallerEmail(completion))}`
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
      endedCallOutcomes[item.call.outcome] &&
      Date.parse(item.call.startedAt) > openedAt
        ? [item.call]
        : [],
    ),
    (value) => value.startedAt,
  )[0]
  if (!call) return undefined
  const attempt = `at ${formatTime(call.startedAt)} (${endedCallOutcomes[call.outcome]})`
  return call.placedByEmail
    ? `${call.placedByEmail} called this number ${attempt}. This Task is still open.`
    : `This number was called ${attempt}. This Task is still open.`
}

export function conversationDateLabel(value: string, now = new Date()) {
  const date = new Date(value)
  if (sameLocalDate(date, now)) return "Today"

  const yesterday = new Date(now)
  yesterday.setDate(yesterday.getDate() - 1)
  if (sameLocalDate(date, yesterday)) return "Yesterday"

  return new Intl.DateTimeFormat(undefined, {
    month: "short",
    day: "numeric",
    ...(date.getFullYear() === now.getFullYear() ? {} : { year: "numeric" }),
  }).format(date)
}

export function sameConversationDate(left: string, right: string) {
  return sameLocalDate(new Date(left), new Date(right))
}

function sameLocalDate(left: Date, right: Date) {
  return (
    left.getFullYear() === right.getFullYear() &&
    left.getMonth() === right.getMonth() &&
    left.getDate() === right.getDate()
  )
}
