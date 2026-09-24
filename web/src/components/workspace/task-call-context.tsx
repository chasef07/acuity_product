"use client"

import { useCallback, useEffect, useRef, useState, type ReactNode } from "react"
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
import { portalAPIURL, portalClient } from "@/lib/api/client"
import {
  completeTask,
  getCallingCall,
  getTaskOutboundEligibility,
  issueCallingRecordingPlayback,
  issueCallingVoicemailPlayback,
  readTask,
  renameTask,
  reopenTask,
} from "@/lib/api/generated/sdk.gen"
import type {
  CallingCall,
  Task,
} from "@/lib/api/generated/types.gen"
import { getAccessToken } from "@/lib/auth-client"
import { formatUSPhone } from "@/lib/phone"
import { automaticAcknowledgementLabel } from "@/lib/task-acknowledgement"

import { TaskGroupContext } from "./task-group-context"
import { TaskMetadata } from "./task-metadata"

type TaskCallContextProps = {
  task: Task | undefined
  eligibility?: ReactNode
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
  eligibility,
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
      const token = await getAccessToken()
      if (!token) return
      const result = await readTask({
        client: portalClient(token),
        path: { taskId: taskID },
      }).catch(() => undefined)
      if (result?.data) onTaskUpdated(result.data)
    },
    [onTaskUpdated],
  )
  if (view === "call" && activeCall) {
    return (
      <CallWorkspace
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
        eligibility={eligibility}
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
  eligibility,
  onNextTask,
  activeCall,
  canMutate,
  historyHint,
  taskCallError,
  onTaskUpdated,
  onReturnToCall,
}: {
  task: Task
  eligibility?: ReactNode
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

  const [reviewedVersion, setReviewedVersion] = useState(task.version)
  const reviewRequired = task.origin === "APPOINTMENT_REVIEW" || task.origin === "INBOUND_MESSAGE_REVIEW"
  const needsReview = reviewRequired && task.state === "OPEN" && reviewedVersion !== task.version
  function acceptUpdate(updated: Task, advance = false) {
    setReviewedVersion(updated.version)
    onTaskUpdated(updated, advance)
  }

  async function refreshTask() {
    const token = await getAccessToken()
    if (!token) return undefined
    const latest = await readTask({
      client: portalClient(token),
      path: { taskId: task.id },
    }).catch(() => undefined)
    if (latest?.data) onTaskUpdated(latest.data)
    return latest?.data
  }

  async function saveTitle() {
    const attempted = draft.trim()
    if (!attempted || attempted === task.title) {
      setDraft(task.title)
      setEditing(false)
      return
    }
    setPending(true)
    setError("")
    const token = await getAccessToken()
    if (!token) {
      setPending(false)
      return
    }
    const result = await renameTask({
      client: portalClient(token),
      path: { taskId: task.id },
      body: {
        expectedVersion: task.version,
        title: attempted,
      },
    }).catch(() => undefined)
    setPending(false)
    if (result?.data) {
      acceptUpdate(result.data)
      setEditing(false)
      return
    }
    if (result?.response?.status === 409) {
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
    const token = await getAccessToken()
    if (!token) {
      setPending(false)
      return
    }
    const request = action === "complete" ? completeTask : reopenTask
    const result = await request({
      client: portalClient(token),
      path: { taskId: task.id },
      body: {
        expectedVersion: task.version,
      },
    }).catch(() => undefined)
    setPending(false)
    if (result?.data) {
      acceptUpdate(result.data, action === "complete")
      return
    }
    if (result?.response?.status === 409) {
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
      className="flex h-full min-h-0 flex-1 flex-col bg-transparent"
    >
      <div className="min-h-0 flex-1 overflow-y-auto px-5 py-5">
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
              if (event.key === "Escape") {
                setDraft(task.title)
                setEditing(false)
                setError("")
              }
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
            onClick={() => setEditing(false)}
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
              variant="ghost"
              size="icon"
              aria-label="Rename task"
              onClick={() => setEditing(true)}
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
      {task.state === "COMPLETED" && <p role="status" className="mt-3 text-sm text-muted-foreground">Completed by {task.completedBy?.email ?? "the team"}{task.completedAt ? ` · ${formatDateTime(task.completedAt)}` : ""}. <span className="block mt-1 text-xs">Completed for everyone with access.</span></p>}
      {needsReview && <div role="status" className="mt-3 rounded-md border p-3 text-sm">
        This Task changed. Review the latest activity before completing it.
        <Button size="sm" variant="outline" className="mt-2" onClick={() => setReviewedVersion(task.version)}>Review latest</Button>
      </div>}
      {eligibility && <div className="-mx-5 mt-4">{eligibility}</div>}
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
            value={`${formatDateTime(task.createdAt)} · ${actorLabel(task.createdBy)}`}
          />
          {task.automaticAcknowledgement && (
            <Metadata
              label="Caller acknowledgement"
              value={automaticAcknowledgementLabel(
                task.automaticAcknowledgement,
              )}
            />
          )}
          <Metadata label="Last changed" value={formatDateTime(task.updatedAt)} />
          <Metadata
            label="Completed"
            value={
              task.completedAt ? formatDateTime(task.completedAt) : "Not completed"
            }
          />
        </div>
      </details>
      </div>
      <footer className="shrink-0 border-t bg-background p-5">
      {canMutate && (
        <div className="flex flex-col gap-2">
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
      </footer>
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
  const [call, setCall] = useState<CallingCall>()
  const [interactions, setInteractions] = useState<Task["interactions"]>([])
  const [error, setError] = useState("")

  useEffect(() => {
    let current = true
    const timeout = window.setTimeout(async () => {
      const token = await getAccessToken()
      if (!token || !current) return
      const client = portalClient(token)
      const detail = await readTask({
        client,
        path: { taskId: task.id },
      }).catch(() => undefined)
      if (!current) return
      if (!detail?.data) {
        setError("Recovery source is temporarily unavailable.")
        return
      }
      const linkedInteractions = detail.data.interactions
      setInteractions(linkedInteractions)
      const selected = newestRecoveryInteraction(linkedInteractions)
      const callID = selected?.callId ?? task.callId
      if (!callID) return
      const result = await getCallingCall({
        client,
        path: { callId: callID },
      }).catch(() => undefined)
      if (!current) return
      if (result?.data) {
        setCall(result.data)
        setError("")
      } else {
        setError("Recovery source is temporarily unavailable.")
      }
    }, 0)
    return () => {
      current = false
      window.clearTimeout(timeout)
    }
  }, [revision, task.callId, task.id, task.version])

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
      {call?.voicemail && <VoicemailSource call={call} compact onUpdated={onUpdated} taskID={task.id} />}
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
                {formatDateTime(interaction.occurredAt)}
              </li>
            ))}
          </ul>
        </details>
      )}
    </section>
  )
}

