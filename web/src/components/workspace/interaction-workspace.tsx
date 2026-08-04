"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import {
  ArrowLeftIcon,
  AudioLinesIcon,
  BotIcon,
  CheckIcon,
  CheckCircle2Icon,
  Clock3Icon,
  HistoryIcon,
  MessageSquareIcon,
  PencilIcon,
  PhoneCallIcon,
  RefreshCwIcon,
  RotateCcwIcon,
  XIcon,
} from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Separator } from "@/components/ui/separator"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { TaskMessageConversation } from "@/components/workspace/message-workspace"
import { portalAPIURL, portalClient } from "@/lib/api/client"
import {
  completeTask,
  getCallingCall,
  getCallingEngagementHistory,
  getCallingCallHistory,
  getTaskOutboundEligibility,
  getTaskCallHistory,
  issueCallingVoicemailPlayback,
  readTask,
  renameTask,
  reopenTask,
  sendMessage,
} from "@/lib/api/generated/sdk.gen"
import type {
  CallHistoryItem,
  CallingCall,
  ConversationTimelineItem,
  Task,
} from "@/lib/api/generated/types.gen"
import { getAccessToken } from "@/lib/auth-client"
import { cn } from "@/lib/utils"

type InteractionWorkspaceProps = {
  task: Task | undefined
  activeCall: CallingCall | undefined
  view: "none" | "task" | "call"
  supportSessionID: string
  canMutate: boolean
  historyHint: number
  taskCallPending: boolean
  taskCallError: string
  onTaskUpdated: (task: Task) => void
  onStartTaskCall: (task: Task) => void
  onReturnToCall: () => void
}

export function InteractionWorkspace({
  task,
  activeCall,
  view,
  supportSessionID,
  canMutate,
  historyHint,
  taskCallPending,
  taskCallError,
  onTaskUpdated,
  onStartTaskCall,
  onReturnToCall,
}: InteractionWorkspaceProps) {
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
        historyHint={historyHint}
        returnTask={task}
        onReturnToTask={task ? () => onTaskUpdated(task) : undefined}
		onOpenRecoveryTask={(taskID) => void openRecoveryTask(taskID)}
        supportSessionID={supportSessionID}
        canMutate={canMutate}
      />
    )
  }
  if (view === "task" && task) {
    return (
      <TaskWorkspace
        key={task.id}
        task={task}
        activeCall={activeCall}
        supportSessionID={supportSessionID}
        canMutate={canMutate}
        historyHint={historyHint}
        taskCallPending={taskCallPending}
        taskCallError={taskCallError}
        onTaskUpdated={onTaskUpdated}
        onStartTaskCall={onStartTaskCall}
        onReturnToCall={onReturnToCall}
      />
    )
  }
  return <section aria-label="No Task selected" className="min-h-0 flex-1" />
}

