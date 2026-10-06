"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import {
  ArrowLeftIcon,
  AudioLinesIcon,
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
import type {
  CallingCall,
  Task,
} from "@/lib/api/generated/types.gen"
import {
  issueRecordingPlayback,
  newestRecoveryInteraction,
  type RecordingKind,
  useRecoverySource,
  useTaskCallEligibility,
} from "@/lib/clients/calls"
import { completeTask, readTask, renameTask, reopenTask } from "@/lib/clients/tasks"
import { formatShortDateTime } from "@/lib/format"
import { formatUSPhone } from "@/lib/phone"
import { automaticAcknowledgementLabel } from "@/lib/task-acknowledgement"

import { TaskGroupContext } from "./task-group-context"
import { TaskMetadata } from "./task-metadata"

type TaskCallContextProps = {
  task: Task | undefined
  group?: Task
  taskRows?: Task[]
  onSelectTask?: (task: Task) => void
  activeCall: CallingCall | undefined
  view: "none" | "task" | "call"
  canMutate: boolean
  historyHint: number
  taskCallError: string
  onTaskUpdated: (task: Task, advance?: boolean) => void
  onReturnToCall: () => void
}

export function TaskCallContext({
  task,
  group,
  taskRows,
  onSelectTask,
  activeCall,
  view,
  canMutate,
  historyHint,
  taskCallError,
  onTaskUpdated,
  onReturnToCall,
}: TaskCallContextProps) {
  const openRecoveryTask = useCallback(
    async (taskID: string) => {
      const outcome = await readTask(taskID)
      if (outcome.ok) onTaskUpdated(outcome.data)
    },
    [onTaskUpdated],
  )
  if (view === "call" && activeCall) {
    return (
      <CallWorkspace
        key={activeCall.id}
        call={activeCall}
        returnTask={task}
        onReturnToTask={task ? () => onTaskUpdated(task) : undefined}
        onOpenRecoveryTask={(taskID) => void openRecoveryTask(taskID)}
      />
    )
  }
  if (view === "task" && task && group && onSelectTask) {
    return <TaskGroupContext key={group.id} group={group} taskRows={taskRows ?? []} canMutate={canMutate} onSelect={onSelectTask} onUpdated={onTaskUpdated} />
  }
  if (view === "task" && task) {
    return (
      <TaskWorkspace
        key={task.id}
        task={task}
        onNextTask={onSelectTask && taskRows?.some((row) => row.id !== task.id)
          ? () => onSelectTask(taskRows.find((row) => row.id !== task.id)!) : undefined}
        activeCall={activeCall}
        canMutate={canMutate}
        historyHint={historyHint}
        taskCallError={taskCallError}
        onTaskUpdated={onTaskUpdated}
        onReturnToCall={onReturnToCall}
      />
    )
  }
  return <section aria-label="No Task selected" className="min-h-0 flex-1" />
}