function newestRecoveryInteraction(interactions: Task["interactions"]) {
  const newestFirst = [...interactions].sort((left, right) =>
    right.occurredAt.localeCompare(left.occurredAt),
  )
  return (
    newestFirst.find((interaction) => interaction.type === "VOICEMAIL") ??
    newestFirst[0]
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
    const token = await getAccessToken()
    const result = token ? await readTask({ client: portalClient(token), path: { taskId: id } }).catch(() => undefined) : undefined
    if (result?.data?.origin === "VOICEMAIL_RECOVERY" && result.data.state === "OPEN") {
      const latestCallID = newestRecoveryInteraction(result.data.interactions)?.callId ?? result.data.callId
      if (latestCallID === call.id) reviewedTask.current = result.data
      else setCompletionError("A newer call needs review. This playback will keep that work open.")
    }
    else if (!result?.data) setCompletionError("Could not load voicemail work. Use Mark done after listening.")
  }
  async function finishReview() {
    const task = reviewedTask.current
    if (!task) return
    reviewedTask.current = undefined
    const token = await getAccessToken()
    const result = token ? await completeTask({ client: portalClient(token), path: { taskId: task.id }, body: { expectedVersion: task.version } }).catch(() => undefined) : undefined
    if (result?.data) onUpdated?.(result.data)
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

type RecordingKind = "voicemail" | "call"

const recordingPresentation = {
  voicemail: {
    title: "Voicemail",
    label: "voicemail",
    audioLabel: "Voicemail recording",
    playbackPath: "voicemail-playback",
    issuePlayback: issueCallingVoicemailPlayback,
  },
  call: {
    title: "Call recording",
    label: "call recording",
    audioLabel: "Call recording",
    playbackPath: "recording-playback",
    issuePlayback: issueCallingRecordingPlayback,
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
    const token = await getAccessToken()
    if (!token) return
    setLoading(true)
    setError("")
    await onBeforePlay?.()
    const issued = await presentation.issuePlayback({
      client: portalClient(token),
      path: { callId: call.id },
    }).catch(() => undefined)
    if (!issued?.data) {
      setLoading(false)
      setError("Playback authorization is unavailable.")
      return
    }
    setAudioURL(
      new URL(
        `/v1/calling/${presentation.playbackPath}/${encodeURIComponent(issued.data.token)}`,
        portalAPIURL(),
      ).toString(),
    )
    setLoading(false)
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
            <Metadata label="Connected" value={formatDateTime(call.connectedAt)} />
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

function formatDateTime(value: string) {
  return new Intl.DateTimeFormat(undefined, {
    month: "short",
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
  }).format(new Date(value))
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
  const [callEligible, setCallEligible] = useState(false)
  const [callReason, setCallReason] = useState("Checking Call route…")
  useEffect(() => {
    if (!canCall) return
    let current = true
    const timeout = window.setTimeout(async () => {
      const token = await getAccessToken()
      if (!token || !current) return
      const result = await getTaskOutboundEligibility({
        client: portalClient(token),
        path: { taskId: task.id },
      }).catch(() => undefined)
      if (!current) return
      if (!result?.data) {
        setCallEligible(false)
        setCallReason("Call eligibility is temporarily unavailable.")
        return
      }
      setCallEligible(result.data.eligible)
      setCallReason(result.data.reason)
    }, 0)
    return () => {
      current = false
      window.clearTimeout(timeout)
    }
  }, [canCall, historyHint, task.id, task.state, task.version])

  const taskCallingEligible = canCall && callEligible
  const taskCallingReason = canCall
    ? callReason
    : "Calling is not enabled for this account."

  return <Button variant="outline" className="shadow-sm" disabled={!taskCallingEligible || pending} title={taskCallingEligible ? "Call this Task" : taskCallingReason} onClick={() => onCall(task)}>
    {pending ? <Spinner /> : <PhoneCallIcon />} {pending ? "Preparing…" : "Call"}
  </Button>
}
