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
  CalendarCheck2Icon,
  CalendarClockIcon,
  CalendarX2Icon,
  CheckIcon,
  CheckSquareIcon,
  CopyIcon,
  DownloadIcon,
  FileTextIcon,
  PaperclipIcon,
  PhoneCallIcon,
  PhoneIncomingIcon,
  PhoneMissedIcon,
  PhoneOutgoingIcon,
  RefreshCwIcon,
  SendIcon,
  VoicemailIcon,
  XIcon,
} from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "@/components/ui/empty"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupTextarea,
} from "@/components/ui/input-group"
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select"
import { Spinner } from "@/components/ui/spinner"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
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
  const [copyState, setCopyState] = useState<"idle" | "copied" | "failed">(
    "idle",
  )
  const {
    activeCall,
    outboundPending,
    ownsSoftphone,
    platformOperator,
    startOutbound,
  } = useCallingNavigation()
  const routeName =
    engagement.locations.find((location) => location.id === route)?.name ??
    "Choose office"
  return (
    <section className="flex min-h-0 flex-1 flex-col">
      <header className="flex min-h-14 flex-wrap items-center gap-3 border-b px-4 py-2">
        <div className="flex min-w-0 flex-1 items-center gap-1">
          <h1 className="truncate text-lg font-semibold tracking-[-0.015em] tabular-nums">
            {formatPhone(engagement.phone)}
          </h1>
          <Tooltip>
            <TooltipTrigger
              render={
                <Button
                  type="button"
                  size="icon-sm"
                  variant="ghost"
                  aria-label={
                    copyState === "copied" ? "Number copied" : "Copy phone number"
                  }
                  onClick={() => {
                    void navigator.clipboard.writeText(engagement.phone).then(
                      () => setCopyState("copied"),
                      () => setCopyState("failed"),
                    )
                  }}
                />
              }
            >
              {copyState === "copied" ? <CheckIcon /> : <CopyIcon />}
            </TooltipTrigger>
            <TooltipContent>
              {copyState === "copied"
                ? "Copied"
                : copyState === "failed"
                  ? "Copy failed"
                  : "Copy number"}
            </TooltipContent>
          </Tooltip>
          <span className="ml-2 min-w-0 truncate text-sm text-muted-foreground">
            {routeName}
          </span>
          <span className="sr-only" role="status">
            {copyState === "copied"
              ? "Phone number copied"
              : copyState === "failed"
                ? "Phone number could not be copied"
                : ""}
          </span>
        </div>
        <div className="flex items-center gap-2">
          {engagement.locations.length > 1 && (
            <NativeSelect
              aria-label="Sender office"
              size="sm"
              value={route}
              onChange={(event) => setRoute(event.target.value)}
            >
              <NativeSelectOption value="" disabled>
                Choose office
              </NativeSelectOption>
              {engagement.locations.map((location) => (
                <NativeSelectOption key={location.id} value={location.id}>
                  {location.name}
                </NativeSelectOption>
              ))}
            </NativeSelect>
          )}
          <Button
            size="sm"
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
            {outboundPending ? <Spinner /> : <PhoneCallIcon data-icon="inline-start" />}
            Call
          </Button>
        </div>
        {callError && (
          <p className="w-full text-xs text-destructive">{callError}</p>
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
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div
        ref={scroller}
        data-testid="message-timeline"
        className="relative min-h-0 flex-1 overflow-y-auto bg-muted/10 px-4 py-5"
        onScroll={(event) => {
          const element = event.currentTarget
          atLatest.current =
            element.scrollHeight - element.scrollTop - element.clientHeight < 72
        }}
      >
        <div className="mx-auto flex max-w-3xl flex-col gap-2">
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
            items.map((item) => (
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
            <Empty className="my-10 border-0">
              <EmptyHeader>
                <EmptyTitle>No messages yet</EmptyTitle>
                <EmptyDescription>
                  Send a text or call this number to start.
                </EmptyDescription>
              </EmptyHeader>
            </Empty>
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
    const touchpoint = callTouchpoint(item.call)
    return (
      <ActivityBubble
        icon={touchpoint.icon}
        label={`${item.call.locationName} · ${touchpoint.label}`}
        occurredAt={item.occurredAt}
        title={item.call.transferReason || touchpoint.label}
        detail={touchpoint.detail}
        onOpen={onCallOpen ? () => onCallOpen(item.call!.id) : undefined}
      />
    )
  }
  if (item.type === "TASK" && item.task) {
    const task = item.task
    const touchpoint = taskTouchpoint(task)
    return (
      <ActivityBubble
        icon={touchpoint.icon}
        label={`${task.locationName} · ${touchpoint.label} · ${taskActivityLabel(item.taskActivity, task.state)}`}
        occurredAt={item.occurredAt}
        title={task.title}
        detail={`Created by ${task.createdBy.email || task.createdBy.subject}`}
        onOpen={onTaskOpen ? () => onTaskOpen(task) : undefined}
      />
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
        outbound ? "justify-end pl-8" : "justify-start pr-8",
      )}
    >
      <div
        className={cn(
          "max-w-[82%] rounded-2xl px-3 py-2 sm:max-w-[32rem]",
          outbound
            ? "rounded-br-md bg-primary text-primary-foreground"
            : "rounded-bl-md bg-muted",
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
              "mt-1.5 flex flex-wrap items-center gap-1",
              outbound && "text-primary-foreground",
            )}
          >
            {!message.taskId && (
              <Button
                size="sm"
                variant={outbound ? "secondary" : "ghost"}
                className="h-6 px-2 text-xs"
                disabled={pending}
                onClick={() => void createTask()}
              >
                <CheckSquareIcon data-icon="inline-start" />
                Create Task
              </Button>
            )}
            {outbound &&
              (message.delivery === "Failed" ||
                message.delivery === "Status unknown") && (
                <Button
                  size="sm"
                  variant={outbound ? "secondary" : "ghost"}
                  className="h-6 px-2 text-xs"
                  disabled={pending}
                  onClick={() => void sendAgain()}
                >
                  {message.delivery === "Status unknown" && (
                    <AlertTriangleIcon data-icon="inline-start" />
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

function ActivityBubble({
  icon,
  label,
  occurredAt,
  title,
  detail,
  onOpen,
}: {
  icon: React.ReactNode
  label: string
  occurredAt: string
  title: string
  detail: string
  onOpen?: () => void
}) {
  const content = (
    <>
      <span className="mt-0.5 text-muted-foreground">{icon}</span>
      <span className="min-w-0 flex-1">
        <span className="flex min-w-0 items-center gap-2 text-xs text-muted-foreground">
          <span className="truncate">{label}</span>
          <time className="ml-auto shrink-0 tabular-nums" dateTime={occurredAt}>
            {formatDateTime(occurredAt)}
          </time>
        </span>
        <span className="mt-1 block truncate text-sm font-medium text-foreground">
          {title}
        </span>
        <span className="mt-0.5 block text-xs text-muted-foreground">
          {detail}
        </span>
      </span>
    </>
  )
  if (onOpen) {
    return (
      <Button
        type="button"
        variant="outline"
        className="mx-auto h-auto w-full max-w-md items-start justify-start gap-2 rounded-xl px-3 py-2 text-left whitespace-normal"
        onClick={onOpen}
      >
        {content}
      </Button>
    )
  }
  return (
    <div className="mx-auto flex w-full max-w-md items-start gap-2 rounded-xl border bg-background px-3 py-2">
      {content}
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
        "mt-2 overflow-hidden rounded-lg border",
        inverse ? "border-primary-foreground/25" : "border-border",
      )}
    >
      {objectURL && !isPDF ? (
        // eslint-disable-next-line @next/next/no-img-element -- private object URL
        <img
          src={objectURL}
          alt={attachment.fileName}
          className="max-h-44 w-full bg-background object-contain"
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
  const fileInput = useRef<HTMLInputElement>(null)
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
      className="border-t bg-background p-3"
      onSubmit={(event) => void submit(event)}
    >
      <div className="mx-auto max-w-3xl">
        <InputGroup className="h-auto min-h-12 rounded-xl bg-background shadow-xs">
          <InputGroupTextarea
            aria-label="Message"
            rows={1}
            maxLength={maximumMessageLength}
            placeholder="Message"
            className="max-h-32 min-h-12 py-3"
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
          <InputGroupAddon align="inline-end" className="self-end">
            <InputGroupButton
              type="button"
              size="icon-sm"
              aria-label="Attach one file"
              disabled={disabled || pending}
              onClick={() => fileInput.current?.click()}
            >
              <PaperclipIcon />
            </InputGroupButton>
            <InputGroupButton
              type="submit"
              variant="default"
              size="icon-sm"
              aria-label="Send message"
              disabled={
                disabled ||
                pending ||
                (!body.trim() && !file) ||
                (!threadID && !destination.trim())
              }
            >
              {pending ? <Spinner /> : <SendIcon />}
            </InputGroupButton>
          </InputGroupAddon>
        </InputGroup>
        <input
          ref={fileInput}
          type="file"
          className="sr-only"
          accept={[...acceptedAttachmentTypes].join(",")}
          disabled={disabled || pending}
          onChange={chooseFile}
        />
        {(file || body.length >= 1_400) && (
          <div className="mt-1.5 flex min-h-5 items-center gap-2 text-xs text-muted-foreground">
          {file && (
            <span className="flex min-w-0 items-center gap-1 rounded-sm border px-1.5 py-0.5">
              <span className="max-w-52 truncate">{file.name}</span>
              <Button
                type="button"
                variant="ghost"
                size="icon-xs"
                aria-label="Remove attachment"
                disabled={pending}
                onClick={() => {
                  draftAttempt.current = undefined
                  setFile(undefined)
                }}
              >
                <XIcon />
              </Button>
            </span>
          )}
          {body.length >= 1_400 && (
            <span className="ml-auto tabular-nums">
              {body.length}/{maximumMessageLength}
            </span>
          )}
          </div>
        )}
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

function callTouchpoint(call: NonNullable<ConversationTimelineItem["call"]>) {
  const duration = formatDuration(call.durationSeconds)
  if (call.outcome === "VOICEMAIL") {
    return {
      icon: <VoicemailIcon />,
      label: "Voicemail",
      detail: `Recorded · ${duration}`,
    }
  }
  if (call.outcome === "MISSED" || call.outcome === "UNANSWERED") {
    return {
      icon: <PhoneMissedIcon />,
      label: call.direction === "INBOUND" ? "Missed call" : "Unanswered call",
      detail: `${sentenceCase(call.direction)} · ${duration}`,
    }
  }
  if (call.direction === "INBOUND") {
    return {
      icon: <PhoneIncomingIcon />,
      label: "Inbound call",
      detail: `${sentenceCase(call.outcome)} · ${duration}`,
    }
  }
  return {
    icon: <PhoneOutgoingIcon />,
    label: "Outbound call",
    detail: `${sentenceCase(call.outcome)} · ${duration}`,
  }
}

function taskTouchpoint(task: Task) {
  if (task.category === "appointments") {
    const text = `${task.title} ${task.sourceMessage ?? ""}`.toLowerCase()
    if (/\b(cancel|cancellation)\b/.test(text)) {
      return { icon: <CalendarX2Icon />, label: "Cancellation" }
    }
    if (/\b(reschedule|rescheduling|move appointment|change appointment)\b/.test(text)) {
      return { icon: <CalendarClockIcon />, label: "Reschedule" }
    }
    if (/\b(book|booking|schedule|new appointment|appointment request)\b/.test(text)) {
      return { icon: <CalendarCheck2Icon />, label: "Booking" }
    }
    return { icon: <CalendarClockIcon />, label: "Appointment" }
  }
  if (task.origin === "VOICEMAIL_RECOVERY") {
    return { icon: <VoicemailIcon />, label: "Voicemail follow-up" }
  }
  if (task.origin === "MISSED_CALL_RECOVERY") {
    return { icon: <PhoneMissedIcon />, label: "Missed call follow-up" }
  }
  return { icon: <CheckSquareIcon />, label: "Task" }
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

function sentenceCase(value: string) {
  const normalized = value.replaceAll("_", " ").toLowerCase()
  return normalized.charAt(0).toUpperCase() + normalized.slice(1)
}

function formatBytes(bytes: number) {
  if (bytes < 1_024) return `${bytes} B`
  return `${Math.round(bytes / 1_024)} KB`
}
