"use client"

import { useRef, useState } from "react"
import {
  CheckIcon,
  CheckSquareIcon,
  CopyIcon,
  RefreshCwIcon,
} from "lucide-react"

import { Bubble, BubbleContent } from "@/components/ui/bubble"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from "@/components/ui/dialog"
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
  onChanged,
  onTaskCreated,
}: {
  message: Message
  showLocation: boolean
  onChanged: () => void
  onTaskCreated: (task: Task) => void
}) {
  const [pending, setPending] = useState(false)
  const [error, setError] = useState("")
  const [copyState, setCopyState] = useCopyState()
  const [selected, setSelected] = useState(false)
  const [confirmingSendAgain, setConfirmingSendAgain] = useState(false)
  const sendAgainAttemptKey = useRef("")
  const outbound = message.direction === "OUTBOUND"
  const sentBy = message.createdBy?.kind === "SERVICE"
    ? "Automated"
    : outbound ? message.createdBy?.email : undefined

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
    setConfirmingSendAgain(false)
    setPending(true)
    setError("")
    if (!sendAgainAttemptKey.current) {
      sendAgainAttemptKey.current = crypto.randomUUID()
    }
    const outcome = await sendMessageAgain(message, {
      idempotencyKey: sendAgainAttemptKey.current,
      duplicateRiskAcknowledged: true,
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

  const deliveryProblem =
    outbound &&
    (message.delivery === "Failed" || message.delivery === "Status unknown")

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
      <MessageContent className="gap-1">
        <Bubble variant={outbound ? "muted" : "ghost"}>
          <BubbleContent>
            {message.body && (
              <p className="whitespace-pre-wrap">{message.body}</p>
            )}
            {message.attachment && (
              <MessageAttachmentView
                attachment={message.attachment}
                onChanged={onChanged}
              />
            )}
          </BubbleContent>
        </Bubble>
        <MessageFooter
          className={cn(
            "min-h-5 flex-wrap gap-x-1.5 gap-y-1 font-normal tabular-nums transition-opacity duration-150 motion-reduce:transition-none",
            !deliveryProblem && !pending && !error &&
              "pointer-events-none opacity-0 group-focus-within/message:pointer-events-auto group-focus-within/message:opacity-100 group-hover/message:pointer-events-auto group-hover/message:opacity-100",
            selected && "pointer-events-auto opacity-100",
          )}
        >
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
              <span className={cn(deliveryProblem && "text-destructive")}>
                {message.delivery}
              </span>
            </>
          )}
          {sentBy && (
            <>
              <span aria-hidden="true">·</span>
              <span>{sentBy}</span>
            </>
          )}
          {message.safeFailureCode && (
            <>
              <span aria-hidden="true">·</span>
              <span>{message.safeFailureCode}</span>
            </>
          )}
          {message.body && (
            <Tooltip>
              <TooltipTrigger
                render={
                  <Button
                    type="button"
                    size="icon-xs"
                    variant="ghost"
                    aria-label="Copy message"
                    onClick={copyMessage}
                  />
                }
              >
                {copyState === "copied" ? (
                  <CheckIcon className="size-3" />
                ) : (
                  <CopyIcon className="size-3" />
                )}
              </TooltipTrigger>
              <TooltipContent data-testid="copy-message-tooltip">
                {copyState === "copied" ? "Copied" : "Copy message"}
              </TooltipContent>
            </Tooltip>
          )}
          {!message.taskId && (
            <Tooltip>
              <TooltipTrigger
                render={
                  <Button
                    type="button"
                    size="icon-xs"
                    variant="ghost"
                    aria-label="Create task"
                    disabled={pending}
                    onClick={() => void createTask()}
                  />
                }
              >
                <CheckSquareIcon className="size-3" />
              </TooltipTrigger>
              <TooltipContent>Create task</TooltipContent>
            </Tooltip>
          )}
          {deliveryProblem && (
            <Button
              type="button"
              size="xs"
              variant="ghost"
              disabled={pending}
              onClick={() =>
                message.delivery === "Status unknown"
                  ? setConfirmingSendAgain(true)
                  : void sendAgain()
              }
            >
              <RefreshCwIcon data-icon="inline-start" />
              Send again
            </Button>
          )}
          {pending && <Spinner />}
          {error && (
            <p role="alert" className="basis-full text-destructive">
              {error}
            </p>
          )}
        </MessageFooter>
        <span role="status" className="sr-only">
          {copyState === "copied"
            ? "Message copied"
            : copyState === "failed"
              ? "Message could not be copied"
              : ""}
        </span>
      </MessageContent>
      <Dialog open={confirmingSendAgain} onOpenChange={setConfirmingSendAgain}>
        <DialogContent>
          <DialogTitle>Send this text again?</DialogTitle>
          <DialogDescription>
            The carrier never confirmed whether the first text arrived. Sending
            again may deliver a duplicate.
          </DialogDescription>
          <div className="flex justify-end gap-2">
            <Button
              type="button"
              variant="outline"
              onClick={() => setConfirmingSendAgain(false)}
            >
              Cancel
            </Button>
            <Button type="button" onClick={() => void sendAgain()}>
              Send again
            </Button>
          </div>
        </DialogContent>
      </Dialog>
    </MessageRow>
  )
}
