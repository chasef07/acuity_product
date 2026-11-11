"use client"

import { useState } from "react"
import { PhoneIncomingIcon, PhoneOutgoingIcon, PlayIcon } from "lucide-react"

import { Button } from "@/components/ui/button"
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible"
import { Item } from "@/components/ui/item"
import { Spinner } from "@/components/ui/spinner"
import type {
  ConversationTimelineItem,
  Task,
} from "@/lib/api/generated/types.gen"
import { useCallingCall } from "@/lib/clients/calls"
import { readFailure } from "@/lib/data/queries"
import { formatClockTime } from "@/lib/format-time"
import { cn } from "@/lib/utils"
import { callStateLabels, formatClock } from "@/lib/vocabulary"
import {
  type CallLine,
  type CallLineTone,
  callHistoryPresentation,
  linkedTaskLabel,
  linkedTaskStatus,
  taskActivityDetail,
} from "@/lib/workspace-history"

import { MessageEntry } from "./message-entry"
import { VoicemailSource } from "./recording-source"

export function TimelineEntry({
  item,
  contextLocationID,
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
  onChanged: () => void
  onTaskCreated: (task: Task) => void
  onTaskOpen?: (task: Task) => void
  onCallOpen?: (callID: string) => void
  onAIInteractionOpen?: (interactionID: string) => void
  selectedTaskID?: string
  selectedCallID?: string
  selectedAIInteractionID?: string
}) {
  if (item.type === "MESSAGE" && item.message) {
    return (
      <MessageEntry
        message={item.message}
        showLocation={item.message.thread.locationId !== contextLocationID}
        onChanged={onChanged}
        onTaskCreated={onTaskCreated}
      />
    )
  }
  if (item.type === "TASK" && item.task) {
    const task = item.task
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
  const line = callHistoryPresentation(item)
  if (!line) return null
  return (
    <CallLineEntry
      line={line}
      occurredAt={item.occurredAt}
      otherLocations={line.locations.filter((location) => location.id !== contextLocationID)}
      selected={
        line.tasks.some((task) => task.id === selectedTaskID) ||
        line.sources.some((source) =>
          source.id === (source.kind === "ai" ? selectedAIInteractionID : selectedCallID),
        )
      }
      selectedTaskID={selectedTaskID}
      onTaskOpen={onTaskOpen}
      onCallOpen={onCallOpen}
      onAIInteractionOpen={onAIInteractionOpen}
    />
  )
}

const toneClasses = {
  success: "text-success",
  warning: "text-warning",
  destructive: "text-destructive",
  muted: "text-muted-foreground",
} satisfies Record<CallLineTone, string>

const detailAction = "text-left underline-offset-4 hover:text-foreground hover:underline focus-visible:underline"

function CallLineEntry({
  line,
  occurredAt,
  otherLocations,
  selected,
  selectedTaskID,
  onTaskOpen,
  onCallOpen,
  onAIInteractionOpen,
}: {
  line: CallLine
  occurredAt: string
  otherLocations: CallLine["locations"]
  selected: boolean
  selectedTaskID?: string
  onTaskOpen?: (task: Task) => void
  onCallOpen?: (callID: string) => void
  onAIInteractionOpen?: (interactionID: string) => void
}) {
  const DirectionIcon = line.direction === "OUTBOUND" ? PhoneOutgoingIcon : PhoneIncomingIcon
  const details = line.sources.flatMap((source) => {
    const open = source.kind === "ai" ? onAIInteractionOpen : onCallOpen
    return open ? [{ ...source, open: () => open(source.id) }] : []
  })
  const time = formatClockTime(occurredAt)
  return (
    <Collapsible
      render={
        <Item
          data-selected={selected || undefined}
          className="relative mx-auto max-w-xl flex-col items-stretch gap-0 p-0 before:absolute before:inset-y-2 before:left-0 before:w-0.5 before:rounded-full before:bg-transparent data-[selected=true]:before:bg-foreground"
        />
      }
    >
      <CollapsibleTrigger
        render={
          <button
            type="button"
            aria-current={selected ? "true" : undefined}
            aria-label={[
              line.lead,
              line.about,
              time,
              ...otherLocations.map((location) => location.name),
              line.outcome.label,
            ].filter(Boolean).join(", ")}
            className="flex w-full items-start gap-2.5 rounded-md px-3 py-2.5 text-left outline-none hover:bg-muted focus-visible:ring-[3px] focus-visible:ring-ring/50"
          />
        }
      >
        <DirectionIcon aria-hidden="true" className="mt-0.5 size-3.5 shrink-0 text-muted-foreground" />
        <span className="flex min-w-0 flex-1 flex-col gap-0.5">
          <span data-slot="call-line-summary" className="line-clamp-2 text-muted-foreground wrap-anywhere">
            <span className="font-medium text-foreground">{line.lead}</span>
            {line.about && ` · ${line.about}`}
            {otherLocations.map((location) => ` · ${location.name}`)}
          </span>
          <time
            data-slot="call-line-time"
            dateTime={occurredAt}
            className="text-xs tabular-nums whitespace-nowrap text-muted-foreground"
          >
            {time}
          </time>
        </span>
        <span className={cn("max-w-[50%] shrink-0 text-right font-medium wrap-anywhere", toneClasses[line.outcome.tone])}>
          {line.outcome.label}
        </span>
      </CollapsibleTrigger>
      {line.voicemail && <VoicemailPlayback key={line.voicemail.callID} voicemail={line.voicemail} />}
      <CollapsibleContent className="flex flex-col gap-2 px-3 pb-2.5 pl-9">
        <ul className="flex flex-col gap-1">
          {line.steps.map((step, index) => (
            <li key={index} className={toneClasses[step.tone]}>
              {step.text}
            </li>
          ))}
        </ul>
        {line.tasks.length > 0 && (
          <ul className="flex flex-col gap-1">
            {line.tasks.map((task) => (
              <li key={task.id}>
                {onTaskOpen ? (
                  <button
                    type="button"
                    aria-label={`View task: ${task.title}`}
                    className={cn(detailAction, "font-medium")}
                    onClick={() => onTaskOpen(task)}
                  >
                    {linkedTaskLabel(task, selectedTaskID)}
                  </button>
                ) : (
                  <span className="font-medium">{linkedTaskLabel(task, selectedTaskID)}</span>
                )}
                <span className="text-muted-foreground"> · {linkedTaskStatus(task)}</span>
              </li>
            ))}
          </ul>
        )}
        <p className="flex flex-wrap gap-x-3 gap-y-1 text-muted-foreground">
          <span>{line.locations.map((location) => location.name).join(" · ")}</span>
          {details.map((detail) => (
            <button
              key={`${detail.kind}:${detail.id}`}
              type="button"
              className={detailAction}
              onClick={detail.open}
            >
              {detail.label}
            </button>
          ))}
        </p>
      </CollapsibleContent>
    </Collapsible>
  )
}

function VoicemailPlayback({ voicemail }: { voicemail: NonNullable<CallLine["voicemail"]> }) {
  const [opened, setOpened] = useState(false)
  const call = useCallingCall(voicemail.callID, opened)
  const failed = opened && Boolean(readFailure(call)) && call.status !== "ready"
  const loading = opened && call.status === "loading"
  const length = voicemail.seconds > 0 ? ` ${formatClock(voicemail.seconds)}` : ""
  return (
    <div className="flex flex-col gap-1 px-3 pb-2.5 pl-9">
      {!(opened && call.status === "ready") && (
        <Button
          size="xs"
          variant="outline"
          className="self-start"
          aria-label={`Play voicemail${length}`}
          disabled={loading}
          onClick={() => (failed ? void call.retry() : setOpened(true))}
        >
          {loading ? <Spinner data-icon="inline-start" /> : <PlayIcon data-icon="inline-start" />}
          <span className="tabular-nums">{callStateLabels.VOICEMAIL}{length}</span>
        </Button>
      )}
      {loading && <span role="status" className="sr-only">Loading voicemail</span>}
      {failed && (
        <p role="alert" className="text-destructive">
          The voicemail could not be loaded. Select Voicemail to try again.
        </p>
      )}
      {opened && call.status === "ready" && (
        call.data.voicemail
          ? <VoicemailSource call={call.data} compact autoPlay />
          : <p role="alert" className="text-destructive">This call has no voicemail recording.</p>
      )}
    </div>
  )
}

function ActivityItem({
  title,
  metadata,
  actionLabel,
  selected,
  onOpen,
}: {
  title: string
  metadata: string[]
  actionLabel: string
  selected: boolean
  onOpen?: () => void
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
      <Item className="mx-auto max-w-xl">
        {content}
      </Item>
    )
  }

  return (
    <Item
      data-selected={selected || undefined}
      className="relative mx-auto max-w-xl cursor-pointer text-left before:absolute before:inset-y-2 before:left-0 before:w-0.5 before:rounded-full before:bg-transparent hover:bg-muted data-[selected=true]:bg-transparent data-[selected=true]:before:bg-foreground data-[selected=true]:hover:bg-transparent"
      render={
        <button
          type="button"
          aria-current={selected ? "true" : undefined}
          aria-label={`${actionLabel}: ${title}`}
          onClick={onOpen}
        />
      }
    >
      {content}
    </Item>
  )
}
