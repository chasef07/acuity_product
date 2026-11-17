"use client"

import { useEffect, useRef, useState } from "react"
import {
  CheckIcon,
  EllipsisIcon,
  PencilIcon,
  PhoneCallIcon,
  XIcon,
} from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import { useSettledState } from "@/hooks/use-settled-state"
import type {
  CallingCall,
  ConversationTimelineItem,
  StaffTaskCloseReason,
  Task,
} from "@/lib/api/generated/types.gen"
import { completeTask, readTask, renameTask, reopenTask } from "@/lib/clients/tasks"
import { formatTimestamp } from "@/lib/format-time"
import { taskCloseSummary, taskUrgencyLabels } from "@/lib/vocabulary"
import { cn } from "@/lib/utils"
import { automaticAcknowledgementLabel } from "@/lib/task-acknowledgement"
import {
  callbackCompletionLabel,
  openTaskCallbackNotice,
} from "@/lib/workspace-history"

import { offersTaskCloseMenu, TaskCloseMenu } from "./task-close-menu"
import { TaskMetadata } from "./task-metadata"
import { RecoveryTaskSource } from "./recording-source"
import { ClosedTaskActions, TaskCheck } from "./task-check"
import { actorLabel, Metadata, TaskSourceDetails } from "./task-details"

