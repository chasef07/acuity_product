"use client"

import { type ReactNode } from "react"

import { Item } from "@/components/ui/item"
import type {
  ConversationTimelineItem,
  Task,
} from "@/lib/api/generated/types.gen"
import { aiCallTimelinePresentation } from "@/lib/ai-interactions"
import { formatClockTime } from "@/lib/format-time"
import {
  callDirectionLabels,
  callStateLabels,
  formatDuration,
} from "@/lib/vocabulary"
import {
  callHistoryPresentation,
  linkedTaskLabel,
  taskActivityDetail,
} from "@/lib/workspace-history"

import { MessageEntry } from "./message-entry"

export function TimelineEntry({
  item,
  contextLocationID,
  canMutate,
  onChanged,
  onTaskCreated,
  onTaskOpen,
  onCallOpen,
  onAIInteractionOpen,
  selectedTaskID,
  selectedCallID,
  selectedAIInteractionID,
}: {
  item: ConversationTimelineItem
  contextLocationID: string
  canMutate: boolean
  onChanged: () => void
  onTaskCreated: (task: Task) => void
  onTaskOpen?: (task: Task) => void
  onCallOpen?: (callID: string) => void
  onAIInteractionOpen?: (interactionID: string) => void
  selectedTaskID?: string
  selectedCallID?: string
  selectedAIInteractionID?: string
}) {
  if (item.type === "CALL_HISTORY") {
    const entries = item.entries ?? []
    const primary = entries.find((entry) => entry.aiInteraction) ??
      entries.find((entry) => entry.call)
    const presentation = callHistoryPresentation(item)
    const selected = entries.some((entry) =>
      (entry.call && entry.call.id === selectedCallID) ||
      (entry.aiInteraction && entry.aiInteraction.id === selectedAIInteractionID) ||
      (entry.task && entry.task.id === selectedTaskID),
    )
    const title = primary?.aiInteraction
      ? aiCallTimelinePresentation(primary.aiInteraction.appointmentOutcome, primary.aiInteraction.status).title
      : primary?.call ? callTouchpoint(primary.call).label : presentation.title
    const related = entries.filter((entry) => entry !== primary)
    return (
      <ActivityItem
        title={title}
        selected={selected}
        metadata={[
          ...presentation.details.filter((detail) =>
            detail !== title && detail !== "No appointment action recorded",
          ),
          ...presentation.tasks.map((task) => linkedTaskLabel(task, selectedTaskID)),
          formatClockTime(item.occurredAt),
        ]}
        actionLabel={primary?.aiInteraction ? "View AI call" : primary?.call?.outcome === "VOICEMAIL" ? "View voicemail" : "View call"}
        onOpen={primary?.aiInteraction && onAIInteractionOpen
          ? () => onAIInteractionOpen(primary.aiInteraction!.id)
          : primary?.call && onCallOpen ? () => onCallOpen(primary.call!.id) : undefined}
        relatedActions={related.length > 0 ? related.map((entry) => {
          const task = entry.task
          const call = entry.call
          const label = task ? `View task: ${task.title}` : call?.outcome === "VOICEMAIL" ? "View voicemail" : "View staff call"
          const onOpen = task && onTaskOpen ? () => onTaskOpen(task)
            : call && onCallOpen ? () => onCallOpen(call.id) : undefined
          if (!onOpen) return null
          return (
            <button
              key={`${entry.type}:${entry.id}`}
              type="button"
              aria-label={label}
              className="text-xs text-muted-foreground underline-offset-4 hover:text-foreground hover:underline focus-visible:underline"
              onClick={onOpen}
            >
              {task ? "Task" : call?.outcome === "VOICEMAIL" ? "Voicemail" : "Staff call"}
            </button>
          )
        }) : undefined}
      />
    )
  }
  if (item.type === "MESSAGE" && item.message) {
    return (
      <MessageEntry
        message={item.message}
        showLocation={item.message.thread.locationId !== contextLocationID}
        canMutate={canMutate}
        onChanged={onChanged}
        onTaskCreated={onTaskCreated}
      />
    )
  }
  if (item.type === "CALL" && item.call) {
    const touchpoint = callTouchpoint(item.call)
    return (
      <ActivityItem
        selected={item.call.id === selectedCallID}
        title={item.call.transferReason || touchpoint.label}
        metadata={[
          item.call.transferReason ? touchpoint.label : "",
          touchpoint.detail,
          item.call.locationId !== contextLocationID
            ? item.call.locationName
            : "",
          formatClockTime(item.occurredAt),
        ]}
        actionLabel={
          item.call.outcome === "VOICEMAIL" ? "View voicemail" : "View call"
        }
        onOpen={onCallOpen ? () => onCallOpen(item.call!.id) : undefined}
      />
    )
  }
  if (item.type === "AI_INTERACTION" && item.aiInteraction) {
    const interaction = item.aiInteraction
    const presentation = aiCallTimelinePresentation(
      interaction.appointmentOutcome,
      interaction.status,
    )
    return (
      <ActivityItem
        selected={interaction.id === selectedAIInteractionID}
        title={presentation.title}
        metadata={[
          presentation.detail,
          interaction.locationId !== contextLocationID
            ? interaction.locationName
            : "",
          formatClockTime(item.occurredAt),
        ]}
        actionLabel="View AI call"
        onOpen={
          onAIInteractionOpen
            ? () => onAIInteractionOpen(interaction.id)
            : undefined
        }
      />
    )
  }
  if (item.type === "TASK" && item.task) {
    const task = item.task
    if (task.origin === "INBOUND_MESSAGE_REVIEW") return null
    return (
      <ActivityItem
        selected={task.id === selectedTaskID}
        title={task.title}
        metadata={[
          taskActivityDetail(item, selectedTaskID),
          item.taskActivity === "CATEGORY_CHANGED" ? `${String(item.taskActivityDetails?.oldCategory ?? "Uncategorized")} → ${String(item.taskActivityDetails?.newCategory ?? "Uncategorized")}` : "",
          task.locationId !== contextLocationID ? task.locationName : "",
          formatClockTime(item.occurredAt),
        ]}
        actionLabel="View task"
        onOpen={onTaskOpen ? () => onTaskOpen(task) : undefined}
      />
    )
  }
  return null
}

