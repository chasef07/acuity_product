"use client"

import {
  type ChangeEvent,
  type FormEvent,
  useCallback,
  useEffect,
  useRef,
  useState,
} from "react"
import {
  AlertTriangleIcon,
  CheckIcon,
  CheckSquareIcon,
  DownloadIcon,
  FileTextIcon,
  MessageSquareIcon,
  PaperclipIcon,
  PhoneCallIcon,
  PhoneIncomingIcon,
  PhoneMissedIcon,
  PhoneOutgoingIcon,
  PencilIcon,
  RefreshCwIcon,
  SearchIcon,
  SendIcon,
  StickyNoteIcon,
  VoicemailIcon,
  XIcon,
} from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select"
import { Spinner } from "@/components/ui/spinner"
import { Textarea } from "@/components/ui/textarea"
import { CallingNumberAction } from "@/components/workspace/calling-dock"
import { portalAPIURL, portalClient } from "@/lib/api/client"
import {
  completeTask,
  createMessageFollowUpTask,
  createStaffNote,
  getEngagementTimeline,
  getCallingCall,
  getMessageAttachment,
  getMessageThreadTimeline,
  getTaskEngagementHistory,
  issueCallingVoicemailPlayback,
  markEngagementRead,
  markEngagementTextHandled,
  markMessageThreadRead,
  markTaskRead,
  readTask,
  retryInboundMessageAttachment,
  reopenTask,
  renameTask,
  sendMessage,
  sendMessageAgain,
  uploadMessageAttachment,
} from "@/lib/api/generated/sdk.gen"
import type {
  ConversationTimelineItem,
  CallingCall,
  CallHistoryItem,
  EngagementSummary,
  Message,
  MessageAttachment,
  MessageThreadSummary,
  Task,
} from "@/lib/api/generated/types.gen"
import { getAccessToken } from "@/lib/auth-client"
import { cn } from "@/lib/utils"

const maximumMessageLength = 1_600
const maximumAttachmentBytes = 600 * 1_024
const acceptedAttachmentTypes = new Set([
  "image/jpeg",
  "image/png",
  "image/gif",
  "image/webp",
  "application/pdf",
])

type MessageWorkspaceProps = {
  thread: MessageThreadSummary | undefined
  composingNew: boolean
  practiceID: string
  locationID: string
  locationName: string
  locations: Array<{ id: string; name: string }>
  supportSessionID: string
  canMutate: boolean
  revision: number
  initialMessage?: Message
  onMessageSent: (message: Message) => void
  onThreadRead: (threadID: string) => void
  onTaskCreated: (task: Task) => void
  onTaskOpen: (task: Task) => void
}

type TimelineSource =
  | { kind: "task"; taskID: string }
  | { kind: "engagement"; practiceID: string; phone: string }

type RecoveryEvidence = {
  outboundTextAfter: boolean
  laterOutboundCallIDs: string[]
  recoveryTask?: Task
}

type RecoveryTaskReference = Pick<Task, "id" | "title" | "state">

export function EngagementWorkspace({
  engagement,
  practiceID,
  supportSessionID,
  canMutate,
  revision,
  initialMessage,
  focusedTask,
  onMessageSent,
  onThreadRead,
  onEngagementRead,
  onTaskUpdated,
  onTextHandled,
  taskCallPending,
  taskCallError,
  onStartTaskCall,
  onTaskCreated,
  onTaskOpen,
}: {
  engagement: EngagementSummary
  practiceID: string
  supportSessionID: string
  canMutate: boolean
  revision: number
  initialMessage?: Message
  focusedTask?: Task
  onMessageSent: (message: Message) => void
  onThreadRead: (threadID: string) => void
  onEngagementRead: (phone: string) => void
  onTaskUpdated: (task: Task) => void
  onTextHandled: (phone: string) => void
  taskCallPending: boolean
  taskCallError: string
  onStartTaskCall: (task: Task) => void
  onTaskCreated: (task: Task) => void
  onTaskOpen: (task: Task) => void
}) {
  const defaultRoute =
    engagement.locations.length === 1 ? engagement.locations[0]!.id : ""
  const [route, setRoute] = useState(defaultRoute)
  const [selectedTask, setSelectedTask] = useState<Task | undefined>(focusedTask)
  const [selectedCall, setSelectedCall] = useState<{
    detail: CallingCall
    history?: CallHistoryItem
    recoveryEligible: boolean
    recoveryTask?: RecoveryTaskReference
  }>()

  async function selectCall(
    callID: string,
    history?: CallHistoryItem,
    evidence?: RecoveryEvidence,
  ) {
    const token = await getAccessToken()
    if (!token) return
    const client = portalClient(token)
    const [result, ...laterCalls] = await Promise.all([
      getCallingCall({ client, path: { callId: callID } }).catch(
        () => undefined,
      ),
      ...(evidence?.laterOutboundCallIDs ?? []).map((laterCallID) =>
        getCallingCall({ client, path: { callId: laterCallID } }).catch(
          () => undefined,
        ),
      ),
    ])
    if (!result?.data) return
    const recoveryTask = evidence?.recoveryTask ?? result.data.recoveryTask
    const connectedCallback = laterCalls.some(
      (candidate) =>
        candidate?.data?.direction === "OUTBOUND" &&
        Boolean(candidate.data.connectedAt),
    )
    const recoveryEligible = Boolean(
      recoveryTask?.state === "OPEN" &&
        (connectedCallback ||
          (Boolean(result.data.voicemail) && evidence?.outboundTextAfter)),
    )
    setSelectedTask(undefined)
    setSelectedCall({
      detail: result.data,
      history,
      recoveryEligible,
      recoveryTask,
    })
  }
  const routeName =
    engagement.locations.find((location) => location.id === route)?.name ??
    "Choose sender route"
  return (
    <section className="flex min-h-0 flex-1">
      <div className="flex min-h-0 min-w-0 flex-1 flex-col">
        <header className="border-b px-4 py-3">
        <div className="flex flex-wrap items-start gap-4">
          <div className="min-w-0 flex-1">
            <p className="text-xs font-medium text-muted-foreground">
              <span>Number inbox</span>
              <span aria-hidden="true"> · </span>
              <span>Unverified phone context</span>
            </p>
            <h1 className="mt-1 truncate text-xl font-semibold tracking-[-0.015em] tabular-nums">
              {formatPhone(engagement.phone)}
            </h1>
            <p className="mt-1 text-xs text-muted-foreground">
              {engagement.displayName ? `${engagement.displayName} · ` : ""}
              {engagement.locations.map((location) => location.name).join(" · ")}
              {engagement.openTaskCount > 0
                ? ` · ${engagement.openTaskCount} open ${engagement.openTaskCount === 1 ? "Task" : "Tasks"}`
                : ""}
            </p>
          </div>
          <div className="flex items-center gap-2">
            {engagement.unread && <Badge variant="secondary">Unread</Badge>}
            <CallingNumberAction locationID={route} phone={engagement.phone} />
            <NativeSelect
              aria-label="Sender route"
              value={route}
              onChange={(event) => setRoute(event.target.value)}
            >
              <NativeSelectOption value="" disabled>
                Choose sender route
              </NativeSelectOption>
              {engagement.locations.map((location) => (
                <NativeSelectOption key={location.id} value={location.id}>
                  Send from {location.name}
                </NativeSelectOption>
              ))}
            </NativeSelect>
          </div>
        </div>
      </header>
      <MessageConversation
        timelineSource={{
          kind: "engagement",
          practiceID,
          phone: engagement.phone,
        }}
        composingNew={false}
        practiceID={practiceID}
        locationID={route}
        routeLabel={routeName}
        initialDestination={engagement.phone}
        supportSessionID={supportSessionID}
        canMutate={canMutate}
        revision={revision}
        initialMessage={initialMessage}
        onMessageSent={onMessageSent}
        onThreadRead={onThreadRead}
        onEngagementRead={onEngagementRead}
        onTaskCreated={onTaskCreated}
        onTextHandled={onTextHandled}
        textNeedsAttention={engagement.textNeedsAttention}
        onTaskOpen={(task) => {
          setSelectedCall(undefined)
          setSelectedTask(task)
          onTaskOpen(task)
        }}
        onCallOpen={(callID, history, evidence) => {
          void selectCall(callID, history, evidence)
        }}
      />
      </div>
      {selectedTask && (
        <SelectedTaskSnapshot
          task={selectedTask}
          supportSessionID={supportSessionID}
          canMutate={canMutate}
          taskCallPending={taskCallPending}
          taskCallError={taskCallError}
          onStartTaskCall={onStartTaskCall}
          onClose={() => setSelectedTask(undefined)}
          onChanged={(task) => {
            setSelectedTask(task.state === "OPEN" ? task : undefined)
            onTaskUpdated(task)
          }}
        />
      )}
      {!selectedTask && selectedCall && (
        <SelectedCallSnapshot
          call={selectedCall.detail}
          history={selectedCall.history}
          recoveryEligible={selectedCall.recoveryEligible}
          recoveryTask={selectedCall.recoveryTask}
          supportSessionID={supportSessionID}
          canMutate={canMutate}
          onClose={() => setSelectedCall(undefined)}
          onTaskUpdated={onTaskUpdated}
        />
      )}
    </section>
  )
}

