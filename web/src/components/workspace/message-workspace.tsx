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
  CheckSquareIcon,
  DownloadIcon,
  FileTextIcon,
  PaperclipIcon,
  PhoneCallIcon,
  RefreshCwIcon,
  SearchIcon,
  SendIcon,
  XIcon,
} from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select"
import { Spinner } from "@/components/ui/spinner"
import { useCallingNavigation } from "@/components/workspace/calling-dock"
import { portalClient } from "@/lib/api/client"
import {
  createMessageFollowUpTask,
  getEngagementTimeline,
  getMessageAttachment,
  getMessageThreadTimeline,
  getTaskEngagementHistory,
  markMessageThreadRead,
  retryInboundMessageAttachment,
  sendMessage,
  sendMessageAgain,
  uploadMessageAttachment,
} from "@/lib/api/generated/sdk.gen"
import type {
  ConversationTimelineItem,
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

type TimelineSource =
  | { kind: "task"; taskID: string }
  | { kind: "engagement"; practiceID: string; phone: string }

export function EngagementWorkspace({
  engagement,
  practiceID,
  supportSessionID,
  canMutate,
  revision,
  onTaskCreated,
  onTaskOpen,
  onCallOpen,
}: {
  engagement: EngagementSummary
  practiceID: string
  supportSessionID: string
  canMutate: boolean
  revision: number
  onTaskCreated: (task: Task) => void
  onTaskOpen: (task: Task) => void
  onCallOpen: (callID: string) => void
}) {
  const defaultRoute =
    engagement.locations.length === 1 ? engagement.locations[0]!.id : ""
  const [route, setRoute] = useState(defaultRoute)
  const [callError, setCallError] = useState("")
  const {
    activeCall,
    outboundPending,
    ownsSoftphone,
    platformOperator,
    startOutbound,
  } = useCallingNavigation()
  const routeName =
    engagement.locations.find((location) => location.id === route)?.name ??
    "Choose sender route"
  return (
    <section className="flex min-h-0 flex-1 flex-col">
      <header className="border-b px-5 py-4">
        <div className="flex flex-wrap items-start gap-4">
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2">
              <Badge variant="outline">Engagement History</Badge>
              <span className="text-xs font-medium text-muted-foreground">
                Unverified phone context
              </span>
            </div>
            <h1 className="mt-2 truncate text-xl font-semibold tracking-[-0.015em] tabular-nums">
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
            <Button
              variant="outline"
              disabled={
                !canMutate ||
                platformOperator ||
                !route ||
                !ownsSoftphone ||
                Boolean(activeCall) ||
                outboundPending
              }
              onClick={() => {
                setCallError("")
                void startOutbound(route, engagement.phone).then(
                  (requestError) => setCallError(requestError ?? ""),
                )
              }}
            >
              {outboundPending ? <Spinner /> : <PhoneCallIcon />}
              Call
            </Button>
          </div>
        </div>
        {callError && (
          <p className="mt-2 text-xs text-destructive">{callError}</p>
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
        routeLabel={routeName}
        initialDestination={engagement.phone}
        supportSessionID={supportSessionID}
        canMutate={canMutate}
        revision={revision}
        onMessageSent={() => undefined}
        onThreadRead={() => undefined}
        onTaskCreated={onTaskCreated}
        onTaskOpen={onTaskOpen}
        onCallOpen={onCallOpen}
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
  onTaskCreated,
  onTaskOpen,
  onCallOpen,
}: {
  thread?: MessageThreadSummary
  threadID?: string
  timelineSource?: TimelineSource
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
  onTaskCreated: (task: Task) => void
  onTaskOpen?: (task: Task) => void
  onCallOpen?: (callID: string) => void
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
  const [newActivity, setNewActivity] = useState(false)
  const [findQuery, setFindQuery] = useState("")
  const [error, setError] = useState("")
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
  const initialized = useRef(false)
  const onThreadReadRef = useRef(onThreadRead)

  useEffect(() => {
    onThreadReadRef.current = onThreadRead
  }, [onThreadRead])

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
      setNewActivity(false)
      if (scroll) {
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
    const timeout = window.setTimeout(
      () => void loadLatest(true),
      Math.max(0, committed.visibleUntil - Date.now()),
    )
    return () => window.clearTimeout(timeout)
  }, [loadLatest, threadID])

  const markRead = useCallback(async () => {
    if (!threadID) return
    const token = await getAccessToken()
    if (!token) return
    const result = await markMessageThreadRead({
      client: portalClient(token),
      path: { threadId: threadID },
      body: {
        ...(supportSessionID ? { supportSessionId: supportSessionID } : {}),
      },
    }).catch(() => undefined)
    if (result?.response?.ok) onThreadReadRef.current(threadID)
  }, [supportSessionID, threadID])

  useEffect(() => {
    if (!timelineKey) return
    const timeout = window.setTimeout(() => {
      void loadLatest(true)
      if (threadID) void markRead()
    }, 0)
    return () => window.clearTimeout(timeout)
  }, [loadLatest, markRead, threadID, timelineKey])

  useEffect(() => {
    if (!initialized.current || !timelineKey) return
    if (atLatest.current) {
      void loadLatest(true)
      if (threadID) void markRead()
    } else {
      setNewActivity(true)
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

  const conversationThread =
    (thread?.locationId === locationID ? thread : undefined) ??
    items.find(
      (item) => item.message?.thread.locationId === locationID,
    )?.message?.thread
  const composerThreadID =
    conversationThread?.id ??
    (timelineSource?.kind === "engagement" ? "" : threadID)
  const visibleItems = findQuery.trim()
    ? items.filter((item) => timelineItemText(item).includes(findQuery.trim().toLowerCase()))
    : items

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="border-b bg-muted/20 px-4 py-2">
        <div className="relative mx-auto max-w-3xl">
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
        className="relative min-h-0 flex-1 overflow-y-auto bg-[linear-gradient(to_right,transparent_calc(50%-0.5px),color-mix(in_oklab,var(--border)_55%,transparent)_50%,transparent_calc(50%+0.5px))] px-4 py-5"
        onScroll={(event) => {
          const element = event.currentTarget
          atLatest.current =
            element.scrollHeight - element.scrollTop - element.clientHeight < 72
        }}
      >
        <div className="mx-auto flex max-w-3xl flex-col gap-3">
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
            visibleItems.map((item) => (
              <TimelineEntry
                key={`${item.type}:${item.id}`}
                item={item}
                canMutate={canMutate}
                supportSessionID={supportSessionID}
                onChanged={() => void loadLatest(true)}
                onTaskCreated={onTaskCreated}
                onTaskOpen={onTaskOpen}
                onCallOpen={onCallOpen}
              />
            ))}
          {!loading && items.length === 0 && (
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
        {newActivity && (
          <div className="sticky bottom-3 flex justify-center">
            <Button
              size="sm"
              className="shadow-lg"
              onClick={() => {
                atLatest.current = true
                void loadLatest(true)
                void markRead()
              }}
            >
              New activity
            </Button>
          </div>
        )}
      </div>
      {error && (
        <Alert variant="destructive" className="rounded-none border-x-0">
          <AlertTitle>Conversation unavailable</AlertTitle>
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      <MessageComposer
        threadID={composerThreadID}
        practiceID={practiceID}
        locationID={locationID}
        routeLabel={routeLabel}
        taskID={taskID}
        destination={initialDestination ?? ""}
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
}: {
  item: ConversationTimelineItem
  canMutate: boolean
  supportSessionID: string
  onChanged: () => void
  onTaskCreated: (task: Task) => void
  onTaskOpen?: (task: Task) => void
  onCallOpen?: (callID: string) => void
}) {
  if (item.type === "MESSAGE" && item.message) {
    return (
      <MessageEntry
        message={item.message}
        canMutate={canMutate}
        supportSessionID={supportSessionID}
        onChanged={onChanged}
        onTaskCreated={onTaskCreated}
      />
    )
  }
  if (item.type === "CALL" && item.call) {
    return (
      <button
        type="button"
        className={cn(
          "w-full text-left",
          onCallOpen &&
            "cursor-pointer focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring",
        )}
        disabled={!onCallOpen}
        onClick={() => onCallOpen?.(item.call!.id)}
      >
        <TimelineRule
          icon={<PhoneCallIcon />}
          label={`${item.call.locationName} · Call · Open detail`}
          occurredAt={item.occurredAt}
          title={item.call.transferReason || "Inbound call"}
          detail={`${item.call.outcome.replaceAll("_", " ")} · ${formatDuration(item.call.durationSeconds)}`}
        />
      </button>
    )
  }
  if (item.type === "TASK" && item.task) {
    const task = item.task
    return (
      <button
        type="button"
        className={cn(
          "w-full text-left",
          onTaskOpen &&
            "cursor-pointer focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring",
        )}
        disabled={!onTaskOpen}
        onClick={() => onTaskOpen?.(task)}
      >
        <TimelineRule
          icon={<CheckSquareIcon />}
          label={`${task.locationName} · Task · ${taskActivityLabel(item.taskActivity, task.state)}`}
          occurredAt={item.occurredAt}
          title={task.title}
          detail={`Created by ${task.createdBy.email || task.createdBy.subject}`}
        />
      </button>
    )
  }
  return null
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
}: {
  message: Message
  canMutate: boolean
  supportSessionID: string
  onChanged: () => void
  onTaskCreated: (task: Task) => void
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
        "flex w-full",
        outbound ? "justify-end pl-10" : "justify-start pr-10",
      )}
    >
      <div
        className={cn(
          "max-w-[34rem] border px-3 py-2.5 shadow-xs",
          outbound
            ? "rounded-l-md rounded-br-md bg-primary text-primary-foreground"
            : "rounded-r-md rounded-bl-md bg-background",
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
            inverse={outbound}
          />
        )}
        <div
          className={cn(
            "mt-2 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs tabular-nums",
            outbound ? "text-primary-foreground/75" : "text-muted-foreground",
          )}
        >
          <time dateTime={message.createdAt}>
            {formatDateTime(message.createdAt)}
          </time>
          <span aria-hidden="true">·</span>
          <span>{message.thread.locationName}</span>
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
        {canMutate && (
          <div
            className={cn(
              "mt-2 flex flex-wrap gap-1 border-t pt-2",
              outbound ? "border-primary-foreground/20" : "border-border",
            )}
          >
            {!message.taskId && (
              <Button
                size="sm"
                variant={outbound ? "secondary" : "ghost"}
                className="h-7"
                disabled={pending}
                onClick={() => void createTask()}
              >
                <CheckSquareIcon />
                Create Task
              </Button>
            )}
            {outbound &&
              (message.delivery === "Failed" ||
                message.delivery === "Status unknown") && (
                <Button
                  size="sm"
                  variant={outbound ? "secondary" : "ghost"}
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
              outbound ? "text-primary-foreground" : "text-destructive",
            )}
          >
            {error}
          </p>
        )}
      </div>
    </article>
  )
}

function TimelineRule({
  icon,
  label,
  occurredAt,
  title,
  detail,
}: {
  icon: React.ReactNode
  label: string
  occurredAt: string
  title: string
  detail: string
}) {
  return (
    <div className="mx-auto w-full max-w-xl border bg-muted/80 px-3 py-2 text-xs shadow-xs backdrop-blur-sm">
      <div className="flex items-center gap-2">
        <span className="[&_svg]:size-3.5 text-muted-foreground">{icon}</span>
        <span className="text-xs font-medium text-muted-foreground">
          {label}
        </span>
        <time
          dateTime={occurredAt}
          className="ml-auto text-xs tabular-nums text-muted-foreground"
        >
          {formatDateTime(occurredAt)}
        </time>
      </div>
      <p className="mt-1.5 font-medium">{title}</p>
      <p className="mt-0.5 text-muted-foreground">{detail}</p>
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
          className="max-h-72 w-full bg-background object-contain"
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
  threadID,
  practiceID,
  locationID,
  routeLabel,
  taskID,
  destination,
  supportSessionID,
  disabled,
  disabledReason,
  onSent,
}: {
  threadID: string
  practiceID: string
  locationID: string
  routeLabel?: string
  taskID?: string
  destination: string
  supportSessionID: string
  disabled: boolean
  disabledReason: string
  onSent: (message: Message) => void
}) {
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
      className="border-t bg-background px-4 py-3"
      onSubmit={(event) => void submit(event)}
    >
      <div className="mx-auto max-w-3xl">
        <p className="mb-2 text-xs text-muted-foreground">
          Sender route: <strong className="font-medium text-foreground">{routeLabel || "Selected office"}</strong>
          {destination
            ? ` · destination locked to ${formatPhone(destination)}`
            : ""}
        </p>
        <div className="flex items-end gap-2">
          <textarea
            aria-label="Message"
            rows={2}
            maxLength={maximumMessageLength}
            placeholder="Write a message"
            className="flex min-h-16 min-w-0 flex-1 resize-y rounded-md border border-input bg-transparent px-3 py-2 text-sm shadow-xs outline-none transition-[color,box-shadow] placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:cursor-not-allowed disabled:opacity-50"
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

function formatDuration(seconds: number) {
  if (seconds < 60) return `${seconds}s`
  return `${Math.floor(seconds / 60)}m ${seconds % 60}s`
}

function formatBytes(bytes: number) {
  if (bytes < 1_024) return `${bytes} B`
  return `${Math.round(bytes / 1_024)} KB`
}
