"use client"

import type { Task } from "@/lib/api/generated/types.gen"
import type { EngagementSummary } from "@/lib/workspace-projection"
import { type NumberHistory } from "@/lib/workspace-history"

import { MessageConversation } from "./message-conversation"

export function EngagementWorkspace({
  engagement,
  practiceID,
  route,
  textTask,
  onTextReviewed,
  selectedTaskID,
  selectedCallID,
  selectedAIInteractionID,
  onTaskCreated,
  onTaskOpen,
  onCallOpen,
  onAIInteractionOpen,
  onHistoryChange,
}: {
  engagement: EngagementSummary
  practiceID: string
  route: string
  textTask?: Task
  onTextReviewed: (reviewKey: string) => void
  selectedTaskID?: string
  selectedCallID?: string
  selectedAIInteractionID?: string
  onTaskCreated: (task: Task) => void
  onTaskOpen: (task: Task) => void
  onCallOpen: (callID: string) => void
  onAIInteractionOpen: (interactionID: string) => void
  onHistoryChange?: (history: NumberHistory) => void
}) {
  return (
    <section className="flex min-h-0 min-w-0 flex-1 flex-col">
      <MessageConversation
        phone={engagement.phone}
        practiceID={practiceID}
        locationID={route}
        reviewKey={textReviewKey(textTask)}
        onReviewed={onTextReviewed}
        selectedTaskID={selectedTaskID}
        selectedCallID={selectedCallID}
        selectedAIInteractionID={selectedAIInteractionID}
        onTaskCreated={onTaskCreated}
        onTaskOpen={onTaskOpen}
        onCallOpen={onCallOpen}
        onAIInteractionOpen={onAIInteractionOpen}
        onHistoryChange={onHistoryChange}
      />
    </section>
  )
}

export function textReviewKey(task: Task | undefined) {
  return task ? `${task.id}:${task.version}` : ""
}
