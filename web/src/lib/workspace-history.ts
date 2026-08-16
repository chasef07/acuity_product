import type { ConversationTimelineItem } from "@/lib/api/generated/types.gen"
import { oldestFirst } from "./workspace-ordering.ts"

export function presentTimeline(items: ConversationTimelineItem[]) {
  const inboundSourceCallIDs = new Set(
    items
      .filter(
        (item) =>
          item.type === "CALL" &&
          item.call?.direction === "INBOUND" &&
          item.call.sourceCallId,
      )
      .map((item) => item.call!.sourceCallId!),
  )

  return oldestFirst(
    items.filter((item) => {
      if (
        item.type === "AI_INTERACTION" &&
        item.aiInteraction?.status === "ESCALATED" &&
        item.aiInteraction.appointmentOutcome === "INDETERMINATE" &&
        inboundSourceCallIDs.has(item.aiInteraction.sourceCallId)
      ) {
        return false
      }
      return !isRecoveryTechnicalEvent(item)
    }),
    (item) => item.occurredAt,
  )
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

export function technicalTimelineItems(items: ConversationTimelineItem[]) {
  return items.filter(isRecoveryTechnicalEvent)
}

export function recoveryFollowUpCallIDs(items: ConversationTimelineItem[]) {
  return new Set(
    items
      .filter(isRecoveryTechnicalEvent)
      .flatMap((item) => [
        item.task?.callId,
        ...(item.task?.interactions.map((interaction) => interaction.callId) ?? []),
      ])
      .filter((callID): callID is string => Boolean(callID)),
  )
}

function isRecoveryTechnicalEvent(item: ConversationTimelineItem) {
  return (
    item.type === "TASK" &&
    (item.task?.origin === "MISSED_CALL_RECOVERY" ||
      item.task?.origin === "VOICEMAIL_RECOVERY") &&
    (item.taskActivity === "TASK_CREATED" ||
      item.taskActivity === "INTERACTION_ATTACHED")
  )
}

function sameLocalDate(left: Date, right: Date) {
  return (
    left.getFullYear() === right.getFullYear() &&
    left.getMonth() === right.getMonth() &&
    left.getDate() === right.getDate()
  )
}