function SelectedCallSnapshot({
  call,
  history,
  recoveryEligible,
  recoveryTask,
  supportSessionID,
  canMutate,
  onClose,
  onTaskUpdated,
}: {
  call: CallingCall
  history?: CallHistoryItem
  recoveryEligible: boolean
  recoveryTask?: RecoveryTaskReference
  supportSessionID: string
  canMutate: boolean
  onClose: () => void
  onTaskUpdated: (task: Task) => void
}) {
  const [pending, setPending] = useState(false)
  const [error, setError] = useState("")

  async function completeRecovery() {
    if (!recoveryTask) return
    setPending(true)
    setError("")
    const token = await getAccessToken()
    if (!token) {
      setPending(false)
      return
    }
    const client = portalClient(token)
    const current = await readTask({
      client,
      path: { taskId: recoveryTask.id },
    }).catch(() => undefined)
    if (!current?.data) {
      setPending(false)
      setError("The related Task could not be loaded.")
      return
    }
    if (current.data.state === "COMPLETED") {
      setPending(false)
      onTaskUpdated(current.data)
      onClose()
      return
    }
    const completed = await completeTask({
      client,
      path: { taskId: current.data.id },
      body: {
        expectedVersion: current.data.version,
        ...(supportSessionID ? { supportSessionId: supportSessionID } : {}),
      },
    }).catch(() => undefined)
    setPending(false)
    if (!completed?.data) {
      setError("The related Task changed or could not be completed.")
      return
    }
    onTaskUpdated(completed.data)
    onClose()
  }

  return (
    <aside
      aria-label="Selected item"
      className="flex w-80 shrink-0 flex-col border-l bg-muted/15"
    >
      <div className="flex items-center gap-2 border-b px-4 py-2.5">
        <span className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
          {call.voicemail ? "Voicemail" : "Call"}
        </span>
        <Button
          size="icon-sm"
          variant="ghost"
          className="ml-auto"
          aria-label="Close selected item"
          onClick={onClose}
        >
          <XIcon />
        </Button>
      </div>
      <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto p-4">
        <div>
          <Badge variant="outline">
            {call.direction === "INBOUND" ? "Inbound" : "Outbound"}
          </Badge>
          <h2 className="mt-3 text-lg font-semibold">
            {call.voicemail ? "Voicemail" : "Call"} · {formatPhone(call.phone)}
          </h2>
          <p className="mt-1 text-sm text-muted-foreground">
            {call.locationName} · {callStateLabel(call.state)}
          </p>
        </div>
        <SnapshotField
          label="Time"
          value={formatDateTime(history?.startedAt ?? call.connectedAt ?? call.deadline)}
        />
        {history && (
          <>
            <SnapshotField
              label="Outcome"
              value={history.outcome.toLowerCase().replaceAll("_", " ")}
            />
            <SnapshotField
              label="Duration"
              value={formatDuration(history.durationSeconds)}
            />
          </>
        )}
        {call.dispositionOutcome && (
          <SnapshotField
            label="Disposition"
            value={call.dispositionOutcome.toLowerCase().replaceAll("_", " ")}
          />
        )}
        {call.connectedAt && (
          <SnapshotField label="Connected" value={formatDateTime(call.connectedAt)} />
        )}
        {call.voicemail && (
          <>
            {!history && (
              <SnapshotField
                label="Duration"
                value={formatDuration(call.voicemail.durationSeconds)}
              />
            )}
            <VoicemailPlayer call={call} callID={call.id} />
            {recoveryTask && (
              <SnapshotField
                label="Related Task"
                value={recoveryTask.title}
              />
            )}
          </>
        )}
        {call.transferReason && (
          <SnapshotField label="Follow-up" value={call.transferReason} />
        )}
        {canMutate && (
          <div className="mt-auto flex flex-wrap gap-2 pt-2">
            <CallingNumberAction
              locationID={call.locationId}
              phone={call.phone}
            />
            {recoveryEligible && recoveryTask?.state === "OPEN" && (
              <Button disabled={pending} onClick={() => void completeRecovery()}>
                {pending && <Spinner />}
                Looks handled — Mark complete
              </Button>
            )}
          </div>
        )}
        {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
      </div>
    </aside>
  )
}

function SnapshotField({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-baseline justify-between gap-4">
      <p className="text-xs font-medium text-muted-foreground">
        {label}
      </p>
      <p className="text-right text-sm">{value}</p>
    </div>
  )
}

function VoicemailPlayer({
  call,
  callID,
  compact = false,
}: {
  call?: CallingCall
  callID: string
  compact?: boolean
}) {
  const [audioURL, setAudioURL] = useState("")
  const [loading, setLoading] = useState(false)
  const [unavailable, setUnavailable] = useState(
    call?.voicemail?.audioState === "UNAVAILABLE",
  )
  const audioRef = useRef<HTMLAudioElement | null>(null)

  useEffect(() => {
    return () => {
      if (audioURL) URL.revokeObjectURL(audioURL)
    }
  }, [audioURL])

  async function play() {
    if (audioURL) {
      await audioRef.current?.play().catch(() => undefined)
      return
    }
    setLoading(true)
    const token = await getAccessToken()
    if (!token) {
      setLoading(false)
      return
    }
    const detail =
      call ??
      (
        await getCallingCall({
          client: portalClient(token),
          path: { callId: callID },
        }).catch(() => undefined)
      )?.data
    if (!detail?.voicemail || detail.voicemail.audioState !== "READY") {
      setLoading(false)
      setUnavailable(detail?.voicemail?.audioState === "UNAVAILABLE")
      return
    }
    const issued = await issueCallingVoicemailPlayback({
      client: portalClient(token),
      path: { callId: callID },
    }).catch(() => undefined)
    if (!issued?.data) {
      setLoading(false)
      setUnavailable(true)
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
      setUnavailable(true)
      return
    }
    const objectURL = URL.createObjectURL(await response.blob())
    await markTaskRead({
      client: portalClient(token),
      path: { taskId: detail.voicemail.taskId },
      body: { callId: callID },
    }).catch(() => undefined)
    setAudioURL(objectURL)
    setLoading(false)
    window.requestAnimationFrame(() => {
      void audioRef.current?.play().catch(() => undefined)
    })
  }

  if (unavailable) {
    return <p className="text-sm text-muted-foreground">Recording unavailable</p>
  }
  if (audioURL) {
    return (
      <audio
        ref={audioRef}
        aria-label="Voicemail recording"
        className={compact ? "h-9 max-w-full" : undefined}
        controls
        controlsList="nodownload"
        preload="metadata"
        src={audioURL}
      />
    )
  }
  return (
    <Button variant="outline" disabled={loading} onClick={() => void play()}>
      {loading ? <Spinner /> : <PhoneCallIcon />}
      {loading ? "Loading voicemail" : "Play voicemail"}
    </Button>
  )
}

function SelectedTaskSnapshot({
  task,
  supportSessionID,
  canMutate,
  taskCallPending,
  taskCallError,
  onStartTaskCall,
  onClose,
  onChanged,
}: {
  task: Task
  supportSessionID: string
  canMutate: boolean
  taskCallPending: boolean
  taskCallError: string
  onStartTaskCall: (task: Task) => void
  onClose: () => void
  onChanged: (task: Task) => void
}) {
  const [pending, setPending] = useState(false)
  const [error, setError] = useState("")
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(task.title)

  async function saveTitle() {
    const title = draft.trim()
    if (!title || title === task.title) {
      setEditing(false)
      setDraft(task.title)
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
        title,
        ...(supportSessionID ? { supportSessionId: supportSessionID } : {}),
      },
    }).catch(() => undefined)
    setPending(false)
    if (!result?.data) {
      setError("The Task changed or its title could not be saved.")
      return
    }
    setEditing(false)
    onChanged(result.data)
  }

  async function transition() {
    setPending(true)
    setError("")
    const token = await getAccessToken()
    if (!token) {
      setPending(false)
      return
    }
    const request = task.state === "OPEN" ? completeTask : reopenTask
    const result = await request({
      client: portalClient(token),
      path: { taskId: task.id },
      body: {
        expectedVersion: task.version,
        ...(supportSessionID ? { supportSessionId: supportSessionID } : {}),
      },
    }).catch(() => undefined)
    setPending(false)
    if (!result?.data) {
      setError("The Task changed or is temporarily unavailable.")
      return
    }
    onChanged(result.data)
  }

  return (
    <aside
      aria-label="Selected item"
      className="flex w-80 shrink-0 flex-col border-l bg-muted/15"
    >
      <div className="flex items-center gap-2 border-b px-4 py-2.5">
        <span className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
          Task
        </span>
        <Button
          size="icon-sm"
          variant="ghost"
          className="ml-auto"
          aria-label="Close selected item"
          onClick={onClose}
        >
          <XIcon />
        </Button>
      </div>
      <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto p-4">
        <div>
          <Badge variant={task.state === "OPEN" ? "secondary" : "outline"}>
            {task.state === "OPEN" ? "Open" : "Completed"}
          </Badge>
          {editing ? (
            <div className="mt-3 flex items-center gap-1">
              <Input
                autoFocus
                aria-label="Task title"
                maxLength={500}
                value={draft}
                disabled={pending}
                onChange={(event) => setDraft(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === "Enter") void saveTitle()
                  if (event.key === "Escape") {
                    setDraft(task.title)
                    setEditing(false)
                  }
                }}
              />
              <Button
                size="icon"
                aria-label="Save title"
                disabled={pending}
                onClick={() => void saveTitle()}
              >
                {pending ? <Spinner /> : <CheckIcon />}
              </Button>
            </div>
          ) : (
            <div className="mt-3 flex items-start gap-1">
              <h2 className="min-w-0 flex-1 text-lg font-semibold tracking-[-0.015em]">
                {task.title}
              </h2>
              {task.state === "OPEN" && canMutate && (
                <Button
                  size="icon-sm"
                  variant="ghost"
                  aria-label="Rename task"
                  onClick={() => {
                    setDraft(task.title)
                    setEditing(true)
                  }}
                >
                  <PencilIcon />
                </Button>
              )}
            </div>
          )}
          <p className="mt-1 text-sm tabular-nums text-muted-foreground">
            {formatPhone(task.phone)} · {task.locationName}
          </p>
        </div>
        {task.sourceMessage && (
          <section className="rounded-lg bg-background/80 p-3">
            <h3 className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
              Instructions
            </h3>
            <p className="mt-1 whitespace-pre-wrap text-sm leading-relaxed">
              {task.sourceMessage}
            </p>
          </section>
        )}
        {(task.category || task.callerName) && (
          <section className="rounded-lg bg-background/80 p-3">
            <h3 className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
              Source context
            </h3>
            <p className="mt-1 text-sm">
              {[task.callerName, task.category?.replaceAll("_", " ")]
                .filter(Boolean)
                .join(" · ")}
            </p>
          </section>
        )}
        {canMutate && (
          <div className="mt-auto flex flex-col gap-2 pt-2">
            {task.state === "OPEN" && (
              <Button
                variant="outline"
                disabled={taskCallPending}
                onClick={() => onStartTaskCall(task)}
              >
                <PhoneCallIcon />
                {taskCallPending ? "Preparing…" : "Call"}
              </Button>
            )}
            <Button disabled={pending} onClick={() => void transition()}>
              {pending && <Spinner />}
              {task.state === "OPEN" ? "Complete" : "Reopen"}
            </Button>
          </div>
        )}
        {taskCallError && (
          <p role="alert" className="text-sm text-destructive">{taskCallError}</p>
        )}
        {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
      </div>
    </aside>
  )
}

