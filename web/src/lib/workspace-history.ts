import type { ConversationTimelineItem } from "@/lib/api/generated/types.gen"

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

  return items.filter((item) => {
    if (
      item.type === "AI_INTERACTION" &&
      item.aiInteraction?.status === "ESCALATED" &&
      item.aiInteraction.appointmentOutcome === "INDETERMINATE" &&
      inboundSourceCallIDs.has(item.aiInteraction.sourceCallId)
    ) {
      return false
    }
    return !isRecoveryTechnicalEvent(item)
  })
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
