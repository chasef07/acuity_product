"use client"

import { useEffect, useRef, useState } from "react"
import {
  CheckIcon,
  CheckCircle2Icon,
  PencilIcon,
  PhoneCallIcon,
  RotateCcwIcon,
  XIcon,
} from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import { useSettledState } from "@/hooks/use-settled-state"
import type {
  CallingCall,
  ConversationTimelineItem,
  Task,
} from "@/lib/api/generated/types.gen"
import { completeTask, readTask, renameTask, reopenTask } from "@/lib/clients/tasks"
import { formatTimestamp } from "@/lib/format-time"
import { taskUrgencyLabels } from "@/lib/vocabulary"
import { cn } from "@/lib/utils"
import { automaticAcknowledgementLabel } from "@/lib/task-acknowledgement"
import {
  callbackCompletionLabel,
  openTaskCallbackNotice,
} from "@/lib/workspace-history"

import { TaskMetadata } from "./task-metadata"
import { RecoveryTaskSource } from "./recording-source"
import { actorLabel, Metadata, TaskSourceDetails } from "./task-details"

export function TaskWorkspace({
  task,
  onNextTask,
  activeCall,
  numberHistory = [],
  taskCallError,
  onTaskUpdated,
  onReturnToCall,
}: {
  task: Task
  onNextTask?: () => void
  activeCall: CallingCall | undefined
  numberHistory?: ConversationTimelineItem[]
  taskCallError: string
  onTaskUpdated: (task: Task, advance?: boolean) => void
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
  const reviewRequired = task.origin === "APPOINTMENT_REVIEW" || task.origin === "INBOUND_MESSAGE_REVIEW"
  const needsReview = reviewRequired && task.state === "OPEN" && reviewedVersion !== task.version
  function acceptUpdate(updated: Task, advance = false) {
    setReviewedVersion(updated.version)
    onTaskUpdated(updated, advance)
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

  async function transition(action: "complete" | "reopen") {
    if (pending || (action === "complete" && needsReview)) return
    if (action === "reopen" && !stateSettled) return
    setPending(true)
    setError("")
    const outcome = await (action === "complete" ? completeTask : reopenTask)(task)
    setPending(false)
    if (outcome.ok) {
      acceptUpdate(outcome.data, action === "complete")
      return
    }
    if (outcome.failure.kind === "signedOut") return
    if (outcome.failure.kind === "conflict") {
      await refreshTask()
      setError("This Task changed elsewhere. The latest state is now loaded.")
      return
    }
    setError(`The Task could not be ${action === "complete" ? "completed" : "reopened"}.`)
  }
  const recovery =
    task.origin === "VOICEMAIL_RECOVERY" ||
    task.origin === "MISSED_CALL_RECOVERY"
  const completion = callbackCompletionLabel(numberHistory, task) ??
    `Completed by ${task.completedBy?.email ?? "the team"}`
  const callbackNotice = openTaskCallbackNotice(numberHistory, task, formatTimestamp)

  return (
    <section
      aria-label="Focused Task"
      className="h-full min-h-0 flex-1 overflow-y-auto bg-transparent px-5 py-5"
    >
      {editing && task.state === "OPEN" ? (
        <div className="flex items-center gap-1">
          <Input
            aria-label="Task title"
            autoFocus
            maxLength={500}
            value={draft}
            disabled={pending}
            onChange={(event) => setDraft(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter") void saveTitle()
              if (event.key === "Escape") closeTitleEditor()
            }}
          />
          <Button
            size="icon"
            aria-label="Save title"
            onClick={() => void saveTitle()}
          >
            {pending ? <Spinner /> : <CheckIcon />}
          </Button>
          <Button
            size="icon"
            variant="ghost"
            aria-label="Cancel rename"
            onClick={closeTitleEditor}
          >
            <XIcon />
          </Button>
        </div>
      ) : (
        <div className="flex items-start gap-1 pr-8">
          <h2 className="min-w-0 flex-1 text-base font-medium leading-snug">
            {task.title}
          </h2>
          {task.state === "OPEN" && (
            <Button
              ref={renameButton}
              variant="ghost"
              size="icon"
              aria-label="Rename task"
              onClick={openTitleEditor}
            >
              <PencilIcon />
            </Button>
          )}
        </div>
      )}
      {task.urgency === "high_priority" && (
        <div className="mt-3 flex flex-wrap gap-2">
          <Badge variant="destructive">{taskUrgencyLabels.high_priority}</Badge>
        </div>
      )}
      <TaskMetadata task={task} onUpdated={acceptUpdate} />
      {task.sourceMessage && (
        <p className="mt-3 whitespace-pre-wrap text-sm leading-6 text-muted-foreground">
          {task.sourceMessage}
        </p>
      )}
      {task.state === "COMPLETED" && <p role="status" className="mt-3 text-sm text-muted-foreground">{completion}{task.completedAt ? ` · ${formatTimestamp(task.completedAt)}` : ""}. <span className="block mt-1 text-xs">Completed for everyone with access.</span></p>}
      {needsReview && <div role="status" className="mt-3 rounded-md border p-3 text-sm">
        This Task changed. Review the latest activity before completing it.
        <Button size="sm" variant="outline" className="mt-2" onClick={() => setReviewedVersion(task.version)}>Review latest</Button>
      </div>}
      <div className="mt-4 flex flex-col gap-2">
        {task.state === "OPEN" ? (
          <>
            {callbackNotice && (
              <p role="status" className={cn("text-xs leading-5 text-muted-foreground", recovery && "order-2")}>
                {callbackNotice}
              </p>
            )}
            <Button
              variant={recovery ? "ghost" : "default"}
              className={recovery ? "order-2" : undefined}
              onClick={() => void transition("complete")}
              disabled={pending || needsReview}
            >
              {pending ? <Spinner /> : <CheckCircle2Icon />} {task.origin === "APPOINTMENT_REVIEW" ? "Verify & next" : recovery ? "Mark done" : "Complete & next"}
            </Button>
            {activeCall ? (
              <Button variant="outline" onClick={onReturnToCall}>
                <PhoneCallIcon /> Return to active call
              </Button>
            ) : null}
          </>
        ) : (
          <>
          {onNextTask && <Button onClick={onNextTask}>Next task</Button>}
          <Button
            variant="ghost"
            size="sm"
            className="mt-2 self-start text-muted-foreground"
            onClick={() => void transition("reopen")}
            disabled={pending || !stateSettled}
          >
            {pending ? <Spinner /> : <RotateCcwIcon />} Reopen
          </Button>
          </>
        )}
      </div>
      {taskCallError && (
        <p className="mt-3 text-xs text-destructive">{taskCallError}</p>
      )}
      {error && (
        <Alert variant="destructive" className="mt-3">
          <AlertTitle>Task changed</AlertTitle>
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      {(recovery || task.relatedInteractionCount > (task.callId ? 1 : 0)) && (
        <RecoveryTaskSource task={task} onUpdated={onTaskUpdated} />
      )}
      <details className="group mt-4 border-t pt-4">
        <summary className="cursor-pointer text-sm font-medium">
          Details
        </summary>
        <div className="mt-3 flex flex-col gap-3">
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
    </section>
  )
}
