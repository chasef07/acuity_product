"use client"

import { useRef, useState } from "react"
import {
  AlertTriangleIcon,
  CheckIcon,
  CheckSquareIcon,
  CopyIcon,
  EllipsisIcon,
  RefreshCwIcon,
} from "lucide-react"

import { Bubble, BubbleContent } from "@/components/ui/bubble"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import {
  Message as MessageRow,
  MessageContent,
  MessageFooter,
} from "@/components/ui/message"
import { Spinner } from "@/components/ui/spinner"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import type { Message, Task } from "@/lib/api/generated/types.gen"
import { createFollowUpTask, sendMessageAgain } from "@/lib/clients/engagement"
import { formatClockTime } from "@/lib/format-time"
import { cn } from "@/lib/utils"

import { MessageAttachmentView } from "./message-attachment"
import { useCopyState } from "@/hooks/use-copy-state"

export function MessageEntry({
  message,
  showLocation,
  canMutate,
  onChanged,
  onTaskCreated,
}: {
  message: Message
  showLocation: boolean
  canMutate: boolean
  onChanged: () => void
  onTaskCreated: (task: Task) => void
}) {
  const [pending, setPending] = useState(false)
  const [error, setError] = useState("")
  const [copyState, setCopyState] = useCopyState()
  const [selected, setSelected] = useState(false)
  const sendAgainAttemptKey = useRef("")
  const outbound = message.direction === "OUTBOUND"

  async function createTask() {
    setPending(true)
    setError("")
    const outcome = await createFollowUpTask(message)
    setPending(false)
    if (!outcome.ok) {
      setError("A follow-up Task could not be created.")
      return
    }
    onTaskCreated(outcome.data)
    onChanged()
  }

  async function sendAgain() {
    const duplicateRisk =
      message.delivery === "Status unknown"
        ? window.confirm(
            "The provider outcome is unknown. Sending again may deliver a duplicate. Continue?",
          )
        : true
    if (!duplicateRisk) return
    setPending(true)
    setError("")
    if (!sendAgainAttemptKey.current) {
      sendAgainAttemptKey.current = crypto.randomUUID()
    }
    const outcome = await sendMessageAgain(message, {
      idempotencyKey: sendAgainAttemptKey.current,
      duplicateRiskAcknowledged: duplicateRisk,
    })
    setPending(false)
    if (!outcome.ok) {
      setError("A new send attempt could not be created.")
      return
    }
    sendAgainAttemptKey.current = ""
    onChanged()
  }

  function copyMessage() {
    setCopyState("idle")
    void navigator.clipboard.writeText(message.body).then(
      () => setCopyState("copied"),
      () => setCopyState("failed"),
    )
  }

  const actionClassName = cn(
    "sm:opacity-0 sm:group-focus-within/message:opacity-100 sm:group-hover/message:opacity-100 motion-reduce:duration-0 motion-reduce:transition-none",
    selected && "sm:opacity-100",
  )

  return (
    <MessageRow
      role="article"
      tabIndex={0}
      align={outbound ? "end" : "start"}
      className="rounded-md outline-none focus-visible:ring-2 focus-visible:ring-ring/30"
      onPointerUp={() => setSelected(Boolean(window.getSelection()?.toString()))}
      onBlur={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget)) setSelected(false)
      }}
    >
      <MessageContent>
        <Bubble variant={outbound ? "muted" : "ghost"}>
          <BubbleContent>
            {message.body && (
              <p className="whitespace-pre-wrap">{message.body}</p>
            )}
            {message.attachment && (
              <MessageAttachmentView
                attachment={message.attachment}
                canMutate={canMutate}
                onChanged={onChanged}
              />
            )}
          </BubbleContent>
        </Bubble>
        <MessageFooter className={cn("flex-wrap gap-x-2 gap-y-1 tabular-nums", !message.safeFailureCode && message.delivery !== "Failed" && message.delivery !== "Status unknown" && actionClassName)}>
          <time dateTime={message.createdAt}>
            {formatClockTime(message.createdAt)}
          </time>
          {showLocation && (
            <>
              <span aria-hidden="true">·</span>
              <span>{message.thread.locationName}</span>
            </>
          )}
          {outbound && (
            <>
              <span aria-hidden="true">·</span>
              <span>{message.delivery}</span>
            </>
          )}
          {message.createdBy?.kind === "SERVICE" && (
            <>
              <span aria-hidden="true">·</span>
              <span>Automated</span>
            </>
          )}
          {message.safeFailureCode && (
            <>
              <span aria-hidden="true">·</span>
              <span>{message.safeFailureCode}</span>
            </>
          )}
          <span className="flex items-center gap-0.5">
            {message.body && (
              <Tooltip>
                <TooltipTrigger
                  render={
                    <Button
                      type="button"
                      size="icon-sm"
                      variant="ghost"
                      aria-label="Copy message"
                      className={actionClassName}
                      onClick={copyMessage}
                    />
                  }
                >
                  {copyState === "copied" ? (
                    <CheckIcon className="size-3.5" />
                  ) : (
                    <CopyIcon className="size-3.5" />
                  )}
                </TooltipTrigger>
                <TooltipContent
                  data-testid="copy-message-tooltip"
                  className="bg-black text-white"
                  arrowClassName="bg-black fill-black"
                >
                  Copy message
                </TooltipContent>
              </Tooltip>
            )}
            {canMutate && !message.taskId && (
              <Tooltip>
                <TooltipTrigger
                  render={
                    <Button
                      type="button"
                      size="icon-sm"
                      variant="ghost"
                      aria-label="Create task"
                      className={actionClassName}
                      disabled={pending}
                      onClick={() => void createTask()}
                    />
                  }
                >
                  <CheckSquareIcon className="size-3.5" />
                </TooltipTrigger>
                <TooltipContent
                  className="bg-black text-white"
                  arrowClassName="bg-black fill-black"
                >
                  Create task
                </TooltipContent>
              </Tooltip>
            )}
            {canMutate &&
              outbound &&
              (message.delivery === "Failed" ||
                message.delivery === "Status unknown") && (
                <DropdownMenu>
                  <DropdownMenuTrigger
                    render={
                      <Button
                        type="button"
                        size="icon-sm"
                        variant="ghost"
                        aria-label="More message actions"
                        className={actionClassName}
                      />
                    }
                  >
                    <EllipsisIcon />
                  </DropdownMenuTrigger>
                  <DropdownMenuContent align={outbound ? "end" : "start"}>
                    <DropdownMenuGroup>
                      <DropdownMenuItem
                        disabled={pending}
                        onClick={() => void sendAgain()}
                      >
                        {message.delivery === "Status unknown" ? (
                          <AlertTriangleIcon />
                        ) : (
                          <RefreshCwIcon />
                        )}
                        Send again
                      </DropdownMenuItem>
                    </DropdownMenuGroup>
                  </DropdownMenuContent>
                </DropdownMenu>
              )}
            {pending && <Spinner />}
          </span>
          {error && (
            <p role="alert" className="basis-full text-destructive">
              {error}
            </p>
          )}
          <span
            role="status"
            className="sr-only"
          >
            {copyState === "copied"
              ? "Message copied"
              : copyState === "failed"
                ? "Message could not be copied"
                : ""}
          </span>
        </MessageFooter>
      </MessageContent>
    </MessageRow>
  )
}
