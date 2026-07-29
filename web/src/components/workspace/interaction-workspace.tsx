"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import {
  ArrowLeftIcon,
  BotIcon,
  CheckIcon,
  CheckCircle2Icon,
  Clock3Icon,
  HistoryIcon,
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
import { portalClient } from "@/lib/api/client"
import {
  completeTask,
  getCallingCallHistory,
  getTaskCallHistory,
  readTask,
  renameTask,
  reopenTask,
} from "@/lib/api/generated/sdk.gen"
import type {
  CallHistoryItem,
  CallingCall,
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
  onTaskUpdated: (task: Task) => void
  onReturnToCall: () => void
}

export function InteractionWorkspace({
  task,
  activeCall,
  view,
  supportSessionID,
  canMutate,
  historyHint,
  onTaskUpdated,
  onReturnToCall,
}: InteractionWorkspaceProps) {
  if (view === "call" && activeCall) {
    return (
      <CallWorkspace
        call={activeCall}
        historyHint={historyHint}
        returnTask={task}
        onReturnToTask={task ? () => onTaskUpdated(task) : undefined}
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
        onTaskUpdated={onTaskUpdated}
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
  onTaskUpdated,
  onReturnToCall,
}: {
  task: Task
  activeCall: CallingCall | undefined
  supportSessionID: string
  canMutate: boolean
  historyHint: number
  onTaskUpdated: (task: Task) => void
  onReturnToCall: () => void
}) {
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(task.title)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState("")

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
    <section className="flex min-h-0 flex-1 flex-col">
      <header className="border-b px-5 py-4">
        <div className="flex flex-wrap items-start gap-3">
          <div className="min-w-0 flex-1">
            <div className="mb-2 flex items-center gap-2">
              <Badge variant={task.state === "OPEN" ? "secondary" : "outline"}>
                {task.state === "OPEN" ? "Open" : "Completed"}
              </Badge>
              <span className="font-mono text-[0.625rem] uppercase tracking-[0.14em] text-muted-foreground">
                Task · v{task.version}
              </span>
            </div>
            {editing && task.state === "OPEN" ? (
              <div className="flex max-w-2xl items-center gap-2">
                <Input
                  aria-label="Task title"
                  autoFocus
                  maxLength={500}
                  value={draft}
                  disabled={pending}
                  onChange={(event) => setDraft(event.target.value)}
                  onKeyDown={(event) => {
                    if (event.key === "Enter") {
                      event.preventDefault()
                      void saveTitle()
                    }
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
                  disabled={pending}
                >
                  {pending ? <Spinner /> : <CheckIcon />}
                </Button>
                <Button
                  size="icon"
                  variant="ghost"
                  aria-label="Cancel rename"
                  onClick={() => {
                    setDraft(task.title)
                    setEditing(false)
                    setError("")
                  }}
                >
                  <XIcon />
                </Button>
              </div>
            ) : (
              <div className="flex min-w-0 items-center gap-2">
                <h1 className="truncate text-xl font-semibold tracking-tight">
                  {task.title}
                </h1>
                {task.state === "OPEN" && canMutate && (
                  <Button
                    variant="ghost"
                    size="icon"
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
            <p className="mt-2 font-mono text-sm text-muted-foreground">
              {formatPhone(task.phone)} · {task.locationName}
            </p>
          </div>
          <div className="flex items-center gap-2">
            {activeCall && (
              <Button variant="outline" onClick={onReturnToCall}>
                <PhoneCallIcon />
                Return to active call
              </Button>
            )}
            {!canMutate ? (
              <Badge variant="outline">Read only · enter Support Mode to change</Badge>
            ) : task.state === "OPEN" ? (
              <Button
                onClick={() => void transition("complete")}
                disabled={pending}
              >
                {pending ? <Spinner /> : <CheckCircle2Icon />}
                Complete
              </Button>
            ) : (
              <Button
                variant="outline"
                onClick={() => void transition("reopen")}
                disabled={pending}
              >
                {pending ? <Spinner /> : <RotateCcwIcon />}
                Reopen
              </Button>
            )}
          </div>
        </div>
        {error && !(editing && task.state === "COMPLETED") && (
          <Alert variant="destructive" className="mt-3">
            <AlertTitle>Task changed</AlertTitle>
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        {editing && task.state === "COMPLETED" && (
          <Alert className="mt-3">
            <AlertTitle>Title retained</AlertTitle>
            <AlertDescription>
              <p>
                This Task was completed while you were editing. Reopen it to
                retry “{draft}”.
              </p>
              <Button
                size="sm"
                variant="ghost"
                className="mt-2"
                onClick={() => {
                  setDraft(task.title)
                  setEditing(false)
                  setError("")
                }}
              >
                Discard attempted title
              </Button>
            </AlertDescription>
          </Alert>
        )}
        <div className="mt-4 grid gap-3 border-t pt-3 text-xs text-muted-foreground sm:grid-cols-3">
          <Metadata
            label="Created"
            value={`${formatDateTime(task.createdAt)} · ${actorLabel(task.createdBy)}`}
          />
          <Metadata
            label="Last changed"
            value={formatDateTime(task.updatedAt)}
          />
          <Metadata
            label="Completed"
            value={
              task.completedAt
                ? `${formatDateTime(task.completedAt)} · ${task.completedBy ? actorLabel(task.completedBy) : ""}`
                : "Not completed"
            }
          />
        </div>
      </header>
      {task.origin === "ABITA_AI" && <AITaskSource task={task} />}
      {task.messageThreadId || task.conversationThreadId ? (
        <TaskMessageConversation
          task={task}
          supportSessionID={supportSessionID}
          canMutate={canMutate}
          revision={historyHint}
          onTaskCreated={onTaskUpdated}
        />
      ) : (
        <CallHistory
          key={`task:${task.id}`}
          source={{ kind: "task", id: task.id }}
          revision={historyHint}
        />
      )}
    </section>
  )
}

function AITaskSource({ task }: { task: Task }) {
  return (
    <section
      aria-label="AI Task source"
      className="border-b bg-muted/20 px-5 py-4"
    >
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
        <span className="ml-auto text-xs text-muted-foreground">
          Created by AI · {formatDateTime(task.createdAt)}
        </span>
      </div>
      <div className="mt-3 grid gap-3 text-sm md:grid-cols-[minmax(0,1fr)_auto]">
        <div>
          <p className="font-medium">
            {task.callerName
              ? `AI-supplied name: ${task.callerName}`
              : "Caller"}{" "}
            · {formatPhone(task.phone)}
          </p>
          <p className="mt-1 max-w-3xl whitespace-pre-wrap text-muted-foreground">
            {task.sourceMessage}
          </p>
        </div>
        {task.sourceCallId && (
          <div className="text-xs text-muted-foreground md:text-right">
            <p className="uppercase tracking-[0.12em]">Source call</p>
            <p className="mt-1 font-mono">{task.sourceCallId}</p>
          </div>
        )}
      </div>
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

function CallWorkspace({
  call,
  historyHint,
  returnTask,
  onReturnToTask,
}: {
  call: CallingCall
  historyHint: number
  returnTask: Task | undefined
  onReturnToTask: (() => void) | undefined
}) {
  return (
    <section className="flex min-h-0 flex-1 flex-col">
      <header className="border-b px-5 py-4">
        <div className="flex flex-wrap items-start gap-3">
          <div className="min-w-0 flex-1">
            <div className="mb-2 flex items-center gap-2">
              <Badge variant="secondary">{callWorkspaceLabel(call.state)}</Badge>
              <span className="font-mono text-[0.625rem] uppercase tracking-[0.14em] text-muted-foreground">
                {callStateLabel(call.state)}
              </span>
            </div>
            <h1 className="truncate text-xl font-semibold tracking-tight">
              {call.displayName || "Caller"}
            </h1>
            <p className="mt-2 font-mono text-sm text-muted-foreground">
              {formatPhone(call.phone)} · {call.locationName}
            </p>
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
      <CallHistory
        key={`call:${call.id}`}
        source={{ kind: "call", id: call.id }}
        revision={historyHint + call.version}
      />
    </section>
  )
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
                    className="ml-auto font-mono text-[0.6875rem] text-muted-foreground"
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
      <span className="block text-[0.625rem] uppercase tracking-[0.14em]">
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