function TaskWorkspace({
  task,
  onNextTask,
  activeCall,
  canMutate,
  historyHint,
  taskCallError,
  onTaskUpdated,
  onReturnToCall,
}: {
  task: Task
  onNextTask?: () => void
  activeCall: CallingCall | undefined
  canMutate: boolean
  historyHint: number
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
          <h2 className="min-w-0 flex-1 text-lg font-semibold leading-snug tracking-[-0.015em]">
            {task.title}
          </h2>
          {task.state === "OPEN" && canMutate && (
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
      {(task.urgency === "high_priority" || !canMutate) && (
        <div className="mt-3 flex flex-wrap gap-2">
          {task.urgency === "high_priority" && (
            <Badge variant="destructive">High priority</Badge>
          )}
          {!canMutate && <Badge variant="outline">Read only</Badge>}
        </div>
      )}
      {canMutate && <TaskMetadata task={task} onUpdated={acceptUpdate} />}
      {task.sourceMessage && (
        <p className="mt-3 whitespace-pre-wrap text-sm leading-6 text-muted-foreground">
          {task.sourceMessage}
        </p>
      )}
      {task.state === "COMPLETED" && <p role="status" className="mt-3 text-sm text-muted-foreground">Completed by {task.completedBy?.email ?? "the team"}{task.completedAt ? ` · ${formatShortDateTime(task.completedAt)}` : ""}. <span className="block mt-1 text-xs">Completed for everyone with access.</span></p>}
      {needsReview && <div role="status" className="mt-3 rounded-md border p-3 text-sm">
        This Task changed. Review the latest activity before completing it.
        <Button size="sm" variant="outline" className="mt-2" onClick={() => setReviewedVersion(task.version)}>Review latest</Button>
      </div>}
      {canMutate && (
        <div className="mt-4 flex flex-col gap-2">
          {task.state === "OPEN" ? (
            <>
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
              variant="outline"
              onClick={() => void transition("reopen")}
              disabled={pending}
            >
              {pending ? <Spinner /> : <RotateCcwIcon />} Reopen
            </Button>
            </>
          )}
        </div>
      )}
      {taskCallError && (
        <p className="mt-3 text-xs text-destructive">{taskCallError}</p>
      )}
      {error && (
        <Alert variant="destructive" className="mt-3">
          <AlertTitle>Task changed</AlertTitle>
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      {recovery && (
        <RecoveryTaskSource task={task} revision={historyHint} onUpdated={onTaskUpdated} />
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
            value={`${formatShortDateTime(task.createdAt)} · ${actorLabel(task.createdBy)}`}
          />
          {task.automaticAcknowledgement && (
            <Metadata
              label="Caller acknowledgement"
              value={automaticAcknowledgementLabel(
                task.automaticAcknowledgement,
              )}
            />
          )}
          <Metadata label="Last changed" value={formatShortDateTime(task.updatedAt)} />
          <Metadata
            label="Completed"
            value={
              task.completedAt ? formatShortDateTime(task.completedAt) : "Not completed"
            }
          />
        </div>
      </details>
    </section>
  )
}

function TaskSourceDetails({ task }: { task: Task }) {
  return (
    <>
      <Metadata label="Source" value={taskSourceLabel(task)} />
      {task.category && (
        <Metadata label="Category" value={formatCategory(task.category)} />
      )}
      <Metadata label="Urgency" value={formatUrgency(task.urgency)} />
      {task.callerName && (
        <Metadata label="Sourced name" value={task.callerName} />
      )}
    </>
  )
}

function RecoveryTaskSource({
  task,
  revision,
  onUpdated,
}: {
  task: Task
  revision: number
  onUpdated: (task: Task, advance?: boolean) => void
}) {
  const { call, interactions, failure } = useRecoverySource(task, revision)
  const error =
    failure && failure.kind !== "signedOut"
      ? "Recovery source is temporarily unavailable."
      : ""

  const selected = newestRecoveryInteraction(interactions)
  const otherInteractions = [...interactions]
    .sort((left, right) => right.occurredAt.localeCompare(left.occurredAt))
    .filter((interaction) => interaction.callId !== selected?.callId)
  const otherCallOrder = otherInteractions.every(
    (interaction) => interaction.occurredAt < (selected?.occurredAt ?? ""),
  )
    ? "earlier"
    : "other"

  return (
    <section aria-label="Call recovery source" className="mt-4">
      {error && <p className="mt-2 text-sm text-destructive">{error}</p>}
      {call?.voicemail && <VoicemailSource key={call.id} call={call} compact onUpdated={onUpdated} taskID={task.id} />}
      {otherInteractions.length > 0 && (
        <details className="mt-3 text-xs text-muted-foreground">
          <summary className="cursor-pointer">
            {otherInteractions.length} {otherCallOrder}{" "}
            {otherInteractions.length === 1 ? "call" : "calls"}
          </summary>
          <ul className="mt-2 space-y-1 pl-4">
            {otherInteractions.map((interaction) => (
              <li key={interaction.callId}>
                {interaction.type === "VOICEMAIL" ? "Voicemail" : "Missed call"}
                {" · "}
                {formatShortDateTime(interaction.occurredAt)}
              </li>
            ))}
          </ul>
        </details>
      )}
    </section>
  )
}

function VoicemailSource({ call, compact = false, taskID, onUpdated }: {
  call: CallingCall
  compact?: boolean
  taskID?: string
  onUpdated?: (task: Task, advance?: boolean) => void
}) {
  const reviewedTask = useRef<Task | undefined>(undefined)
  const [completionError, setCompletionError] = useState("")
  const voicemail = call.voicemail
  if (!voicemail) return null
  async function captureReview() {
    reviewedTask.current = undefined
    setCompletionError("")
    const id = taskID ?? call.recoveryTask?.id
    if (!id) return
    const result = await readTask(id)
    if (result.ok && result.data.origin === "VOICEMAIL_RECOVERY" && result.data.state === "OPEN") {
      const latestCallID = newestRecoveryInteraction(result.data.interactions)?.callId ?? result.data.callId
      if (latestCallID === call.id) reviewedTask.current = result.data
      else setCompletionError("A newer call needs review. This playback will keep that work open.")
    }
    else if (!result.ok) setCompletionError("Could not load voicemail work. Use Mark done after listening.")
  }
  async function finishReview() {
    const task = reviewedTask.current
    if (!task) return
    reviewedTask.current = undefined
    const result = await completeTask(task)
    if (result.ok) onUpdated?.(result.data)
    else setCompletionError("Could not mark voicemail done. Review any new activity and use Mark done.")
  }
  return <>
    <RecordingSource call={call} audioState={voicemail.audioState} durationSeconds={voicemail.durationSeconds} kind="voicemail" compact={compact} unavailable={voicemail.outcome === "MISSED_CALL"} onBeforePlay={captureReview} onEnded={() => void finishReview()} />
    {completionError && <p role="alert" className="px-5 py-2 text-xs text-destructive">{completionError}</p>}
  </>
}

function CallRecordingSource({ call }: { call: CallingCall }) {
  if (!call.recording) return null
  return (
    <RecordingSource
      call={call}
      audioState={call.recording.audioState}
      durationSeconds={call.recording.durationSeconds}
      kind="call"
    />
  )
}

const recordingPresentation = {
  voicemail: {
    title: "Voicemail",
    label: "voicemail",
    audioLabel: "Voicemail recording",
  },
  call: {
    title: "Call recording",
    label: "call recording",
    audioLabel: "Call recording",
  },
} as const

function RecordingSource({
  call,
  audioState,
  durationSeconds,
  kind,
  compact = false,
  unavailable = false,
  onBeforePlay,
  onEnded,
}: {
  call: CallingCall
  audioState: "PROCESSING" | "READY" | "UNAVAILABLE" | "EXPIRED" | "DELETED" | undefined
  durationSeconds: number
  kind: RecordingKind
  compact?: boolean
  unavailable?: boolean
  onBeforePlay?: () => Promise<void>
  onEnded?: () => void
}) {
  const [audioURL, setAudioURL] = useState("")
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState("")
  const audioRef = useRef<HTMLAudioElement>(null)
  const presentation = recordingPresentation[kind]

  useEffect(() => {
    if (audioURL) void audioRef.current?.play().catch(() => undefined)
  }, [audioURL])

  const stateLabel =
    unavailable
      ? "No voicemail recording."
      : audioState === "READY"
        ? "Ready"
        : audioState === "EXPIRED" || audioState === "DELETED"
          ? "Recording expired"
        : audioState === "UNAVAILABLE"
          ? "Recording unavailable"
          : "Processing"

  async function loadAudio() {
    setLoading(true)
    setError("")
    await onBeforePlay?.()
    const playback = await issueRecordingPlayback(kind, call.id)
    setLoading(false)
    if (playback.ok) setAudioURL(playback.data)
    else if (playback.failure.kind !== "signedOut") {
      setError("Playback authorization is unavailable.")
    }
  }

  return (
    <section className={compact ? "mt-4" : "border-t px-5 py-4"}>
      <div className="flex items-start gap-3">
        <AudioLinesIcon className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
        <div className="min-w-0 flex-1">
          <h2 className="text-sm font-semibold">{presentation.title}</h2>
          <p className="mt-0.5 text-xs text-muted-foreground">
            {stateLabel}
            {durationSeconds > 0 && ` · ${formatDuration(durationSeconds)}`}
          </p>
        </div>
        {audioState === "READY" && !audioURL && (
          <Button
            size="sm"
            variant="outline"
            disabled={loading}
            onClick={() => void loadAudio()}
          >
            {loading ? <Spinner /> : "Play"}
          </Button>
        )}
      </div>
      {audioURL && (
        <audio
          ref={audioRef}
          aria-label={presentation.audioLabel}
          controls
          controlsList="nodownload"
          preload="metadata"
          src={audioURL}
          onEnded={onEnded}
          onError={() => setError(`The ${presentation.label} could not be opened.`)}
          className="mt-3 h-9 max-w-full"
        />
      )}
      {error && <p className="mt-2 text-xs text-destructive">{error}</p>}
    </section>
  )
}

function actorLabel(actor: Task["createdBy"]) {
  if (actor.email) return actor.email
  return actor.kind === "SERVICE" ? "Abita AI" : actor.subject
}

function formatCategory(category: NonNullable<Task["category"]>) {
  return category.charAt(0).toUpperCase() + category.slice(1)
}

function formatUrgency(urgency: Task["urgency"]) {
  switch (urgency) {
    case "high_priority":
      return "High priority"
    case "non_urgent":
      return "Non-urgent"
    default:
      return "Normal"
  }
}

function taskSourceLabel(task: Task) {
  switch (task.origin) {
    case "ABITA_AI":
      return "Created by AI"
    case "APPOINTMENT_REVIEW":
      return "Appointment verification"
    case "INBOUND_MESSAGE_REVIEW":
      return "Incoming text"
    case "STAFF_MESSAGE_FOLLOW_UP":
      return "Message follow-up"
    case "VOICEMAIL_RECOVERY":
      return "Voicemail follow-up"
    case "MISSED_CALL_RECOVERY":
      return "Missed-call follow-up"
    default:
      return "Call follow-up"
  }
}

function CallWorkspace({
  call,
  returnTask,
  onReturnToTask,
  onOpenRecoveryTask,
}: {
  call: CallingCall
  returnTask: Task | undefined
  onReturnToTask: (() => void) | undefined
  onOpenRecoveryTask: (taskID: string) => void
}) {
  const formattedPhone = formatUSPhone(call.phone)
  const contactName =
    call.displayName &&
    call.displayName !== call.phone &&
    call.displayName !== formattedPhone
      ? call.displayName
      : ""
  const taskAction = call.recoveryTask
    ? {
        label: "Open Task",
        onClick: () => onOpenRecoveryTask(call.recoveryTask!.id),
      }
    : returnTask && onReturnToTask
      ? { label: "Back to Task", onClick: onReturnToTask }
      : undefined
  return (
    <section className="flex min-h-0 flex-1 flex-col">
      <header className="px-5 py-5 pr-12">
        <h2 className="text-lg font-semibold tracking-[-0.015em]">
          {callContextTitle(call.state)}
        </h2>
        {contactName && <p className="mt-3 font-medium">{contactName}</p>}
        <p className="mt-1 text-sm tabular-nums text-muted-foreground">
          {formattedPhone} · {call.locationName}
        </p>
        {call.transferReason && (
          <p className="mt-3 text-sm leading-6">{call.transferReason}</p>
        )}
        {call.recoveryTask && (
          <p className="mt-2 text-xs text-muted-foreground">
            {call.recoveryTask.title}
          </p>
        )}
        {taskAction && (
          <Button className="mt-4 w-full" variant="outline" onClick={taskAction.onClick}>
            {taskAction.label === "Back to Task" && <ArrowLeftIcon />}
            {taskAction.label}
          </Button>
        )}
      </header>
      {call.voicemail && <VoicemailSource call={call} />}
      {call.recording && <CallRecordingSource call={call} />}
      <details className="group border-t px-5 py-4">
        <summary className="cursor-pointer text-sm font-medium">Details</summary>
        <div className="mt-3 flex flex-col gap-3">
          <Metadata label="Direction" value={formatDirection(call.direction)} />
          <Metadata label="Started from" value={formatEntryPoint(call.entryPoint)} />
          {call.connectedAt && (
            <Metadata label="Connected" value={formatShortDateTime(call.connectedAt)} />
          )}
        </div>
      </details>
    </section>
  )
}

function Metadata({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0">
      <span className="block text-xs font-medium">
        {label}
      </span>
      <span className="mt-1 block truncate text-foreground">{value}</span>
    </div>
  )
}

function formatDuration(seconds: number) {
  const minutes = Math.floor(seconds / 60)
  const remainder = seconds % 60
  return `${minutes}:${String(remainder).padStart(2, "0")}`
}

function callContextTitle(state: CallingCall["state"]) {
  switch (state) {
    case "PREPARING":
      return "Preparing call"
    case "RINGING":
      return "Calling"
    case "CONNECTING":
      return "Connecting"
    case "CONNECTED":
      return "Call connected"
    case "VOICEMAIL_GREETING":
    case "VOICEMAIL_RECORDING":
      return "Leaving voicemail"
    case "UNANSWERED":
      return "Unanswered call"
    case "VOICEMAIL":
      return "Voicemail"
    case "MISSED":
      return "Missed call"
    case "NEEDS_DISPOSITION":
      return "Call ended"
    case "FOLLOW_UP_REQUIRED":
      return "Follow-up required"
    default:
      return "Call resolved"
  }
}

function formatDirection(direction: CallingCall["direction"]) {
  return direction === "INBOUND" ? "Inbound call" : "Outbound call"
}

function formatEntryPoint(entryPoint: CallingCall["entryPoint"]) {
  switch (entryPoint) {
    case "AI_HANDOFF":
      return "AI handoff"
    case "TASK":
      return "Task"
    default:
      return "Phone number"
  }
}

export function TaskCallAction({ task, canCall, historyHint, pending, onCall }: {
  task: Task
  canCall: boolean
  historyHint: number
  pending: boolean
  onCall: (task: Task) => void
}) {
  const eligibility = useTaskCallEligibility({ task, historyHint, enabled: canCall })
  const answer =
    eligibility.status === "loading" ||
    (eligibility.status === "failed" && eligibility.failure.kind === "signedOut")
      ? undefined
      : eligibility
  const taskCallingEligible = canCall && answer?.status === "ready" && answer.data.eligible
  const taskCallingReason = !canCall
    ? "Calling is not enabled for this account."
    : !answer
      ? "Checking Call route…"
      : answer.status === "ready"
        ? answer.data.reason
        : "Call eligibility is temporarily unavailable."

  return <Button variant="outline" size="sm" className="h-7 shadow-xs" disabled={!taskCallingEligible || pending} title={taskCallingEligible ? "Call this Task" : taskCallingReason} onClick={() => onCall(task)}>
    {pending ? <Spinner /> : <PhoneCallIcon />} {pending ? "Preparing…" : "Call"}
  </Button>
}
