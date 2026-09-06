import type { ConversationTimelineItem } from "@/lib/api/generated/types.gen"
import { appointmentOutcomeTitle } from "./ai-interactions.ts"
import { oldestFirst } from "./workspace-ordering.ts"

// Grouping and authorization belong to the backend. Preserve every returned
// outcome and replace overlapping page items by their stable history identity.
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
            : inbound ? "Missed call" : "Unanswered call",
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