function ActivityItem({
  title,
  metadata,
  actionLabel,
  selected,
  onOpen,
  relatedActions,
}: {
  title: string
  metadata: string[]
  actionLabel: string
  selected: boolean
  onOpen?: () => void
  relatedActions?: ReactNode
}) {
  const content = (
    <span className="flex flex-1 flex-col gap-1">
      <span className="line-clamp-1 w-fit text-xs/relaxed font-medium leading-snug">
        {title}
      </span>
      <span className="line-clamp-2 text-left text-xs/relaxed font-normal text-muted-foreground">
        {metadata.filter(Boolean).join(" · ")}
      </span>
    </span>
  )

  if (!onOpen) {
    return (
      <Item size="sm" className="mx-auto max-w-xl">
        {content}
      </Item>
    )
  }

  return (
    <Item
      size="sm"
      data-selected={selected || undefined}
      className="relative mx-auto max-w-xl cursor-pointer text-left before:absolute before:inset-y-2 before:left-0 before:w-0.5 before:rounded-full before:bg-transparent hover:bg-muted data-[selected=true]:bg-transparent data-[selected=true]:before:bg-foreground data-[selected=true]:hover:bg-transparent"
      render={relatedActions ? <div /> : (
        <button
          type="button"
          aria-current={selected ? "true" : undefined}
          aria-label={`${actionLabel}: ${title}`}
          onClick={onOpen}
        />
      )}
    >
      {relatedActions ? (
        <>
          <button
            type="button"
            aria-current={selected ? "true" : undefined}
            data-selected={selected || undefined}
            aria-label={`${actionLabel}: ${title}`}
            className="flex min-w-0 flex-1 text-left"
            onClick={onOpen}
          >
            {content}
          </button>
          <span className="flex shrink-0 items-center gap-2">{relatedActions}</span>
        </>
      ) : content}
    </Item>
  )
}

function callTouchpoint(call: NonNullable<ConversationTimelineItem["call"]>) {
  const duration = call.durationSeconds > 0 ? formatDuration(call.durationSeconds) : ""
  if (call.direction === "OUTBOUND") {
    const unanswered = call.outcome === "MISSED" || call.outcome === "UNANSWERED"
    return {
      label: call.placedByEmail ? `${callDirectionLabels.OUTBOUND} by ${call.placedByEmail}` : callDirectionLabels.OUTBOUND,
      detail: [callStateLabels[unanswered ? "UNANSWERED" : call.outcome], duration].filter(Boolean).join(" · "),
    }
  }
  if (call.outcome === "VOICEMAIL") {
    return {
      label: callStateLabels.VOICEMAIL,
      detail: duration,
    }
  }
  if (call.outcome === "MISSED" || call.outcome === "UNANSWERED") {
    return {
      label: callStateLabels.MISSED,
      detail: duration,
    }
  }
  return {
    label: callDirectionLabels.INBOUND,
    detail: [callStateLabels[call.outcome], duration].filter(Boolean).join(" · "),
  }
}