function TaskWorkspace({
  task,
  activeCall,
  supportSessionID,
  canMutate,
  historyHint,
  taskCallPending,
  taskCallError,
  onTaskUpdated,
  onStartTaskCall,
  onReturnToCall,
}: {
  task: Task
  activeCall: CallingCall | undefined
  supportSessionID: string
  canMutate: boolean
  historyHint: number
  taskCallPending: boolean
  taskCallError: string
  onTaskUpdated: (task: Task) => void
  onStartTaskCall: (task: Task) => void
  onReturnToCall: () => void
}) {
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(task.title)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState("")
  const [callEligible, setCallEligible] = useState(false)
  const [callReason, setCallReason] = useState("Checking Call route…")

  useEffect(() => {
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
  }, [historyHint, task.id, task.state, task.version])

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
        ...(supportSessionID
          ? { supportSessionId: supportSessionID }
          : {}),
      },
    }).catch(() => undefined)
    setPending(false)
    if (result?.data) {
      onTaskUpdated(result.data)
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
        ...(supportSessionID
          ? { supportSessionId: supportSessionID }
          : {}),
      },
    }).catch(() => undefined)
    setPending(false)
    if (result?.data) {
      onTaskUpdated(result.data)
      return
    }
    if (result?.response?.status === 409) {
      await refreshTask()
      setError("This Task changed elsewhere. The latest state is now loaded.")
      return
    }
    setError(`The Task could not be ${action === "complete" ? "completed" : "reopened"}.`)
  }

  return (
    <section className="flex min-h-0 flex-1 flex-col xl:flex-row">
      <div className="flex min-h-0 min-w-0 flex-1 flex-col">
        <header className="border-b px-5 py-4">
          <p className="text-xs font-medium text-muted-foreground">
            Engagement History · exact phone
          </p>
          <h1 className="mt-1 text-xl font-semibold tabular-nums">
            {formatPhone(task.phone)}
          </h1>
          <p className="mt-1 text-xs text-muted-foreground">
            {task.locationName} · Task-linked context across authorized offices
          </p>
        </header>
        <TaskMessageConversation
          key={task.id}
          task={task}
          supportSessionID={supportSessionID}
          canMutate={canMutate}
          revision={historyHint}
          onTaskCreated={onTaskUpdated}
          onMessageSent={() => void refreshTask()}
        />
        {!task.messageThreadId && !task.conversationThreadId && (
          <CallHistory
            key={`task:${task.id}`}
            source={{ kind: "task", id: task.id }}
            revision={historyHint}
          />
        )}
      </div>
      <aside
        aria-label="Focused Task"
        className="w-full shrink-0 overflow-y-auto border-t bg-card px-4 py-4 xl:w-80 xl:border-t-0 xl:border-l"
      >
        <div className="flex items-center gap-2">
          <Badge
            variant={task.state === "OPEN" ? "secondary" : "outline"}
            className={task.state === "COMPLETED" ? "text-success" : undefined}
          >
            {task.state === "OPEN" ? "Open" : "Completed"}
          </Badge>
          <span className="text-xs text-muted-foreground">Task · v{task.version}</span>
        </div>
        {editing && task.state === "OPEN" ? (
          <div className="mt-3 flex items-center gap-1">
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
            <Button size="icon" aria-label="Save title" onClick={() => void saveTitle()}>
              {pending ? <Spinner /> : <CheckIcon />}
            </Button>
            <Button size="icon" variant="ghost" aria-label="Cancel rename" onClick={() => setEditing(false)}>
              <XIcon />
            </Button>
          </div>
        ) : (
          <div className="mt-3 flex items-start gap-1">
            <h2 className="min-w-0 flex-1 text-lg font-semibold leading-snug">{task.title}</h2>
            {task.state === "OPEN" && canMutate && (
              <Button variant="ghost" size="icon" aria-label="Rename task" onClick={() => setEditing(true)}>
                <PencilIcon />
              </Button>
            )}
          </div>
        )}
        <div className="mt-4 flex flex-col gap-2">
          {activeCall && (
            <Button variant="outline" onClick={onReturnToCall}>
              <PhoneCallIcon /> Return to active call
            </Button>
          )}
          {!canMutate ? (
            <Badge variant="outline">Read only · enter Support Mode</Badge>
          ) : task.state === "OPEN" ? (
            <>
              {!activeCall && (
                <Button
                  variant="outline"
                  disabled={!callEligible || taskCallPending}
                  title={callEligible ? "Call this Task" : callReason}
                  onClick={() => onStartTaskCall(task)}
                >
                  <PhoneCallIcon /> {taskCallPending ? "Preparing…" : "Call"}
                </Button>
              )}
              <Button onClick={() => void transition("complete")} disabled={pending}>
                {pending ? <Spinner /> : <CheckCircle2Icon />} Complete
              </Button>
            </>
          ) : (
            <Button variant="outline" onClick={() => void transition("reopen")} disabled={pending}>
              {pending ? <Spinner /> : <RotateCcwIcon />} Reopen
            </Button>
          )}
        </div>
        {(taskCallError || (!callEligible && callReason)) && (
          <p className="mt-3 text-xs text-muted-foreground">{taskCallError || callReason}</p>
        )}
        {error && (
          <Alert variant="destructive" className="mt-3">
            <AlertTitle>Task changed</AlertTitle>
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        {(task.origin === "VOICEMAIL_RECOVERY" ||
          task.origin === "MISSED_CALL_RECOVERY") && (
          <RecoveryTaskSource task={task} revision={historyHint} />
        )}
        <Separator className="my-4" />
        <details className="group rounded-md border px-3 py-2">
          <summary className="cursor-pointer text-sm font-medium">
            More context
          </summary>
          <div className="mt-3 space-y-3">
            <p className="text-xs text-muted-foreground">
              Task Activity is shown in sequence in Engagement History.
            </p>
            {task.origin === "ABITA_AI" && <AITaskSource task={task} />}
            {task.sourceCallId && task.origin !== "ABITA_AI" && (
              <Metadata label="Source call" value={task.sourceCallId} />
            )}
          </div>
        </details>
        <div className="grid gap-3 text-xs text-muted-foreground">
          <Metadata label="Created" value={`${formatDateTime(task.createdAt)} · ${actorLabel(task.createdBy)}`} />
          <Metadata label="Last changed" value={formatDateTime(task.updatedAt)} />
          <Metadata
            label="Completed"
            value={task.completedAt ? formatDateTime(task.completedAt) : "Not completed"}
          />
        </div>
      </aside>
    </section>
  )
}

function AITaskSource({ task }: { task: Task }) {
  return (
    <section aria-label="AI Task source" className="space-y-2">
      <div className="flex flex-wrap items-center gap-2">
        <Badge variant="outline" className="gap-1.5">
          <BotIcon className="size-3.5" aria-hidden="true" />
          AI-created
        </Badge>
        {task.category && (
          <Badge variant="secondary">{formatCategory(task.category)}</Badge>
        )}
        <Badge
          variant={
            task.urgency === "high_priority" ? "destructive" : "secondary"
          }
        >
          {formatUrgency(task.urgency)}
        </Badge>
      </div>
      <div className="text-sm">
        <div>
          <p className="font-medium">
            {task.callerName
              ? `AI-supplied name: ${task.callerName}`
              : "No sourced caller name"}
          </p>
          <p className="mt-1 whitespace-pre-wrap text-muted-foreground">
            {task.sourceMessage}
          </p>
        </div>
        {task.sourceCallId && (
          <p className="mt-2 break-all font-mono text-xs text-muted-foreground">
            Source call · {task.sourceCallId}
          </p>
        )}
      </div>
    </section>
  )
}

function RecoveryTaskSource({
  task,
  revision,
}: {
  task: Task
  revision: number
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
      const voicemail = linkedInteractions.find(
        (interaction) => interaction.type === "VOICEMAIL",
      )
      const callID = voicemail?.callId ?? task.callId
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

  return (
    <section aria-label="Call recovery source">
      <div className="flex flex-wrap items-center gap-2">
        <Badge variant="outline" className="gap-1.5">
          <AudioLinesIcon className="size-3.5" aria-hidden="true" />
          {task.recoveryOutcome === "VOICEMAIL" ? "Voicemail" : "Missed call"}
        </Badge>
		{task.relatedInteractionCount > 0 && (
			<span className="text-xs text-muted-foreground">
				{task.relatedInteractionCount} related
			</span>
		)}
      </div>
      {interactions.length > 0 && (
        <ul className="mt-2 space-y-1 text-xs text-muted-foreground">
          {interactions.map((interaction) => (
            <li key={interaction.callId}>
              {interaction.type === "VOICEMAIL" ? "Voicemail" : "Missed call"}
              {" · "}
              {formatDateTime(interaction.occurredAt)}
            </li>
          ))}
        </ul>
      )}
      {error && <p className="mt-2 text-sm text-destructive">{error}</p>}
      {call?.voicemail && <VoicemailSource call={call} compact />}
    </section>
  )
}

function VoicemailSource({
  call,
  compact = false,
}: {
  call: CallingCall
  compact?: boolean
}) {
  const [audioURL, setAudioURL] = useState("")
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState("")
	const audioRef = useRef<HTMLAudioElement>(null)
  const voicemail = call.voicemail

  useEffect(
    () => () => {
      if (audioURL) URL.revokeObjectURL(audioURL)
    },
    [audioURL],
  )

	useEffect(() => {
		if (audioURL) void audioRef.current?.play().catch(() => undefined)
	}, [audioURL])

  if (!voicemail) return null
  const stateLabel =
    voicemail.outcome === "MISSED_CALL"
      ? "No recording was produced."
      : voicemail.audioState === "READY"
        ? "Ready"
        : voicemail.audioState === "UNAVAILABLE"
          ? "Recording unavailable"
          : "Processing"

  async function loadAudio() {
    const token = await getAccessToken()
    if (!token) return
    setLoading(true)
    setError("")
    const issued = await issueCallingVoicemailPlayback({
      client: portalClient(token),
      path: { callId: call.id },
    }).catch(() => undefined)
    if (!issued?.data) {
      setLoading(false)
      setError("Playback authorization is unavailable.")
      return
    }
    const response = await fetch(
      new URL(
        `/v1/calling/voicemail-playback/${encodeURIComponent(issued.data.token)}`,
        portalAPIURL(),
      ),
      {
        headers: { authorization: `Bearer ${token}` },
        cache: "no-store",
      },
    ).catch(() => undefined)
    if (!response?.ok) {
      setLoading(false)
      setError("The voicemail could not be opened.")
      return
    }
    const objectURL = URL.createObjectURL(await response.blob())
    setAudioURL((current) => {
      if (current) URL.revokeObjectURL(current)
      return objectURL
    })
    setLoading(false)
  }

  return (
    <div className={compact ? "mt-3" : "border-b bg-muted/20 px-5 py-4"}>
      {!compact && (
        <div className="mb-2 flex items-center gap-2">
          <AudioLinesIcon className="size-4" />
          <h2 className="text-sm font-semibold">Voicemail source</h2>
        </div>
      )}
      <div className="flex flex-wrap items-center gap-2">
        <Badge variant="secondary">{stateLabel}</Badge>
        {voicemail.durationSeconds > 0 && (
          <span className="text-xs text-muted-foreground">
            {formatDuration(voicemail.durationSeconds)}
          </span>
        )}
        {voicemail.audioState === "READY" && !audioURL && (
          <Button
            size="sm"
            variant="outline"
            disabled={loading}
            onClick={() => void loadAudio()}
          >
            {loading ? <Spinner /> : "Play"}
          </Button>
        )}
        {audioURL && (
          <audio
			ref={audioRef}
            aria-label="Voicemail recording"
            controls
            controlsList="nodownload"
            preload="metadata"
            src={audioURL}
            className="h-9 max-w-full"
          />
        )}
      </div>
      {error && <p className="mt-2 text-xs text-destructive">{error}</p>}
    </div>
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

function CallWorkspace({
  call,
  historyHint,
  returnTask,
  onReturnToTask,
	onOpenRecoveryTask,
  supportSessionID,
  canMutate,
}: {
  call: CallingCall
  historyHint: number
  returnTask: Task | undefined
  onReturnToTask: (() => void) | undefined
	onOpenRecoveryTask: (taskID: string) => void
  supportSessionID: string
  canMutate: boolean
}) {
  const [localRevision, setLocalRevision] = useState(0)
  return (
    <section className="flex min-h-0 flex-1 flex-col">
      <header className="border-b px-5 py-4">
        <div className="flex flex-wrap items-start gap-3">
          <div className="min-w-0 flex-1 basis-full sm:basis-auto">
            <div className="mb-2 flex items-center gap-2">
              <Badge
                variant="secondary"
                className={cn(
                  call.state === "CONNECTED" && "text-success",
                  (call.state === "CONNECTING" ||
                    call.state === "RECONCILING") && "text-warning",
                )}
              >
                {callWorkspaceLabel(call.state)}
              </Badge>
              <span className="text-xs font-medium text-muted-foreground">
                {callStateLabel(call.state)}
              </span>
            </div>
            <p className="text-xs font-medium text-muted-foreground">
              Contact Context
            </p>
            <h1 className="truncate text-xl font-semibold tracking-[-0.015em]">
              {call.displayName || formatPhone(call.phone)}
            </h1>
            <p className="mt-2 text-sm tabular-nums text-muted-foreground">
              {formatPhone(call.phone)} · {call.locationName}
            </p>
			{call.recoveryTask && (
				<div className="mt-3 flex flex-wrap items-center gap-2 text-sm">
					<Badge variant="outline">{call.recoveryTask.state}</Badge>
					<span className="font-medium">{call.recoveryTask.title}</span>
					<span className="text-xs text-muted-foreground">
						{call.recoveryTask.relatedInteractionCount} related
					</span>
					<Button size="sm" variant="outline" onClick={() => onOpenRecoveryTask(call.recoveryTask!.id)}>
						Open Task
					</Button>
				</div>
			)}
          </div>
          {returnTask && onReturnToTask && (
            <Button variant="outline" onClick={onReturnToTask}>
              <ArrowLeftIcon />
              Back to Task
            </Button>
          )}
        </div>
        <div className="mt-4 grid gap-3 border-t pt-3 text-xs text-muted-foreground sm:grid-cols-3">
          <Metadata
            label="Transfer reason"
            value={call.transferReason || "No transfer reason"}
          />
          <Metadata
            label="Office"
            value={call.locationName}
          />
          <Metadata
            label="Connected"
            value={
              call.connectedAt
                ? formatDateTime(call.connectedAt)
                : "Connection in progress"
            }
          />
        </div>
      </header>
      {call.voicemail && <VoicemailSource call={call} />}
      <LockedCallMessageComposer
        call={call}
        canMutate={canMutate}
        supportSessionID={supportSessionID}
        onSent={() => setLocalRevision((current) => current + 1)}
      />
      <EngagementHistory
        key={`engagement:${call.id}`}
        call={call}
        revision={historyHint + call.version + localRevision}
      />
    </section>
  )
}

function LockedCallMessageComposer({
  call,
  canMutate,
  supportSessionID,
  onSent,
}: {
  call: CallingCall
  canMutate: boolean
  supportSessionID: string
  onSent: () => void
}) {
  const [body, setBody] = useState("")
  const [pending, setPending] = useState(false)
  const [error, setError] = useState("")
  const attempt = useRef<{ body: string; key: string } | undefined>(undefined)

  async function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const trimmed = body.trim()
    if (!trimmed || pending || !canMutate) return
    if (attempt.current?.body !== trimmed) {
      attempt.current = { body: trimmed, key: crypto.randomUUID() }
    }
    setPending(true)
    setError("")
    const token = await getAccessToken()
    if (!token) {
      setPending(false)
      return
    }
    const result = await sendMessage({
      client: portalClient(token),
      body: {
        practiceId: call.practiceId,
        locationId: call.locationId,
        destination: call.phone,
        body: trimmed,
        idempotencyKey: attempt.current.key,
        ...(supportSessionID ? { supportSessionId: supportSessionID } : {}),
      },
    }).catch(() => undefined)
    setPending(false)
    if (!result?.data) {
      setError(
        result?.response?.status === 409
          ? "This contact cannot be messaged from this office."
          : "The message was not queued. Nothing was sent.",
      )
      return
    }
    setBody("")
    attempt.current = undefined
    onSent()
  }

  return (
    <form
      aria-label="Call message composer"
      className="border-b bg-background px-5 py-3"
      onSubmit={(event) => void submit(event)}
    >
      <div className="mx-auto max-w-4xl">
        <p className="mb-2 text-xs text-muted-foreground">
          Message from{" "}
          <strong className="font-medium text-foreground">
            {call.locationName}
          </strong>
          {" · "}
          destination locked to {formatPhone(call.phone)}
        </p>
        <div className="flex items-end gap-2">
          <textarea
            aria-label="Message"
            rows={2}
            maxLength={1600}
            placeholder="Write a message"
            className="flex min-h-16 min-w-0 flex-1 resize-y rounded-md border border-input bg-transparent px-3 py-2 text-sm shadow-xs outline-none transition-[color,box-shadow] placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:cursor-not-allowed disabled:opacity-50"
            value={body}
            disabled={!canMutate || pending}
            onChange={(event) => setBody(event.target.value)}
          />
          <Button
            type="submit"
            disabled={!canMutate || pending || !body.trim()}
          >
            {pending ? <Spinner /> : <MessageSquareIcon />}
            Send
          </Button>
        </div>
        {error && <p className="mt-2 text-xs text-destructive">{error}</p>}
      </div>
    </form>
  )
}

function EngagementHistory({
  call,
  revision,
}: {
  call: CallingCall
  revision: number
}) {
  const [items, setItems] = useState<ConversationTimelineItem[]>([])
  const [nextCursor, setNextCursor] = useState("")
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")
  const generation = useRef(0)
  const scrollContainer = useRef<HTMLDivElement | null>(null)

  const load = useCallback(
    async (cursor = "") => {
      const requestGeneration = ++generation.current
      setLoading(true)
      setError("")
      const token = await getAccessToken()
      if (!token) {
        setLoading(false)
        return
      }
      const result = await getCallingEngagementHistory({
        client: portalClient(token),
        path: { callId: call.id },
        query: cursor ? { cursor } : undefined,
      }).catch(() => undefined)
      if (requestGeneration !== generation.current) return
      setLoading(false)
      if (!result?.data) {
        setError("Engagement history is temporarily unavailable.")
        return
      }
      setItems((current) =>
        cursor ? [...result.data.items, ...current] : result.data.items,
      )
      setNextCursor(result.data.nextCursor)
      window.requestAnimationFrame(() => {
        const current = scrollContainer.current
        if (current && !cursor) current.scrollTop = current.scrollHeight
      })
    },
    [call.id],
  )

  useEffect(() => {
    const timeout = window.setTimeout(() => void load(), 0)
    return () => {
      window.clearTimeout(timeout)
      generation.current += 1
    }
  }, [load, revision])

  return (
    <div ref={scrollContainer} className="min-h-0 flex-1 overflow-y-auto">
      <div className="mx-auto max-w-4xl px-5 py-5">
        <div className="flex flex-wrap items-center gap-2">
          <HistoryIcon className="size-4 text-muted-foreground" />
          <h2 className="text-sm font-semibold">Engagement history</h2>
          <span className="text-xs text-muted-foreground">
            Exact phone · authorized offices
          </span>
        </div>
        <Separator className="my-4" />
        {nextCursor && (
          <div className="mb-4 flex justify-center">
            <Button
              size="sm"
              variant="outline"
              disabled={loading}
              onClick={() => void load(nextCursor)}
            >
              {loading ? <Spinner /> : <HistoryIcon />}
              Load older
            </Button>
          </div>
        )}
        {loading && items.length === 0 && (
          <div className="space-y-3">
            <Skeleton className="h-24 w-full" />
            <Skeleton className="h-24 w-full" />
          </div>
        )}
        {error && (
          <Alert variant="destructive">
            <AlertTitle>History unavailable</AlertTitle>
            <AlertDescription className="flex items-center justify-between gap-3">
              <span>{error}</span>
              <Button size="sm" variant="outline" onClick={() => void load()}>
                <RefreshCwIcon />
                Retry
              </Button>
            </AlertDescription>
          </Alert>
        )}
        {!loading && !error && items.length === 0 && (
          <p className="py-12 text-center text-sm text-muted-foreground">
            No earlier engagement for this phone.
          </p>
        )}
        <ol className="relative ml-2 border-l">
          {items.map((item) => (
            <li
              key={`${item.type}:${item.id}`}
              className="relative pb-4 pl-6 last:pb-0"
            >
              <span className="absolute top-4 -left-[0.31rem] size-2.5 rounded-full border-2 border-background bg-muted-foreground" />
              <EngagementHistoryItem item={item} currentCallID={call.id} />
            </li>
          ))}
        </ol>
      </div>
    </div>
  )
}

function EngagementHistoryItem({
  item,
  currentCallID,
}: {
  item: ConversationTimelineItem
  currentCallID: string
}) {
  if (item.type === "MESSAGE" && item.message) {
    const message = item.message
    return (
      <article className="border bg-card px-4 py-3">
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-sm font-medium">
            {message.direction === "INBOUND" ? "Inbound" : "Outbound"} message
          </span>
          <Badge variant="outline">{message.delivery}</Badge>
          <time
            dateTime={item.occurredAt}
            className="ml-auto text-xs tabular-nums text-muted-foreground"
          >
            {formatDateTime(item.occurredAt)}
          </time>
        </div>
        <p className="mt-3 whitespace-pre-wrap text-sm">
          {message.body || "Attachment"}
        </p>
        <p className="mt-3 border-t pt-2 text-xs text-muted-foreground">
          Office · {message.thread.locationName}
        </p>
      </article>
    )
  }
  if (item.type === "CALL" && item.call) {
    const historyCall = item.call
    return (
      <article
        className={cn(
          "border bg-card px-4 py-3",
          historyCall.id === currentCallID && "border-primary/50",
        )}
      >
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-sm font-medium">
            {historyCall.direction === "INBOUND" ? "Inbound" : "Outbound"} call
          </span>
          {historyCall.id === currentCallID && (
            <Badge variant="secondary">Current</Badge>
          )}
          <time
            dateTime={item.occurredAt}
            className="ml-auto text-xs tabular-nums text-muted-foreground"
          >
            {formatDateTime(item.occurredAt)}
          </time>
        </div>
        <div className="mt-3 grid gap-x-5 gap-y-2 text-xs sm:grid-cols-2">
          <HistoryField label="Office" value={historyCall.locationName} />
          <HistoryField
            label="Duration"
            value={formatDuration(historyCall.durationSeconds)}
          />
          <HistoryField
            label="Answered by"
            value={historyCall.answeredByEmail || "Not answered"}
          />
          <HistoryField
            label="Outcome"
            value={historyOutcome(historyCall.outcome)}
          />
        </div>
      </article>
    )
  }
  if (item.type === "TASK" && item.task) {
    const task = item.task
    return (
      <article className="border bg-card px-4 py-3">
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-sm font-medium">Task</span>
          <Badge
            variant={task.state === "OPEN" ? "secondary" : "outline"}
            className={task.state === "COMPLETED" ? "text-success" : undefined}
          >
            {task.state}
          </Badge>
          {task.recoveryOutcome && (
            <Badge variant="outline">
              {task.recoveryOutcome.replaceAll("_", " ")}
            </Badge>
          )}
          <time
            dateTime={item.occurredAt}
            className="ml-auto text-xs tabular-nums text-muted-foreground"
          >
            {formatDateTime(item.occurredAt)}
          </time>
        </div>
        <p className="mt-3 text-sm">{task.title}</p>
        <p className="mt-3 border-t pt-2 text-xs text-muted-foreground">
          Office · {task.locationName}
        </p>
      </article>
    )
  }
  return null
}

type HistorySource = { kind: "task" | "call"; id: string }

function CallHistory({
  source,
  revision,
}: {
  source: HistorySource
  revision: number
}) {
  const [items, setItems] = useState<CallHistoryItem[]>([])
  const [cursor, setCursor] = useState("")
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")
  const generation = useRef(0)
  const itemsRef = useRef<CallHistoryItem[]>([])
  const scrollContainer = useRef<HTMLDivElement | null>(null)
  const olderSentinel = useRef<HTMLDivElement | null>(null)

  const load = useCallback(
    async (nextCursor = "", requestedGeneration?: number) => {
      const requestGeneration =
        requestedGeneration ?? ++generation.current
      const container = scrollContainer.current
      const previousHeight = container?.scrollHeight ?? 0
      const previousTop = container?.scrollTop ?? 0
      const wasNearBottom = container
        ? previousHeight - previousTop - container.clientHeight < 64
        : true
      setLoading(true)
      setError("")
      const token = await getAccessToken()
      if (!token) {
        setLoading(false)
        return
      }
      const client = portalClient(token)
      const fetchPage = (pageCursor = "") => {
        const query = pageCursor ? { cursor: pageCursor } : undefined
        return source.kind === "task"
          ? getTaskCallHistory({
              client,
              path: { taskId: source.id },
              query,
            }).catch(() => undefined)
          : getCallingCallHistory({
              client,
              path: { callId: source.id },
              query,
            }).catch(() => undefined)
      }
      let targetDepth = nextCursor ? 0 : itemsRef.current.length
      const result = await fetchPage(nextCursor)
      if (requestGeneration !== generation.current) return
      if (!result?.data) {
        setLoading(false)
        setError("Call history is temporarily unavailable.")
        return
      }
      if (!nextCursor && targetDepth > 0) {
        const currentIDs = new Set(itemsRef.current.map((item) => item.id))
        targetDepth += result.data.items.filter(
          (item) => !currentIDs.has(item.id),
        ).length
      }
      let pageItems = result.data.items
      let pageCursor = result.data.nextCursor
      while (pageCursor && pageItems.length < targetDepth) {
        const older = await fetchPage(pageCursor)
        if (requestGeneration !== generation.current) return
        if (!older?.data) {
          setLoading(false)
          setError("Call history is temporarily unavailable.")
          return
        }
        pageItems = [...older.data.items, ...pageItems]
        pageCursor = older.data.nextCursor
      }
      if (!nextCursor && targetDepth > 0 && pageItems.length > targetDepth) {
        pageItems = pageItems.slice(-targetDepth)
      }
      const nextItems = nextCursor
        ? [...pageItems, ...itemsRef.current]
        : pageItems
      itemsRef.current = nextItems
      setItems(nextItems)
      setCursor(pageCursor)
      setLoading(false)
      window.requestAnimationFrame(() =>
        window.requestAnimationFrame(() => {
          if (requestGeneration !== generation.current) return
          const current = scrollContainer.current
          if (!current) return
          if (nextCursor) {
            current.scrollTop =
              previousTop + current.scrollHeight - previousHeight
          } else if (wasNearBottom) {
            current.scrollTop = current.scrollHeight
          } else {
            current.scrollTop = previousTop
          }
        }),
      )
    },
    [source.id, source.kind],
  )

  useEffect(() => {
    const requestGeneration = ++generation.current
    const timeout = window.setTimeout(() => {
      void load("", requestGeneration)
    }, 0)
    return () => {
      window.clearTimeout(timeout)
      generation.current += 1
    }
  }, [load, revision])

  useEffect(() => {
    const element = olderSentinel.current
    if (!element || !cursor || loading) return
    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((entry) => entry.isIntersecting)) void load(cursor)
      },
      { rootMargin: "160px 0px" },
    )
    observer.observe(element)
    return () => observer.disconnect()
  }, [cursor, load, loading])

  return (
    <div
      ref={scrollContainer}
      className="min-h-0 flex-1 overflow-y-auto"
    >
      <div className="mx-auto max-w-4xl px-5 py-5">
        <div className="flex items-center gap-2">
          <HistoryIcon className="size-4 text-muted-foreground" />
          <h2 className="text-sm font-semibold">Engagement history</h2>
          <span className="text-xs text-muted-foreground">
            Exact phone · authorized offices
          </span>
        </div>
        <Separator className="my-4" />
        {cursor && (
          <div
            ref={olderSentinel}
            aria-label="Loading older calls"
            className="mb-3 flex h-8 items-center justify-center text-muted-foreground"
          >
            {loading ? <Spinner /> : <Clock3Icon className="size-3.5" />}
          </div>
        )}
        {loading && items.length === 0 && (
          <div className="space-y-3">
            <Skeleton className="h-24 w-full" />
            <Skeleton className="h-24 w-full" />
          </div>
        )}
        {error && (
          <Alert variant="destructive">
            <AlertTitle>History unavailable</AlertTitle>
            <AlertDescription className="flex items-center justify-between gap-3">
              <span>{error}</span>
              <Button size="sm" variant="outline" onClick={() => void load()}>
                <RefreshCwIcon />
                Retry
              </Button>
            </AlertDescription>
          </Alert>
        )}
        {!loading && !error && items.length === 0 && (
          <p className="py-12 text-center text-sm text-muted-foreground">
            No earlier calls for this phone.
          </p>
        )}
        <ol className="relative ml-2 border-l">
          {items.map((item) => (
            <li key={item.id} className="relative pb-4 pl-6 last:pb-0">
              <span
                className={cn(
                  "absolute top-4 -left-[0.31rem] size-2.5 rounded-full border-2 border-background bg-muted-foreground",
                  item.current && "bg-primary",
                )}
              />
              <article
                className={cn(
                  "border bg-card px-4 py-3",
                  item.originating && "border-primary/50",
                )}
              >
                <div className="flex flex-wrap items-center gap-2">
                  <span className="text-sm font-medium">Inbound call</span>
                  {item.current && <Badge variant="secondary">Current</Badge>}
                  {item.originating && (
                    <Badge variant="outline">Created this Task</Badge>
                  )}
                  <time
                    dateTime={item.startedAt}
                    className="ml-auto text-xs tabular-nums text-muted-foreground"
                  >
                    {formatDateTime(item.startedAt)}
                  </time>
                </div>
                <div className="mt-3 grid gap-x-5 gap-y-2 text-xs sm:grid-cols-2">
                  <HistoryField label="Office" value={item.locationName} />
                  <HistoryField
                    label="Duration"
                    value={formatDuration(item.durationSeconds)}
                  />
                  <HistoryField
                    label="Answered by"
                    value={item.answeredByEmail || "Not answered"}
                  />
                  <HistoryField
                    label="Outcome"
                    value={historyOutcome(item.outcome)}
                  />
                </div>
                <p className="mt-3 border-t pt-2 text-xs text-muted-foreground">
                  {item.transferReason || "No transfer reason recorded"}
                </p>
              </article>
            </li>
          ))}
        </ol>
      </div>
    </div>
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

function HistoryField({ label, value }: { label: string; value: string }) {
  return (
    <p>
      <span className="text-muted-foreground">{label}</span>
      <span className="ml-2 text-foreground">{value}</span>
    </p>
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

function formatPhone(phone: string) {
  const match = phone.match(/^\+1(\d{3})(\d{3})(\d{4})$/)
  if (!match) return phone
  return `(${match[1]}) ${match[2]}-${match[3]}`
}

function callStateLabel(state: CallingCall["state"]) {
  return state.toLowerCase().replaceAll("_", " ")
}

function callWorkspaceLabel(state: CallingCall["state"]) {
  if (state === "NEEDS_DISPOSITION") return "Call ended"
  if (
    state === "UNANSWERED" ||
    state === "RESOLVED" ||
    state === "FOLLOW_UP_REQUIRED"
  ) {
    return "Call closed"
  }
  return "Live call"
}

function historyOutcome(outcome: CallHistoryItem["outcome"]) {
  if (outcome === "FOLLOW_UP_REQUIRED") return "Follow-up created"
  return outcome.toLowerCase().replaceAll("_", " ")
}