export function TaskWorkspace({
  task,
  onNextTask,
  activeCall,
  numberHistory = [],
  onTaskUpdated,
  onReturnToCall,
}: {
  task: Task
  onNextTask?: () => void
  activeCall: CallingCall | undefined
  numberHistory?: ConversationTimelineItem[]
  onTaskUpdated: (task: Task) => void
  onReturnToCall: () => void
}) {
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(task.title)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState("")
  const renameButton = useRef<HTMLButtonElement>(null)
  const restoreRenameFocus = useRef(false)

  useEffect(() => {
    if (editing || !restoreRenameFocus.current) return
    restoreRenameFocus.current = false
    renameButton.current?.focus()
  }, [editing])

  function openTitleEditor() {
    setDraft(task.title)
    setError("")
    setEditing(true)
  }

  function closeTitleEditor() {
    restoreRenameFocus.current = true
    setError("")
    setEditing(false)
  }

  const [reviewedVersion, setReviewedVersion] = useState(task.version)
  const stateSettled = useSettledState(task.state)
  const needsReview = task.origin === "APPOINTMENT_REVIEW" && task.state === "OPEN" && reviewedVersion !== task.version
  function acceptUpdate(updated: Task) {
    setReviewedVersion(updated.version)
    onTaskUpdated(updated)
  }

  async function refreshTask() {
    const latest = await readTask(task.id)
    if (latest.ok) onTaskUpdated(latest.data)
  }

  async function saveTitle() {
    const attempted = draft.trim()
    if (!attempted || attempted === task.title) {
      closeTitleEditor()
      return
    }
    setPending(true)
    setError("")
    const outcome = await renameTask(task, attempted)
    setPending(false)
    if (outcome.ok) {
      acceptUpdate(outcome.data)
      closeTitleEditor()
      return
    }
    if (outcome.failure.kind === "signedOut") return
    if (outcome.failure.kind === "conflict") {
      await refreshTask()
      setDraft(attempted)
      setEditing(true)
      setError(
        "This Task changed elsewhere. The latest version is loaded; your title is still here to retry.",
      )
      return
    }
    setError("The title could not be saved.")
  }

  async function transition(closeReason?: StaffTaskCloseReason) {
    if (pending || (closeReason && needsReview)) return
    if (!closeReason && !stateSettled) return
    setPending(true)
    setError("")
    const outcome = await (closeReason ? completeTask(task, closeReason) : reopenTask(task))
    setPending(false)
    if (outcome.ok) {
      acceptUpdate(outcome.data)
      return
    }
    if (outcome.failure.kind === "signedOut") return
    if (outcome.failure.kind === "conflict") {
      await refreshTask()
      setError("This Task changed elsewhere. The latest state is now loaded.")
      return
    }
    setError(`The Task could not be ${closeReason ? "completed" : "reopened"}.`)
  }
  const recovery =
    task.origin === "VOICEMAIL_RECOVERY" ||
    task.origin === "MISSED_CALL_RECOVERY"
  const completion = callbackCompletionLabel(task) ??
    taskCloseSummary(task) ??
    `Completed by ${task.completedBy?.email ?? "the team"}`
  const callbackNotice = openTaskCallbackNotice(numberHistory, task, formatTimestamp)

  const completeLabel = task.origin === "APPOINTMENT_REVIEW" ? "Verify" : recovery ? "Mark done" : "Complete"
  const open = task.state === "OPEN"

  return (
    <section
      aria-label="Focused Task"
      className="group/task flex flex-col rounded-lg bg-muted/60 px-3 py-2.5"
    >
      <header className="flex items-start gap-2">
        <TaskCheck
          open={open}
          label={completeLabel}
          title={needsReview ? "Review the latest activity first" : completeLabel}
          pending={pending}
          blocked={needsReview}
          onComplete={() => void transition("DONE")}
        />
        {editing && open ? (
          <div className="flex min-w-0 flex-1 items-center gap-1">
            <Input
              aria-label="Task title"
              autoFocus
              maxLength={500}
              value={draft}
              disabled={pending}
              className="h-7"
              onChange={(event) => setDraft(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === "Enter") void saveTitle()
                if (event.key === "Escape") closeTitleEditor()
              }}
            />
            <Button size="icon-sm" aria-label="Save title" onClick={() => void saveTitle()}>
              {pending ? <Spinner /> : <CheckIcon />}
            </Button>
            <Button size="icon-sm" variant="ghost" aria-label="Cancel rename" onClick={closeTitleEditor}>
              <XIcon />
            </Button>
          </div>
        ) : (
          <>
            <h2 className={cn("min-w-0 flex-1 text-sm font-medium leading-5", !open && "text-muted-foreground line-through")}>
              {task.title}
            </h2>
            {open && (
              <div className="flex shrink-0 items-center [@media(hover:hover)]:opacity-0 group-hover/task:opacity-100 group-focus-within/task:opacity-100">
                <Button
                  ref={renameButton}
                  variant="ghost"
                  size="icon-xs"
                  aria-label="Rename task"
                  className="text-muted-foreground"
                  onClick={openTitleEditor}
                >
                  <PencilIcon />
                </Button>
                {offersTaskCloseMenu(task) && (
                  <TaskCloseMenu
                    trigger={
                      <Button
                        variant="ghost"
                        size="icon-xs"
                        aria-label="Close as"
                        className="text-muted-foreground"
                        disabled={pending || needsReview}
                      />
                    }
                    onClose={(reason) => void transition(reason)}
                  >
                    <EllipsisIcon />
                  </TaskCloseMenu>
                )}
              </div>
            )}
          </>
        )}
      </header>
      <div className="pl-7">
        <div className="flex flex-wrap items-center gap-x-2 text-xs text-muted-foreground">
          {task.urgency === "high_priority" && (
            <span className="text-destructive">{taskUrgencyLabels.high_priority}</span>
          )}
          <TaskMetadata task={task} onUpdated={acceptUpdate} />
        </div>
        {task.sourceMessage && (
          <p className="mt-1.5 whitespace-pre-wrap text-sm leading-6 text-muted-foreground">
            {task.sourceMessage}
          </p>
        )}
        <div data-testid="task-actions" className="flex flex-col gap-2 empty:hidden [&:not(:empty)]:mt-2">
          {needsReview && (
            <p role="status" className="text-xs leading-5 text-muted-foreground">
              This Task changed. Review the latest activity before completing it.{" "}
              <button type="button" className="font-medium text-foreground underline underline-offset-2" onClick={() => setReviewedVersion(task.version)}>
                Review latest
              </button>
            </p>
          )}
          {open && callbackNotice && (
            <p role="status" className="text-xs leading-5 text-muted-foreground">{callbackNotice}</p>
          )}
          {open && activeCall && (
            <Button size="sm" variant="outline" className="self-start" onClick={onReturnToCall}>
              <PhoneCallIcon /> Return to active call
            </Button>
          )}
          {!open && (
            <>
              <p role="status" className="text-xs leading-5 text-muted-foreground">
                {completion}{task.completedAt ? ` · ${formatTimestamp(task.completedAt)}` : ""}. Completed for everyone with access.
              </p>
              <ClosedTaskActions
                pending={pending}
                settled={stateSettled}
                onNext={onNextTask}
                onReopen={() => void transition()}
              />
            </>
          )}
        </div>
        {error && (
          <Alert variant="destructive" className="mt-2">
            <AlertTitle>Task changed</AlertTitle>
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        {(recovery || task.relatedInteractionCount > (task.callId ? 1 : 0)) && (
          <RecoveryTaskSource task={task} onUpdated={onTaskUpdated} />
        )}
        <details className="group mt-2">
          <summary className="cursor-pointer text-xs text-muted-foreground hover:text-foreground">
            Details
          </summary>
          <div className="mt-2 flex flex-col gap-2">
            <TaskSourceDetails task={task} />
            {task.sourceCallId && (
              <Metadata label="Source call" value={task.sourceCallId} />
            )}
            <Metadata
              label="Created"
              value={`${formatTimestamp(task.createdAt)} · ${actorLabel(task.createdBy)}`}
            />
            {task.automaticAcknowledgement && (
              <Metadata
                label="Caller acknowledgement"
                value={automaticAcknowledgementLabel(
                  task.automaticAcknowledgement,
                )}
              />
            )}
            <Metadata label="Last changed" value={formatTimestamp(task.updatedAt)} />
            <Metadata
              label="Completed"
              value={
                task.completedAt ? formatTimestamp(task.completedAt) : "Not completed"
              }
            />
          </div>
        </details>
      </div>
    </section>
  )
}