export function MessageWorkspace({
  thread,
  composingNew,
  practiceID,
  locationID,
  locationName,
  locations,
  supportSessionID,
  canMutate,
  revision,
  initialMessage,
  onMessageSent,
  onThreadRead,
  onTaskCreated,
  onTaskOpen,
}: MessageWorkspaceProps) {
  const [route, setRoute] = useState(thread?.locationId ?? locationID)

  const routeName =
    locations.find((location) => location.id === route)?.name ?? locationName

  if (!thread && !composingNew) {
    return (
      <section
        aria-label="No conversation selected"
        className="flex min-h-0 flex-1 items-center justify-center p-8"
      >
        <div className="max-w-sm text-center">
          <MessageSquareIcon
            className="mx-auto size-8 text-muted-foreground"
            aria-hidden="true"
          />
          <h1 className="mt-4 text-lg font-semibold">Select a conversation</h1>
          <p className="mt-2 text-sm text-muted-foreground">
            The timeline combines texts, calls, and follow-up Tasks for one
            exact phone number across authorized offices.
          </p>
        </div>
      </section>
    )
  }

  return (
    <section className="flex min-h-0 flex-1 flex-col">
      <header className="border-b px-5 py-4">
        <div className="flex flex-wrap items-start gap-4">
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2">
              <Badge variant="outline">New text</Badge>
              <span className="text-xs font-medium text-muted-foreground">
                {composingNew ? locationName : "Unverified phone context"}
              </span>
            </div>
            <h1 className="mt-2 truncate text-xl font-semibold tracking-[-0.015em] tabular-nums">
              {thread ? formatPhone(thread.externalPhone) : "Choose a number"}
            </h1>
            <p className="mt-1 text-xs text-muted-foreground">
              {thread?.displayName ? `${thread.displayName} · ` : ""}
              {thread
                ? "Messages, calls, and Tasks across authorized offices"
                : "Choose an exact destination number"}
            </p>
          </div>
          <div className="flex items-center gap-2">
            {thread && (
              <NativeSelect
                aria-label="Sender route"
                value={route}
                onChange={(event) => setRoute(event.target.value)}
              >
                {locations.map((location) => (
                  <NativeSelectOption key={location.id} value={location.id}>
                    Send from {location.name}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            )}
            {thread?.outboundBlocked && (
              <Badge variant="destructive">Patient opted out</Badge>
            )}
          </div>
        </div>
      </header>
      <MessageConversation
        key={thread?.id ?? `new:${locationID}`}
        thread={thread}
        timelineSource={
          thread
            ? {
                kind: "engagement",
                practiceID,
                phone: thread.externalPhone,
              }
            : undefined
        }
        composingNew={composingNew}
        practiceID={practiceID}
        locationID={thread ? route : locationID}
        routeLabel={thread ? routeName : locationName}
        initialDestination={thread?.externalPhone}
        supportSessionID={supportSessionID}
        canMutate={canMutate}
        revision={revision}
        initialMessage={initialMessage}
        onMessageSent={onMessageSent}
        onThreadRead={onThreadRead}
        onTaskCreated={onTaskCreated}
        onTaskOpen={onTaskOpen}
      />
    </section>
  )
}

export function TaskMessageConversation({
  task,
  supportSessionID,
  canMutate,
  revision,
  onTaskCreated,
  onMessageSent,
}: {
  task: Task
  supportSessionID: string
  canMutate: boolean
  revision: number
  onTaskCreated: (task: Task) => void
  onMessageSent: () => void
}) {
  const [createdMessage, setCreatedMessage] = useState<Message>()
  const threadID =
    task.messageThreadId ||
    task.conversationThreadId ||
    createdMessage?.thread.id ||
    ""
  return (
    <section
      aria-label="Task conversation"
      className="flex min-h-[22rem] flex-1 flex-col border-b"
    >
      <div className="border-b bg-muted/20 px-5 py-2.5">
        <p className="text-xs font-medium tabular-nums text-muted-foreground">
          Conversation · {formatPhone(task.phone)}
        </p>
      </div>
      <MessageConversation
        threadID={threadID}
        timelineSource={{ kind: "task", taskID: task.id }}
        composingNew={false}
        practiceID={task.practiceId}
        locationID={task.locationId}
        routeLabel={task.locationName}
        taskID={task.id}
        taskOpen={task.state === "OPEN"}
        initialDestination={task.phone}
        supportSessionID={supportSessionID}
        canMutate={canMutate}
        revision={revision}
        initialMessage={createdMessage}
        onMessageSent={(message) => {
          setCreatedMessage(message)
          onMessageSent()
        }}
        onThreadRead={() => undefined}
        onTaskCreated={onTaskCreated}
      />
    </section>
  )
}

function MessageConversation({
  thread,
  threadID = thread?.id ?? "",
  timelineSource,
  composingNew,
  practiceID,
  locationID,
  routeLabel,
  taskID,
  taskOpen = true,
  initialDestination,
  supportSessionID,
  canMutate,
  revision,
  initialMessage,
  onMessageSent,
  onThreadRead,
  onEngagementRead,
  onTaskCreated,
  onTaskOpen,
  onCallOpen,
  onTextHandled,
  textNeedsAttention = false,
}: {
  thread?: MessageThreadSummary
  threadID?: string
  timelineSource?: TimelineSource
  composingNew: boolean
  practiceID: string
  locationID: string
  routeLabel?: string
  taskID?: string
  taskOpen?: boolean
  initialDestination?: string
  supportSessionID: string
  canMutate: boolean
  revision: number
  initialMessage?: Message
  onMessageSent: (message: Message) => void
  onThreadRead: (threadID: string) => void
  onEngagementRead?: (phone: string) => void
  onTaskCreated: (task: Task) => void
  onTaskOpen?: (task: Task) => void
  onCallOpen?: (
    callID: string,
    history?: CallHistoryItem,
    evidence?: RecoveryEvidence,
  ) => void
  onTextHandled?: (phone: string) => void
  textNeedsAttention?: boolean
}) {
  const timelineKind = timelineSource?.kind
  const timelineTaskID =
    timelineSource?.kind === "task" ? timelineSource.taskID : ""
  const timelinePracticeID =
    timelineSource?.kind === "engagement" ? timelineSource.practiceID : ""
  const timelinePhone =
    timelineSource?.kind === "engagement" ? timelineSource.phone : ""
  const timelineKey = timelineKind
    ? timelineKind === "task"
      ? `task:${timelineTaskID}`
      : `engagement:${timelinePracticeID}:${timelinePhone}`
    : threadID
  const committedItem = initialMessage
    ? messageTimelineItem(initialMessage)
    : undefined
  const [items, setItems] = useState<ConversationTimelineItem[]>(
    committedItem ? [committedItem] : [],
  )
  const [cursor, setCursor] = useState("")
  const [loading, setLoading] = useState(Boolean(timelineKey && !committedItem))
  const [loadingOlder, setLoadingOlder] = useState(false)
  const [findQuery, setFindQuery] = useState("")
  const [error, setError] = useState("")
  const [handledThrough, setHandledThrough] = useState("")
  const [handling, setHandling] = useState(false)
  const [noteOpen, setNoteOpen] = useState(false)
  const [noteBody, setNoteBody] = useState("")
  const [noteSaving, setNoteSaving] = useState(false)
  const generation = useRef(0)
  const committedMessage = useRef<
    { id: string; visibleUntil: number } | undefined
  >(
    initialMessage
      ? { id: initialMessage.id, visibleUntil: Number.POSITIVE_INFINITY }
      : undefined,
  )
  const scroller = useRef<HTMLDivElement | null>(null)
  const atLatest = useRef(true)
  const pendingRealtime = useRef(false)
  const initialized = useRef(false)
  const onThreadReadRef = useRef(onThreadRead)
  const onEngagementReadRef = useRef(onEngagementRead)
  const conversationThread =
    (thread?.locationId === locationID ? thread : undefined) ??
    items.find(
      (item) => item.message?.thread.locationId === locationID,
    )?.message?.thread
  const readableThreadID = conversationThread?.id ?? threadID

  useEffect(() => {
    onThreadReadRef.current = onThreadRead
  }, [onThreadRead])

  useEffect(() => {
    onEngagementReadRef.current = onEngagementRead
  }, [onEngagementRead])

  useEffect(() => {
    if (initialMessage) {
      committedMessage.current = {
        id: initialMessage.id,
        visibleUntil: Date.now() + 750,
      }
    }
  }, [initialMessage, threadID])

  const loadPage = useCallback(
    async (token: string, cursor = "") => {
      if (timelineKind === "task") {
        return getTaskEngagementHistory({
          client: portalClient(token),
          path: { taskId: timelineTaskID },
          query: { ...(cursor ? { cursor } : {}), limit: 50 },
        }).catch(() => undefined)
      }
      if (timelineKind === "engagement") {
        return getEngagementTimeline({
          client: portalClient(token),
          path: { phone: timelinePhone },
          query: {
            practiceId: timelinePracticeID,
            ...(cursor ? { cursor } : {}),
            limit: 50,
          },
        }).catch(() => undefined)
      }
      if (!threadID) return undefined
      return getMessageThreadTimeline({
        client: portalClient(token),
        path: { threadId: threadID },
        query: { ...(cursor ? { cursor } : {}), limit: 50 },
      }).catch(() => undefined)
    },
    [
      threadID,
      timelineKind,
      timelinePhone,
      timelinePracticeID,
      timelineTaskID,
    ],
  )

  const loadLatest = useCallback(
    async (scroll = false) => {
      if (!timelineKey) return
      const requestGeneration = ++generation.current
      if (!initialized.current) setLoading(true)
      const token = await getAccessToken()
      if (!token) return
      const result = await loadPage(token)
      if (requestGeneration !== generation.current) return
      setLoading(false)
      if (!result?.data) {
        setError("The conversation could not be loaded.")
        return
      }
      const currentScroller = scroller.current
      const preservedScrollTop = currentScroller &&
        currentScroller.scrollHeight - currentScroller.scrollTop - currentScroller.clientHeight >= 72
        ? currentScroller.scrollTop
        : undefined
      initialized.current = true
      setItems((current) => {
        const committed = committedMessage.current
        if (committed && Date.now() < committed.visibleUntil) {
          const responseItem = current.find((item) => item.id === committed.id)
          if (responseItem) {
            return [
              ...result.data.items.filter((item) => item.id !== committed.id),
              responseItem,
            ]
          }
        }
        committedMessage.current = undefined
        return result.data.items
      })
      setCursor(result.data.nextCursor)
      if (preservedScrollTop !== undefined) {
        window.requestAnimationFrame(() => {
          if (scroller.current) scroller.current.scrollTop = preservedScrollTop
        })
      } else if (scroll) {
        window.requestAnimationFrame(() =>
          scroller.current?.scrollTo({
            top: scroller.current.scrollHeight,
            behavior: "smooth",
          }),
        )
      }
    },
    [loadPage, timelineKey],
  )

  useEffect(() => {
    const committed = committedMessage.current
    if (!committed) return
    const firstRefresh = Math.max(0, committed.visibleUntil - Date.now())
    const timeouts = [firstRefresh, firstRefresh + 2_000, firstRefresh + 6_000]
      .map((delay) => window.setTimeout(() => void loadLatest(true), delay))
    return () => timeouts.forEach((timeout) => window.clearTimeout(timeout))
  }, [loadLatest, threadID])

  const markRead = useCallback(async () => {
    if (timelineKind === "engagement") {
      const token = await getAccessToken()
      if (!token) return
      const result = await markEngagementRead({
        client: portalClient(token),
        path: { phone: timelinePhone },
        body: { practiceId: timelinePracticeID },
      }).catch(() => undefined)
      if (result?.response?.ok) {
        onEngagementReadRef.current?.(timelinePhone)
      }
      return
    }
    if (!readableThreadID) return
    const token = await getAccessToken()
    if (!token) return
    const result = await markMessageThreadRead({
      client: portalClient(token),
      path: { threadId: readableThreadID },
      body: {
        ...(supportSessionID ? { supportSessionId: supportSessionID } : {}),
      },
    }).catch(() => undefined)
    if (result?.response?.ok) onThreadReadRef.current(readableThreadID)
  }, [
    readableThreadID,
    supportSessionID,
    timelineKind,
    timelinePhone,
    timelinePracticeID,
  ])

  useEffect(() => {
    if (!timelineKey) return
    const timeout = window.setTimeout(() => {
      void loadLatest(true)
      void markRead()
    }, 0)
    return () => window.clearTimeout(timeout)
  }, [loadLatest, markRead, threadID, timelineKey])

  useEffect(() => {
    if (!initialized.current || !timelineKey) return
    const container = scroller.current
    const isAtLatest = container
      ? container.scrollHeight - container.scrollTop - container.clientHeight < 72
      : atLatest.current
    atLatest.current = isAtLatest
    if (isAtLatest) {
      void loadLatest(true)
      void markRead()
    } else {
      pendingRealtime.current = true
    }
  }, [loadLatest, markRead, revision, threadID, timelineKey])

  async function loadOlder() {
    if (!cursor || loadingOlder) return
    setLoadingOlder(true)
    const token = await getAccessToken()
    if (!token) {
      setLoadingOlder(false)
      return
    }
    const container = scroller.current
    const previousHeight = container?.scrollHeight ?? 0
    const result = await loadPage(token, cursor)
    setLoadingOlder(false)
    if (!result?.data) return
    setItems((current) => [...result.data.items, ...current])
    setCursor(result.data.nextCursor)
    window.requestAnimationFrame(() => {
      if (container)
        container.scrollTop += container.scrollHeight - previousHeight
    })
  }

  const composerThreadID =
    conversationThread?.id ??
    (timelineSource?.kind === "engagement" ? "" : threadID)
  const consolidatedItems = consolidateTaskActivity(items)
  const visibleItems = findQuery.trim()
    ? consolidatedItems.filter((item) =>
        timelineItemText(item).includes(findQuery.trim().toLowerCase()),
      )
    : consolidatedItems
  const messageItems = items.filter(
    (item): item is ConversationTimelineItem & { message: Message } =>
      item.type === "MESSAGE" && Boolean(item.message),
  )
  const latestMessage = messageItems.at(-1)?.message
  const handledEligible = Boolean(
    textNeedsAttention &&
      latestMessage?.direction === "OUTBOUND" &&
      latestMessage.id !== handledThrough &&
      messageItems.some((item) => item.message.direction === "INBOUND"),
  )

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="bg-muted/20 px-4 py-2">
        <div className="relative mx-auto max-w-5xl">
          <SearchIcon className="pointer-events-none absolute top-2.5 left-2.5 size-4 text-muted-foreground" />
          <Input
            aria-label="Find in history"
            className="h-9 bg-background pl-8"
            placeholder="Find in loaded history"
            value={findQuery}
            onChange={(event) => setFindQuery(event.target.value)}
          />
        </div>
      </div>
      <div
        ref={scroller}
        data-testid="message-timeline"
        className="relative min-h-0 flex-1 overflow-y-auto bg-muted/[0.08] px-5 py-4"
        onScroll={(event) => {
          const element = event.currentTarget
          const nowAtLatest =
            element.scrollHeight - element.scrollTop - element.clientHeight < 72
          atLatest.current = nowAtLatest
          if (nowAtLatest && pendingRealtime.current) {
            pendingRealtime.current = false
            void loadLatest(true)
            void markRead()
          }
        }}
      >
        <div className="mx-auto flex max-w-5xl flex-col gap-2.5">
          {cursor && (
            <Button
              size="sm"
              variant="outline"
              className="mx-auto bg-background"
              disabled={loadingOlder}
              onClick={() => void loadOlder()}
            >
              {loadingOlder ? <Spinner /> : <RefreshCwIcon />}
              Earlier activity
            </Button>
          )}
          {loading && (
            <div className="flex justify-center py-10 text-muted-foreground">
              <Spinner />
              <span className="sr-only">Loading conversation</span>
            </div>
          )}
          {!loading &&
            visibleItems.map((item, index) => (
              <div key={`${item.type}:${item.id}`} className="contents">
                {isDateBoundary(visibleItems[index - 1], item) && (
                  <div
                    role="separator"
                    className="flex justify-center py-1 text-xs font-medium text-muted-foreground"
                  >
                    <span className="rounded-full bg-muted px-2.5 py-1">
                      {formatTimelineDate(item.occurredAt)}
                    </span>
                  </div>
                )}
                <TimelineEntry
                  item={item}
                  canMutate={canMutate}
                  supportSessionID={supportSessionID}
                  onChanged={() => void loadLatest(true)}
                  onTaskCreated={onTaskCreated}
                  onTaskOpen={onTaskOpen}
                  groupedWithNext={areConsecutiveMessages(
                    item,
                    visibleItems[index + 1],
                  )}
                  onCallOpen={
                    onCallOpen
                      ? (callID, history) =>
                          onCallOpen(
                            callID,
                            history,
                            recoveryEvidence(items, history),
                          )
                      : undefined
                  }
                />
              </div>
            ))}
          {!loading && items.length === 0 && !composingNew && (
            <div className="mx-auto my-10 max-w-sm border bg-background p-5 text-center">
              <p className="text-sm font-medium">No activity yet</p>
              <p className="mt-1 text-xs text-muted-foreground">
                Messages, calls, and Tasks for this exact number appear here.
              </p>
            </div>
          )}
          {!loading && items.length > 0 && visibleItems.length === 0 && (
            <div className="mx-auto my-10 border bg-background p-5 text-center text-sm">
              No activity on this page matches “{findQuery.trim()}”.
            </div>
          )}
        </div>
      </div>
      {error && (
        <Alert variant="destructive" className="rounded-none border-x-0">
          <AlertTitle>Conversation unavailable</AlertTitle>
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      {handledEligible && latestMessage && (
        <div className="flex items-center justify-end gap-3 px-4 pt-2">
          <p className="text-xs text-muted-foreground">
            The latest inbound Text has an outbound reply.
          </p>
          <Button
            size="sm"
            variant="outline"
            disabled={handling}
            onClick={async () => {
              setHandling(true)
              const phone = timelinePhone || latestMessage.thread.externalPhone
              const token = await getAccessToken()
              const result = token
                ? await markEngagementTextHandled({
                    client: portalClient(token),
                    path: { phone },
                    body: {
                      practiceId: practiceID,
                      evidenceMessageId: latestMessage.id,
                      ...(supportSessionID
                        ? { supportSessionId: supportSessionID }
                        : {}),
                    },
                  }).catch(() => undefined)
                : undefined
              setHandling(false)
              if (!result?.response?.ok) {
                setError("Text attention changed. Refresh and try again.")
                return
              }
              setHandledThrough(latestMessage.id)
              onTextHandled?.(phone)
            }}
          >
            Looks handled — Mark complete
          </Button>
        </div>
      )}
      {canMutate &&
        locationID &&
        (timelinePhone || conversationThread?.externalPhone) && (
          <div className="px-4 py-2">
            {!noteOpen ? (
              <div className="flex justify-end">
                <Button
                  size="sm"
                  variant="ghost"
                  onClick={() => setNoteOpen(true)}
                >
                  <StickyNoteIcon />
                  Add staff note
                </Button>
              </div>
            ) : (
              <form
                className="mx-auto flex max-w-5xl flex-col gap-2 rounded-lg bg-muted/40 p-3"
                onSubmit={async (event) => {
                  event.preventDefault()
                  const body = noteBody.trim()
                  const phone =
                    timelinePhone || conversationThread?.externalPhone || ""
                  if (!body || !phone || noteSaving) return
                  setNoteSaving(true)
                  const token = await getAccessToken()
                  const result = token
                    ? await createStaffNote({
                        client: portalClient(token),
                        path: { phone },
                        body: {
                          practiceId: practiceID,
                          locationId: locationID,
                          body,
                          ...(supportSessionID
                            ? { supportSessionId: supportSessionID }
                            : {}),
                        },
                      }).catch(() => undefined)
                    : undefined
                  setNoteSaving(false)
                  if (!result?.data) {
                    setError("The staff note could not be saved.")
                    return
                  }
                  setError("")
                  setNoteBody("")
                  setNoteOpen(false)
                  atLatest.current = true
                  await loadLatest(true)
                }}
              >
                <Textarea
                  aria-label="Staff note"
                  maxLength={2500}
                  placeholder="Add context for staff…"
                  value={noteBody}
                  onChange={(event) => setNoteBody(event.target.value)}
                />
                <div className="flex justify-end gap-2">
                  <Button
                    type="button"
                    size="sm"
                    variant="ghost"
                    disabled={noteSaving}
                    onClick={() => {
                      setNoteOpen(false)
                      setNoteBody("")
                    }}
                  >
                    Cancel
                  </Button>
                  <Button
                    type="submit"
                    size="sm"
                    disabled={!noteBody.trim() || noteSaving}
                  >
                    {noteSaving ? <Spinner /> : <StickyNoteIcon />}
                    Save note
                  </Button>
                </div>
              </form>
            )}
          </div>
        )}
      <MessageComposer
        thread={conversationThread}
        threadID={composerThreadID}
        practiceID={practiceID}
        locationID={locationID}
        routeLabel={routeLabel}
        taskID={taskID}
        initialDestination={initialDestination}
        destinationLocked={Boolean(initialDestination)}
        supportSessionID={supportSessionID}
        disabled={
          !canMutate ||
          !locationID ||
          !taskOpen ||
          Boolean(conversationThread?.outboundBlocked)
        }
        disabledReason={
          !canMutate
            ? "Read only"
            : !locationID
              ? "Choose an authorized sender route"
            : !taskOpen
              ? "Reopen this Task to send a message"
              : conversationThread?.outboundBlocked
                ? "Outbound messaging is blocked after STOP"
                : ""
        }
        onSent={(message) => {
          atLatest.current = true
          if (message.delivery === "Sending") {
            committedMessage.current = {
              id: message.id,
              visibleUntil: Date.now() + 750,
            }
            window.setTimeout(() => void loadLatest(true), 750)
          }
          setLoading(false)
          setItems((current) => [
            ...current.filter((item) => item.id !== message.id),
            messageTimelineItem(message),
          ])
          onMessageSent(message)
        }}
      />
    </div>
  )
}

function TimelineEntry({
  item,
  canMutate,
  supportSessionID,
  onChanged,
  onTaskCreated,
  onTaskOpen,
  onCallOpen,
  groupedWithNext,
}: {
  item: ConversationTimelineItem
  canMutate: boolean
  supportSessionID: string
  onChanged: () => void
  onTaskCreated: (task: Task) => void
  onTaskOpen?: (task: Task) => void
  onCallOpen?: (callID: string, history?: CallHistoryItem) => void
  groupedWithNext: boolean
}) {
  if (item.type === "MESSAGE" && item.message) {
    return (
      <MessageEntry
        message={item.message}
        canMutate={canMutate}
        supportSessionID={supportSessionID}
        onChanged={onChanged}
        onTaskCreated={onTaskCreated}
        groupedWithNext={groupedWithNext}
      />
    )
  }
  if (item.type === "CALL" && item.call) {
    const presentation = callPresentation(item.call)
    const card = (
      <TimelineCard
        icon={presentation.icon}
        kind={presentation.label}
        meta={item.call.locationName}
        occurredAt={item.occurredAt}
        title={presentation.title}
        detail={presentation.detail}
        tone={presentation.tone}
        side={item.call.direction === "OUTBOUND" ? "outbound" : "inbound"}
      />
    )
    return (
      <article>
        {onCallOpen ? (
          <button
            type="button"
            aria-label={`${presentation.label}: ${presentation.title}. ${presentation.detail}. ${item.call.locationName}, ${formatTimelineTime(item.occurredAt)}. Open details`}
            className="w-full cursor-pointer text-left focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
            onClick={() => onCallOpen(item.call!.id, item.call!)}
          >
            {card}
          </button>
        ) : (
          card
        )}
        {item.call.outcome === "VOICEMAIL" && (
          <div className="flex w-full justify-start pr-12">
            <div className="mt-2 w-full max-w-lg pl-10">
              <VoicemailPlayer callID={item.call.id} compact />
            </div>
          </div>
        )}
      </article>
    )
  }
  if (item.type === "TASK" && item.task) {
    const task = item.task
    const activity = taskActivityLabel(item.taskActivity, task.state)
    const card = (
      <TimelineCard
        icon={<CheckSquareIcon />}
        kind="Task"
        meta={`${activity} · ${task.locationName}`}
        occurredAt={item.occurredAt}
        title={task.title}
        tone="task"
        side="outbound"
      />
    )
    return onTaskOpen ? (
      <button
        type="button"
        aria-label={`Task: ${task.title}. ${activity} at ${task.locationName}, ${formatTimelineTime(item.occurredAt)}. Open details`}
        className="w-full cursor-pointer text-left focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
        onClick={() => onTaskOpen(task)}
      >
        {card}
      </button>
    ) : (
      card
    )
  }
  if (item.type === "NOTE" && item.note) {
    return (
      <article>
        <TimelineCard
          icon={<StickyNoteIcon />}
          kind="Staff note"
          meta={item.note.locationName}
          occurredAt={item.occurredAt}
          title={item.note.body}
          tone="note"
          side="outbound"
        />
      </article>
    )
  }
  return null
}

function recoveryEvidence(
  items: ConversationTimelineItem[],
  call?: CallHistoryItem,
): RecoveryEvidence | undefined {
  if (!call || (call.outcome !== "MISSED" && call.outcome !== "VOICEMAIL")) {
    return undefined
  }
  const callStartedAt = new Date(call.startedAt).getTime()
  const recoveryTask = items.find(
    (item) =>
      item.type === "TASK" &&
      item.task?.state === "OPEN" &&
      (item.task.callId === call.id ||
        item.task.interactions.some(
          (interaction) => interaction.callId === call.id,
        )),
  )?.task
  return {
    outboundTextAfter: items.some(
      (item) =>
        item.type === "MESSAGE" &&
        item.message?.direction === "OUTBOUND" &&
        new Date(item.occurredAt).getTime() > callStartedAt,
    ),
    laterOutboundCallIDs: items.flatMap((item) =>
      item.type === "CALL" &&
      item.call?.direction === "OUTBOUND" &&
      new Date(item.occurredAt).getTime() > callStartedAt
        ? [item.call.id]
        : [],
    ),
    ...(recoveryTask ? { recoveryTask } : {}),
  }
}

function areConsecutiveMessages(
  left?: ConversationTimelineItem,
  right?: ConversationTimelineItem,
) {
  return Boolean(
    left?.type === "MESSAGE" &&
      right?.type === "MESSAGE" &&
      left.message?.direction === right.message?.direction &&
      left.message?.thread.locationId === right.message?.thread.locationId,
  )
}

function consolidateTaskActivity(items: ConversationTimelineItem[]) {
  const latestByTask = new Map<string, ConversationTimelineItem>()
  for (const item of items) {
    if (item.type !== "TASK" || !item.task) continue
    const current = latestByTask.get(item.task.id)
    if (
      !current ||
      new Date(item.occurredAt).getTime() >=
        new Date(current.occurredAt).getTime()
    ) {
      latestByTask.set(item.task.id, item)
    }
  }
  return items.filter(
    (item) =>
      item.type !== "TASK" ||
      !item.task ||
      latestByTask.get(item.task.id) === item,
  )
}

function messageTimelineItem(message: Message): ConversationTimelineItem {
  return {
    type: "MESSAGE",
    id: message.id,
    occurredAt: message.createdAt,
    message,
  }
}

function MessageEntry({
  message,
  canMutate,
  supportSessionID,
  onChanged,
  onTaskCreated,
  groupedWithNext,
}: {
  message: Message
  canMutate: boolean
  supportSessionID: string
  onChanged: () => void
  onTaskCreated: (task: Task) => void
  groupedWithNext: boolean
}) {
  const [pending, setPending] = useState(false)
  const [error, setError] = useState("")
  const sendAgainAttemptKey = useRef("")
  const outbound = message.direction === "OUTBOUND"

  async function createTask() {
    setPending(true)
    setError("")
    const token = await getAccessToken()
    if (!token) {
      setPending(false)
      return
    }
    const result = await createMessageFollowUpTask({
      client: portalClient(token),
      path: { messageId: message.id },
      body: {
        ...(supportSessionID ? { supportSessionId: supportSessionID } : {}),
      },
    }).catch(() => undefined)
    setPending(false)
    if (!result?.data) {
      setError("A follow-up Task could not be created.")
      return
    }
    onTaskCreated(result.data)
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
    const token = await getAccessToken()
    if (!token) {
      setPending(false)
      return
    }
    if (!sendAgainAttemptKey.current) {
      sendAgainAttemptKey.current = crypto.randomUUID()
    }
    const result = await sendMessageAgain({
      client: portalClient(token),
      path: { messageId: message.id },
      body: {
        idempotencyKey: sendAgainAttemptKey.current,
        duplicateRiskAcknowledged: duplicateRisk,
        ...(supportSessionID ? { supportSessionId: supportSessionID } : {}),
      },
    }).catch(() => undefined)
    setPending(false)
    if (!result?.data) {
      setError("A new send attempt could not be created.")
      return
    }
    sendAgainAttemptKey.current = ""
    onChanged()
  }

  return (
    <article
      className={cn(
        "group/message flex w-full",
        outbound ? "justify-end pl-12" : "justify-start pr-12",
      )}
    >
      <div
        className={cn(
          "relative max-w-[min(36rem,78%)] rounded-xl border px-3 py-2.5 shadow-xs",
          outbound
            ? "border-sky-500/20 bg-sky-500/10"
            : "border-border/70 bg-muted/60",
        )}
      >
        {message.body && (
          <p className="whitespace-pre-wrap text-sm leading-relaxed">
            {message.body}
          </p>
        )}
        {message.attachment && (
          <AttachmentCard
            attachment={message.attachment}
            canMutate={canMutate}
            supportSessionID={supportSessionID}
            onChanged={onChanged}
            inverse={false}
          />
        )}
        {!groupedWithNext && (
          <div
            className={cn(
              "mt-2 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs tabular-nums",
              "text-muted-foreground",
            )}
          >
            <span>{outbound ? "Outbound text" : "Inbound text"}</span>
            <span aria-hidden="true">·</span>
            <time dateTime={message.createdAt}>
              {formatTimelineTime(message.createdAt)}
            </time>
            {outbound && (
              <>
                <span aria-hidden="true">·</span>
                <span>{message.delivery}</span>
              </>
            )}
            {message.safeFailureCode && (
              <>
                <span aria-hidden="true">·</span>
                <span>{message.safeFailureCode}</span>
              </>
            )}
          </div>
        )}
        {canMutate && !message.taskId && (
          <Button
            size="sm"
            variant="ghost"
            className={cn(
              "pointer-events-none absolute top-1/2 size-7 -translate-y-1/2 p-0 opacity-0 transition-opacity group-hover/message:pointer-events-auto group-hover/message:opacity-100 focus-visible:pointer-events-auto focus-visible:opacity-100",
              outbound ? "-left-9" : "-right-9",
            )}
            title="Create follow-up Task"
            disabled={pending}
            onClick={() => void createTask()}
          >
            <CheckSquareIcon />
            <span className="sr-only">Create Task</span>
          </Button>
        )}
        {canMutate &&
          (pending ||
            (outbound &&
              (message.delivery === "Failed" ||
                message.delivery === "Status unknown"))) && (
          <div className="mt-1 flex flex-wrap justify-end gap-1">
            {outbound &&
              (message.delivery === "Failed" ||
                message.delivery === "Status unknown") && (
                <Button
                  size="sm"
                  variant="outline"
                  className="h-7"
                  disabled={pending}
                  onClick={() => void sendAgain()}
                >
                  {message.delivery === "Status unknown" && (
                    <AlertTriangleIcon />
                  )}
                  Send again
                </Button>
              )}
            {pending && <Spinner />}
          </div>
          )}
        {error && (
          <p
            role="alert"
            className={cn(
              "mt-2 text-xs",
              "text-destructive",
            )}
          >
            {error}
          </p>
        )}
      </div>
    </article>
  )
}

function callPresentation(call: CallHistoryItem): {
  icon: React.ReactNode
  label: string
  title: string
  detail: string
  tone: "call" | "missed" | "voicemail"
} {
  const duration = formatDuration(call.durationSeconds)
  if (call.outcome === "VOICEMAIL") {
    return {
      icon: <VoicemailIcon />,
      label: "Voicemail",
      title: call.transferReason || "Voicemail received",
      detail: duration,
      tone: "voicemail",
    }
  }
  if (
    call.direction === "INBOUND" &&
    (call.outcome === "MISSED" || call.outcome === "UNANSWERED")
  ) {
    return {
      icon: <PhoneMissedIcon />,
      label: "Missed call",
      title: call.transferReason || "No answer",
      detail: duration,
      tone: "missed",
    }
  }
  return {
    icon:
      call.direction === "INBOUND" ? <PhoneIncomingIcon /> : <PhoneOutgoingIcon />,
    label: call.direction === "INBOUND" ? "Inbound call" : "Outbound call",
    title: call.transferReason || "Phone conversation",
    detail: `${humanizeCallOutcome(call.outcome)} · ${duration}`,
    tone: "call",
  }
}

function humanizeCallOutcome(outcome: CallHistoryItem["outcome"]) {
  const words = outcome.toLowerCase().replaceAll("_", " ")
  return words.charAt(0).toUpperCase() + words.slice(1)
}

function TimelineCard({
  icon,
  kind,
  meta,
  occurredAt,
  title,
  detail,
  tone,
  side,
}: {
  icon: React.ReactNode
  kind: string
  meta: string
  occurredAt: string
  title: string
  detail?: string
  tone: "call" | "missed" | "voicemail" | "task" | "note"
  side: "inbound" | "outbound"
}) {
  return (
    <div
      data-timeline-side={side}
      className={cn(
        "flex w-full",
        side === "outbound" ? "justify-end pl-12" : "justify-start pr-12",
      )}
    >
      <div
        className="w-full max-w-lg rounded-xl border bg-background px-3 py-2.5 shadow-xs"
      >
        <div className="flex items-start gap-2.5">
          <span
            className={cn(
              "flex size-7 shrink-0 items-center justify-center rounded-md [&_svg]:size-3.5",
              tone === "call" && "bg-sky-500/10 text-sky-700 dark:text-sky-300",
              tone === "missed" && "bg-amber-500/10 text-amber-700 dark:text-amber-300",
              tone === "voicemail" && "bg-violet-500/10 text-violet-700 dark:text-violet-300",
              tone === "task" && "bg-emerald-500/10 text-emerald-700 dark:text-emerald-300",
              tone === "note" && "bg-muted text-muted-foreground",
            )}
          >
            {icon}
          </span>
          <div className="min-w-0 flex-1">
            <div className="flex items-baseline gap-2">
              <span className="text-xs font-semibold uppercase tracking-wide">
                {kind}
              </span>
              <span className="truncate text-xs text-muted-foreground">
                {meta}
              </span>
              <time
                dateTime={occurredAt}
                className="ml-auto shrink-0 text-xs tabular-nums text-muted-foreground"
              >
                {formatTimelineTime(occurredAt)}
              </time>
            </div>
            <p className="mt-1 text-sm font-medium">{title}</p>
            {detail && (
              <p className="mt-0.5 text-xs text-muted-foreground">{detail}</p>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}

function AttachmentCard({
  attachment,
  canMutate,
  supportSessionID,
  onChanged,
  inverse,
}: {
  attachment: MessageAttachment
  canMutate: boolean
  supportSessionID: string
  onChanged: () => void
  inverse: boolean
}) {
  const [objectURL, setObjectURL] = useState("")
  const [pending, setPending] = useState(false)
  const [error, setError] = useState("")
  const isPDF = attachment.contentType === "application/pdf"

  const loadBlob = useCallback(async () => {
    if (attachment.state !== "Stored") return
    const token = await getAccessToken()
    if (!token) return
    const result = await getMessageAttachment({
      client: portalClient(token),
      path: { attachmentId: attachment.id },
    }).catch(() => undefined)
    if (!result?.data) {
      setError("Attachment unavailable.")
      return
    }
    return result.data
  }, [attachment.id, attachment.state])

  useEffect(() => {
    if (isPDF) return
    const timeout = window.setTimeout(() => {
      void loadBlob().then((blob) => {
        if (blob) setObjectURL(URL.createObjectURL(blob))
      })
    }, 0)
    return () => window.clearTimeout(timeout)
  }, [isPDF, loadBlob])

  useEffect(() => {
    return () => {
      if (objectURL) URL.revokeObjectURL(objectURL)
    }
  }, [objectURL])

  async function retry() {
    setPending(true)
    setError("")
    const token = await getAccessToken()
    if (!token) {
      setPending(false)
      return
    }
    const result = await retryInboundMessageAttachment({
      client: portalClient(token),
      path: { attachmentId: attachment.id },
      body: {
        ...(supportSessionID ? { supportSessionId: supportSessionID } : {}),
      },
    }).catch(() => undefined)
    setPending(false)
    if (!result?.data) {
      setError("The attachment copy could not be retried.")
      return
    }
    onChanged()
  }

  async function download() {
    setPending(true)
    const blob = await loadBlob()
    setPending(false)
    if (!blob) return
    const downloadURL = URL.createObjectURL(blob)
    const link = document.createElement("a")
    link.href = downloadURL
    link.download = attachment.fileName
    link.click()
    URL.revokeObjectURL(downloadURL)
  }

  return (
    <div
      className={cn(
        "mt-2 overflow-hidden border",
        inverse ? "border-primary-foreground/25" : "border-border",
      )}
    >
      {objectURL && !isPDF ? (
        // eslint-disable-next-line @next/next/no-img-element -- private object URL
        <img
          src={objectURL}
          alt={attachment.fileName}
          className="block h-auto max-h-60 max-w-full bg-background object-contain"
        />
      ) : (
        <div
          className={cn(
            "flex items-center gap-2 px-3 py-2 text-xs",
            inverse ? "bg-primary-foreground/10" : "bg-muted/50",
          )}
        >
          <FileTextIcon className="size-4" />
          <span className="min-w-0 flex-1 truncate">{attachment.fileName}</span>
          <span className="tabular-nums">{formatBytes(attachment.byteSize)}</span>
        </div>
      )}
      <div className="flex items-center gap-2 px-3 py-1.5 text-xs">
        <span>{attachment.state}</span>
        {isPDF && attachment.state === "Stored" && (
          <Button
            size="sm"
            variant="ghost"
            className="ml-auto h-7 px-2"
            disabled={pending}
            onClick={() => void download()}
          >
            <DownloadIcon />
            Download
          </Button>
        )}
        {attachment.state === "Attachment unavailable" && canMutate && (
          <Button
            size="sm"
            variant="ghost"
            className="ml-auto h-7 px-2"
            disabled={pending}
            onClick={() => void retry()}
          >
            {pending ? <Spinner /> : <RefreshCwIcon />}
            Retry copy
          </Button>
        )}
      </div>
      {error && <p className="px-3 pb-2 text-xs">{error}</p>}
    </div>
  )
}

function MessageComposer({
  thread,
  threadID,
  practiceID,
  locationID,
  routeLabel,
  taskID,
  initialDestination,
  destinationLocked = false,
  supportSessionID,
  disabled,
  disabledReason,
  onSent,
}: {
  thread?: Pick<MessageThreadSummary, "externalPhone" | "outboundBlocked">
  threadID: string
  practiceID: string
  locationID: string
  routeLabel?: string
  taskID?: string
  initialDestination?: string
  destinationLocked?: boolean
  supportSessionID: string
  disabled: boolean
  disabledReason: string
  onSent: (message: Message) => void
}) {
  const [destination, setDestination] = useState(
    thread?.externalPhone ?? initialDestination ?? "",
  )
  const [body, setBody] = useState("")
  const [file, setFile] = useState<File>()
  const [pending, setPending] = useState(false)
  const [error, setError] = useState("")
  const draftAttempt = useRef<
    | {
        signature: string
        idempotencyKey: string
        attachmentID: string
      }
    | undefined
  >(undefined)

  function chooseFile(event: ChangeEvent<HTMLInputElement>) {
    const selected = event.target.files?.[0]
    event.target.value = ""
    setError("")
    if (!selected) return
    draftAttempt.current = undefined
    setFile(undefined)
    if (
      !acceptedAttachmentTypes.has(selected.type) ||
      selected.size > maximumAttachmentBytes
    ) {
      setError("Use one JPEG, PNG, GIF, WebP, or PDF up to 600 KB.")
      return
    }
    setFile(selected)
  }

  async function submit(event: FormEvent) {
    event.preventDefault()
    if (disabled || (!body.trim() && !file)) return
    setPending(true)
    setError("")
    const signature = JSON.stringify({
      practiceID,
      locationID,
      threadID,
      destination: destination.trim(),
      body: body.trim(),
      taskID,
      file: file
        ? [file.name, file.type, file.size, file.lastModified]
        : undefined,
    })
    if (draftAttempt.current?.signature !== signature) {
      draftAttempt.current = {
        signature,
        idempotencyKey: crypto.randomUUID(),
        attachmentID: "",
      }
    }
    const attempt = draftAttempt.current
    const token = await getAccessToken()
    if (!token) {
      setPending(false)
      return
    }
    let attachmentID = attempt.attachmentID
    if (file && !attachmentID) {
      const upload = await uploadMessageAttachment({
        client: portalClient(token),
        body: {
          practiceId: practiceID,
          locationId: locationID,
          fileName: file.name,
          contentType: file.type as
            | "image/jpeg"
            | "image/png"
            | "image/gif"
            | "image/webp"
            | "application/pdf",
          contentBase64: await fileToBase64(file),
          ...(supportSessionID ? { supportSessionId: supportSessionID } : {}),
        },
      }).catch(() => undefined)
      if (!upload?.data) {
        setPending(false)
        setError("The attachment could not be prepared.")
        return
      }
      attachmentID = upload.data.id
      attempt.attachmentID = attachmentID
    }
    const result = await sendMessage({
      client: portalClient(token),
      body: {
        practiceId: practiceID,
        locationId: locationID,
        ...(threadID ? { threadId: threadID } : {}),
        ...(!threadID ? { destination: destination.trim() } : {}),
        body: body.trim(),
        ...(attachmentID ? { attachmentId: attachmentID } : {}),
        ...(taskID ? { taskId: taskID } : {}),
        idempotencyKey: attempt.idempotencyKey,
        ...(supportSessionID ? { supportSessionId: supportSessionID } : {}),
      },
    }).catch(() => undefined)
    setPending(false)
    if (!result?.data) {
      const status = result?.response?.status
      setError(
        status === 409
          ? "This destination cannot be messaged from the selected office."
          : "The message was not queued. Nothing was sent.",
      )
      return
    }
    setBody("")
    setFile(undefined)
    draftAttempt.current = undefined
    onSent(result.data.message)
  }

  return (
    <form
      aria-label="Message composer"
      className="border-t bg-background px-4 py-2.5"
      onSubmit={(event) => void submit(event)}
    >
      <div className="mx-auto max-w-5xl">
        <p className="mb-2 text-xs text-muted-foreground">
          Sender route: <strong className="font-medium text-foreground">{routeLabel || "Selected office"}</strong>
          {initialDestination
            ? ` · destination locked to ${formatPhone(initialDestination)}`
            : ""}
        </p>
        {!threadID && (
          <Input
            aria-label="Destination phone number"
            inputMode="tel"
            autoComplete="tel"
            placeholder="+1 727 555 0100"
            className="mb-2 tabular-nums"
            value={destination}
            disabled={disabled || pending || destinationLocked}
            onChange={(event) => setDestination(event.target.value)}
          />
        )}
        <div className="flex items-end gap-2">
          <textarea
            aria-label="Message"
            rows={1}
            maxLength={maximumMessageLength}
            placeholder="Write a message"
            className="flex min-h-11 max-h-32 min-w-0 flex-1 resize-y rounded-md border border-input bg-transparent px-3 py-2 text-sm shadow-xs outline-none transition-[color,box-shadow] placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:cursor-not-allowed disabled:opacity-50"
            value={body}
            disabled={disabled || pending}
            onChange={(event) => setBody(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) {
                event.preventDefault()
                event.currentTarget.form?.requestSubmit()
              }
            }}
          />
          <label
            htmlFor={`attachment-${threadID || locationID}`}
            className={cn(
              "inline-flex size-9 shrink-0 items-center justify-center rounded-md border bg-background text-sm shadow-xs",
              "hover:bg-accent hover:text-accent-foreground",
              (disabled || pending) && "pointer-events-none opacity-50",
            )}
          >
            <PaperclipIcon className="size-4" />
            <span className="sr-only">Attach one file</span>
          </label>
          <input
            id={`attachment-${threadID || locationID}`}
            type="file"
            className="sr-only"
            accept={[...acceptedAttachmentTypes].join(",")}
            disabled={disabled || pending}
            onChange={chooseFile}
          />
          <Button
            type="submit"
            size="icon"
            aria-label="Send message"
            disabled={
              disabled ||
              pending ||
              (!body.trim() && !file) ||
              (!threadID && !destination.trim())
            }
          >
            {pending ? <Spinner /> : <SendIcon />}
          </Button>
        </div>
        <div className="mt-1.5 flex min-h-5 items-center gap-2 text-xs text-muted-foreground">
          {file && (
            <span className="flex min-w-0 items-center gap-1 rounded-sm border px-1.5 py-0.5">
              <span className="max-w-52 truncate">{file.name}</span>
              <button
                type="button"
                aria-label="Remove attachment"
                disabled={pending}
                onClick={() => {
                  draftAttempt.current = undefined
                  setFile(undefined)
                }}
              >
                <XIcon className="size-3" />
              </button>
            </span>
          )}
          {body.length >= 1_400 && (
            <span className="ml-auto tabular-nums">
              {body.length}/{maximumMessageLength}
            </span>
          )}
          {!file && body.length < 1_400 && (
            <span>⌘↵ to send · one attachment up to 600 KB</span>
          )}
        </div>
        {(disabledReason || error) && (
          <p
            role={error ? "alert" : "status"}
            className={cn(
              "mt-1 text-xs",
              error ? "text-destructive" : "text-muted-foreground",
            )}
          >
            {error || disabledReason}
          </p>
        )}
      </div>
    </form>
  )
}

function timelineItemText(item: ConversationTimelineItem) {
  return [
    item.message?.body,
    item.message?.attachment?.fileName,
    item.call?.transferReason,
    item.call?.outcome,
    item.task?.title,
    item.task?.state,
  ]
    .filter(Boolean)
    .join(" ")
    .toLowerCase()
}

function fileToBase64(file: File) {
  return new Promise<string>((resolve, reject) => {
    const reader = new FileReader()
    reader.onerror = () => reject(reader.error)
    reader.onload = () => {
      const result = String(reader.result ?? "")
      resolve(result.slice(result.indexOf(",") + 1))
    }
    reader.readAsDataURL(file)
  })
}

function taskActivityLabel(
  activity: ConversationTimelineItem["taskActivity"],
  state: Task["state"],
) {
  switch (activity) {
    case "TASK_CREATED":
      return "Created"
    case "TITLE_CHANGED":
      return "Title changed"
    case "TASK_COMPLETED":
      return "Completed"
    case "TASK_REOPENED":
      return "Reopened"
    case "INTERACTION_ATTACHED":
      return "New interaction attached"
    default:
      return state === "OPEN" ? "Open" : "Completed"
  }
}

function formatPhone(phone: string) {
  const match = phone.match(/^\+1(\d{3})(\d{3})(\d{4})$/)
  if (!match) return phone
  return `(${match[1]}) ${match[2]}-${match[3]}`
}

function formatDateTime(value: string) {
  return new Intl.DateTimeFormat(undefined, {
    month: "short",
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
  }).format(new Date(value))
}

function formatTimelineTime(value: string) {
  return new Intl.DateTimeFormat(undefined, {
    hour: "numeric",
    minute: "2-digit",
  }).format(new Date(value))
}

function formatTimelineDate(value: string) {
  return new Intl.DateTimeFormat(undefined, {
    weekday: "short",
    month: "short",
    day: "numeric",
  }).format(new Date(value))
}

function isDateBoundary(
  previous: ConversationTimelineItem | undefined,
  current: ConversationTimelineItem,
) {
  if (!previous) return true
  return new Date(previous.occurredAt).toDateString() !==
    new Date(current.occurredAt).toDateString()
}

function formatDuration(seconds: number) {
  if (seconds < 60) return `${seconds}s`
  return `${Math.floor(seconds / 60)}m ${seconds % 60}s`
}

function callStateLabel(state: CallingCall["state"]) {
  return state.toLowerCase().replaceAll("_", " ")
}

function formatBytes(bytes: number) {
  if (bytes < 1_024) return `${bytes} B`
  return `${Math.round(bytes / 1_024)} KB`
}
