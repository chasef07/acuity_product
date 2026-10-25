"use client"

import { type ReactNode, useState } from "react"
import { CheckIcon, CopyIcon, PhoneCallIcon } from "lucide-react"

import { Button } from "@/components/ui/button"
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select"
import { Spinner } from "@/components/ui/spinner"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import { useCallingNavigation } from "@/components/workspace/calling-dock"
import type { Task } from "@/lib/api/generated/types.gen"
import { formatPhone } from "@/lib/vocabulary"
import type { EngagementSummary } from "@/lib/workspace-projection"
import { type NumberHistory } from "@/lib/workspace-history"

import { MessageConversation } from "./message-conversation"
import { TextConversationAction } from "./text-conversation-action"
import { useCopyState } from "@/hooks/use-copy-state"

type EngagementWorkspaceProps = {
  engagement: EngagementSummary
  practiceID: string
  practiceName: string
  selectedTaskID?: string
  selectedCallID?: string
  selectedAIInteractionID?: string
  callAction?: ReactNode
  textTask?: Task
  onTextTaskUpdated?: (task: Task) => void
  onNextTask?: () => void
  onTaskCreated: (task: Task) => void
  onTaskOpen: (task: Task) => void
  onCallOpen: (callID: string) => void
  onAIInteractionOpen: (interactionID: string) => void
  onHistoryChange?: (history: NumberHistory) => void
}

type EngagementWorkspaceCalling = Pick<
  ReturnType<typeof useCallingNavigation>,
  | "callingOccupied"
  | "outboundPending"
  | "ownsSoftphone"
  | "startOutbound"
>

export function EngagementWorkspace(props: EngagementWorkspaceProps) {
  const calling = useCallingNavigation()
  return <EngagementWorkspaceView {...props} calling={calling} />
}

export function EngagementWorkspaceView({
  engagement,
  practiceID,
  practiceName,
  selectedTaskID,
  selectedCallID,
  selectedAIInteractionID,
  callAction,
  textTask,
  onTextTaskUpdated,
  onNextTask,
  onTaskCreated,
  onTaskOpen,
  onCallOpen,
  onAIInteractionOpen,
  onHistoryChange,
  calling,
}: EngagementWorkspaceProps & { calling: EngagementWorkspaceCalling }) {
  const [reviewedTextTaskKey, setReviewedTextTaskKey] = useState("")
  const textTaskKey = textTask ? `${textTask.id}:${textTask.version}` : ""
  const [chosenRoute, setRoute] = useState("")
  const route =
    engagement.locations.length === 1
      ? engagement.locations[0]!.id
      : engagement.locations.some((location) => location.id === chosenRoute)
        ? chosenRoute
        : ""
  const [callError, setCallError] = useState("")
  const [copyState, setCopyState] = useCopyState()
  const {
    callingOccupied,
    outboundPending,
    ownsSoftphone,
    startOutbound,
  } = calling
  return (
    <section className="flex min-h-0 flex-1 flex-col">
      <header className="group/conversation-header relative flex h-16 min-w-0 shrink-0 items-center gap-3 px-4 sm:h-13 sm:px-6">
        <div className="flex min-w-0 items-center gap-1">
          <div className="flex min-w-0 flex-col sm:flex-row sm:items-center sm:gap-2">
            <span className="truncate text-xs text-muted-foreground sm:text-base md:text-sm">
              {practiceName}
            </span>
            <span aria-hidden="true" className="hidden text-muted-foreground sm:inline">/</span>
            <h1 className="shrink-0 whitespace-nowrap text-base font-medium tracking-tight tabular-nums md:text-sm">
              {formatPhone(engagement.phone)}
            </h1>
          </div>
          <Tooltip>
            <TooltipTrigger
              render={
                <Button
                  type="button"
                  size="icon-sm"
                  variant="ghost"
                  className="[@media(hover:hover)]:opacity-0 group-hover/conversation-header:opacity-100 group-focus-within/conversation-header:opacity-100 focus-visible:opacity-100"
                  aria-label={
                    copyState === "copied" ? "Number copied" : "Copy phone number"
                  }
                  onClick={() => {
                    void navigator.clipboard.writeText(engagement.phone).then(
                      () => setCopyState("copied"),
                      () => setCopyState("failed"),
                    )
                  }}
                />
              }
            >
              {copyState === "copied" ? <CheckIcon /> : <CopyIcon />}
            </TooltipTrigger>
            <TooltipContent>
              {copyState === "copied"
                ? "Copied"
                : copyState === "failed"
                  ? "Copy failed"
                  : "Copy number"}
            </TooltipContent>
          </Tooltip>
          <span className="sr-only" role="status">
            {copyState === "copied"
              ? "Phone number copied"
              : copyState === "failed"
                ? "Phone number could not be copied"
                : ""}
          </span>

        </div>
        {callAction ?? (
          <Button
            className="h-7 shrink-0 shadow-xs"
            type="button"
            size="sm"
            variant="outline"
            title={`Call ${formatPhone(engagement.phone)}`}
            disabled={
              !route ||
              !ownsSoftphone ||
              callingOccupied
            }
            onClick={() => {
              setCallError("")
              void startOutbound(route, engagement.phone).then(
                (requestError) => setCallError(requestError ?? ""),
              )
            }}
          >
            {outboundPending ? (
              <>
                <Spinner data-icon="inline-start" /> Calling…
              </>
            ) : (
              <>
                <PhoneCallIcon data-icon="inline-start" /> Call
              </>
            )}
          </Button>
        )}
        <div className="ml-auto flex min-w-0 shrink-0 items-center gap-1 sm:gap-2">
          {textTask && onTextTaskUpdated && <TextConversationAction key={textTask.id} task={textTask} reviewed={reviewedTextTaskKey === textTaskKey} onUpdated={onTextTaskUpdated} onNext={onNextTask} />}
          {engagement.locations.length > 1 && (
            <NativeSelect
              aria-label="Sender office"
              className="max-w-24 md:max-w-none"
              size="sm"
              value={route}
              onChange={(event) => setRoute(event.target.value)}
            >
              <NativeSelectOption value="" disabled>
                Choose office
              </NativeSelectOption>
              {engagement.locations.map((location) => (
                <NativeSelectOption key={location.id} value={location.id}>
                  {location.name}
                </NativeSelectOption>
              ))}
            </NativeSelect>
          )}
        </div>
        {callError && (
          <p className="absolute right-3 top-[calc(100%+0.5rem)] z-10 rounded-lg border bg-popover px-3 py-2 text-xs text-destructive shadow-sm">
            {callError}
          </p>
        )}
      </header>
      <MessageConversation
        timelineSource={{
          kind: "engagement",
          practiceID,
          phone: engagement.phone,
        }}
        practiceID={practiceID}
        locationID={route}
        reviewKey={textTaskKey}
        onReviewed={setReviewedTextTaskKey}
        initialDestination={engagement.phone}
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
