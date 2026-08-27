"use client"

import {
  Fragment,
  type ChangeEvent,
  type FormEvent,
  type ReactNode,
  useCallback,
  useEffect,
  useRef,
  useState,
} from "react"
import {
  AlertTriangleIcon,
  ArrowUpIcon,
  CheckIcon,
  CheckSquareIcon,
  CopyIcon,
  DownloadIcon,
  EllipsisIcon,
  FileTextIcon,
  ImageIcon,
  PaperclipIcon,
  PhoneCallIcon,
  RefreshCwIcon,
  XIcon,
} from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import {
  Attachment,
  AttachmentAction,
  AttachmentActions,
  AttachmentContent,
  AttachmentDescription,
  AttachmentMedia,
  AttachmentTitle,
} from "@/components/ui/attachment"
import { Bubble, BubbleContent } from "@/components/ui/bubble"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
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
import { Item } from "@/components/ui/item"
import { Marker, MarkerContent } from "@/components/ui/marker"
import {
  Message as MessageRow,
  MessageContent,
  MessageFooter,
} from "@/components/ui/message"
import {
  MessageScroller,
  MessageScrollerButton,
  MessageScrollerContent,
  MessageScrollerItem,
  MessageScrollerProvider,
  MessageScrollerViewport,
} from "@/components/ui/message-scroller"
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select"
import { Skeleton } from "@/components/ui/skeleton"
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
  Task,
} from "@/lib/api/generated/types.gen"
import { aiCallTimelinePresentation } from "@/lib/ai-interactions"
import { getAccessToken } from "@/lib/auth-client"
import { formatUSPhone } from "@/lib/phone"
import { cn } from "@/lib/utils"
import {
  conversationDateLabel,
  presentTimeline,
  recoveryFollowUpCallIDs,
  sameConversationDate,
} from "@/lib/workspace-history"

const maximumMessageLength = 1_600
const maximumAttachmentBytes = 600 * 1_024
const acceptedAttachmentTypes = new Set([
  "image/jpeg",
  "image/png",
  "image/gif",
  "image/webp",
  "application/pdf",
])

type TimelineSource = {
  kind: "engagement"
  practiceID: string
  phone: string
}

export function EngagementWorkspace({
  engagement,
  practiceID,
  canMutate,
  revision,
  selectedTaskID,
  selectedCallID,
  selectedAIInteractionID,
  headerLeading,
  headerTrailing,
  onTaskCreated,
  onTaskOpen,
  onCallOpen,
  onAIInteractionOpen,
}: {
  engagement: EngagementSummary
  practiceID: string
  canMutate: boolean
  revision: number
  selectedTaskID?: string
  selectedCallID?: string
  selectedAIInteractionID?: string
  headerLeading?: ReactNode
  headerTrailing?: ReactNode
  onTaskCreated: (task: Task) => void
  onTaskOpen: (task: Task) => void
  onCallOpen: (callID: string) => void
  onAIInteractionOpen: (interactionID: string) => void
}) {
  const defaultRoute =
    engagement.locations.length === 1 ? engagement.locations[0]!.id : ""
  const [route, setRoute] = useState(defaultRoute)
  const [callError, setCallError] = useState("")
  const [copyState, setCopyState] = useState<"idle" | "copied" | "failed">(
    "idle",
  )
  const {
    callingOccupied,
    callingEnabled,
    outboundPending,
    ownsSoftphone,
    startOutbound,
  } = useCallingNavigation()
  return (
    <section className="flex min-h-0 flex-1 flex-col">
      <header className="relative flex h-12 shrink-0 items-center gap-2 border-b px-3">
        {headerLeading}
        <div className="flex min-w-0 flex-1 items-center gap-1">
          <h1 className="truncate text-base font-semibold tracking-[-0.015em] tabular-nums sm:text-lg">
            {formatUSPhone(engagement.phone)}
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
          <span className="sr-only" role="status">
            {copyState === "copied"
              ? "Phone number copied"
              : copyState === "failed"
                ? "Phone number could not be copied"
                : ""}
          </span>
          <Button
            className="ml-1 size-10 min-h-10 rounded-full"
            type="button"
            size="icon-lg"
            aria-label="Call"
            title={`Call ${formatUSPhone(engagement.phone)}`}
            disabled={
              !canMutate ||
              !callingEnabled ||
              !route ||
              !ownsSoftphone ||
              callingOccupied
            }
            onClick={() => {
              setCallError("")
              void startOutbound(route, engagement.phone).then(
                (requestError) => setCallError(requestError ?? ""),
              )
            }}
          >
            {outboundPending ? (
              <Spinner />
            ) : (
              <PhoneCallIcon className="size-[1.125rem]" />
            )}
          </Button>
        </div>
        <div className="flex min-w-0 shrink-0 items-center gap-1 sm:gap-2">
          {headerTrailing}
          {engagement.locations.length > 1 && (
            <NativeSelect
              aria-label="Sender office"
              className="max-w-24 md:max-w-none"
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
        </div>
        {callError && (
          <p className="absolute right-3 top-[calc(100%+0.5rem)] z-10 rounded-lg border bg-popover px-3 py-2 text-xs text-destructive shadow-sm">
            {callError}
          </p>
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
        canMutate={canMutate}
        revision={revision}
        selectedTaskID={selectedTaskID}
        selectedCallID={selectedCallID}
        selectedAIInteractionID={selectedAIInteractionID}
        onTaskCreated={onTaskCreated}
        onTaskOpen={onTaskOpen}
        onCallOpen={onCallOpen}
        onAIInteractionOpen={onAIInteractionOpen}
      />
    </section>
  )
}

function MessageConversation({
  timelineSource,
  practiceID,
  locationID,
  initialDestination,
  canMutate,
  revision,
  selectedTaskID,
  selectedCallID,
  selectedAIInteractionID,
  onTaskCreated,
  onTaskOpen,
  onCallOpen,
  onAIInteractionOpen,
}: {
  timelineSource: TimelineSource
  practiceID: string
  locationID: string
  initialDestination?: string
  canMutate: boolean
  revision: number
  selectedTaskID?: string
  selectedCallID?: string
  selectedAIInteractionID?: string
  onTaskCreated: (task: Task) => void
  onTaskOpen?: (task: Task) => void
  onCallOpen?: (callID: string) => void
  onAIInteractionOpen?: (interactionID: string) => void
}) {
  const timelineKey = `${timelineSource.practiceID}:${timelineSource.phone}`
  const [items, setItems] = useState<ConversationTimelineItem[]>([])
  const [cursor, setCursor] = useState("")
  const [loading, setLoading] = useState(true)
  const [loadingOlder, setLoadingOlder] = useState(false)
  const [newActivity, setNewActivity] = useState(false)
  const [error, setError] = useState("")
  const generation = useRef(0)
  const committedMessage = useRef<
    { id: string; visibleUntil: number } | undefined
  >(undefined)
  const scroller = useRef<HTMLDivElement | null>(null)
  const atLatest = useRef(true)
  const initialized = useRef(false)

  const loadPage = useCallback(
    (token: string, cursor = "") =>
      getEngagementTimeline({
        client: portalClient(token),
        path: { phone: timelineSource.phone },
        query: {
          practiceId: timelineSource.practiceID,
          ...(cursor ? { cursor } : {}),
          limit: 50,
        },
      }).catch(() => undefined),
    [timelineSource.phone, timelineSource.practiceID],
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
  }, [loadLatest])

  useEffect(() => {
    if (!timelineKey) return
    const timeout = window.setTimeout(() => {
      void loadLatest(true)
    }, 0)
    return () => window.clearTimeout(timeout)
  }, [loadLatest, timelineKey])

  useEffect(() => {
    if (!initialized.current || !timelineKey) return
    if (atLatest.current) {
      void loadLatest(true)
    } else {
      setNewActivity(true)
    }
  }, [loadLatest, revision, timelineKey])

  async function loadOlder() {
    if (!cursor || loadingOlder) return
    setLoadingOlder(true)
    const token = await getAccessToken()
    if (!token) {
      setLoadingOlder(false)
      return
    }
    const result = await loadPage(token, cursor)
    setLoadingOlder(false)
    if (!result?.data) return
    setItems((current) => [...result.data.items, ...current])
    setCursor(result.data.nextCursor)
  }

  const conversationThread = items.find(
    (item) => item.message?.thread.locationId === locationID,
  )?.message?.thread
  const composerThreadID = conversationThread?.id ?? ""
  const presentedItems = presentTimeline(items)
  const followUpCallIDs = recoveryFollowUpCallIDs(items)
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <MessageScrollerProvider
        autoScroll
        defaultScrollPosition="end"
        scrollEdgeThreshold={72}
      >
        <MessageScroller className="min-h-0 flex-1 bg-background">
          <MessageScrollerViewport
            ref={scroller}
            aria-label="Conversation activity"
            data-testid="message-timeline"
            preserveScrollOnPrepend
            className="px-3 py-4 sm:px-4"
            onScroll={(event) => {
              const element = event.currentTarget
              atLatest.current =
                element.scrollHeight - element.scrollTop - element.clientHeight <
                72
            }}
          >
            <MessageScrollerContent className="mx-auto max-w-2xl gap-2">
              {cursor && (
                <MessageScrollerItem
                  messageId="earlier-activity"
                  className="flex justify-center"
                >
                  <Button
                    size="sm"
                    variant="outline"
                    className="bg-background"
                    disabled={loadingOlder}
                    onClick={() => void loadOlder()}
                  >
                    {loadingOlder ? <Spinner /> : <RefreshCwIcon />}
                    Earlier activity
                  </Button>
                </MessageScrollerItem>
              )}
              {loading && (
                <MessageScrollerItem messageId="loading-conversation">
                  <div
                    aria-label="Loading conversation"
                    className="flex flex-col gap-3 py-8"
                    role="status"
                  >
                    <Skeleton className="ml-auto h-12 w-2/3 rounded-lg" />
                    <Skeleton className="h-14 w-3/4 rounded-lg" />
                    <Skeleton className="mx-auto h-16 w-full max-w-xl rounded-md" />
                    <span className="sr-only">Loading conversation</span>
                  </div>
                </MessageScrollerItem>
              )}
              {!loading &&
                presentedItems.map((item, index) => (
                  <MessageScrollerItem
                    key={`${item.type}:${item.id}`}
                    messageId={`${item.type}:${item.id}`}
                  >
                    <Fragment>
                      {(index === 0 ||
                        !sameConversationDate(
                          presentedItems[index - 1]!.occurredAt,
                          item.occurredAt,
                        )) && (
                        <Marker variant="separator" className="my-2">
                          <MarkerContent>
                            {conversationDateLabel(item.occurredAt)}
                          </MarkerContent>
                        </Marker>
                      )}
                      <TimelineEntry
                        item={item}
                        contextLocationID={locationID}
                        canMutate={canMutate}
                        onChanged={() => void loadLatest(true)}
                        onTaskCreated={onTaskCreated}
                        onTaskOpen={onTaskOpen}
                        onCallOpen={onCallOpen}
                        onAIInteractionOpen={onAIInteractionOpen}
                        selectedTaskID={selectedTaskID}
                        selectedCallID={selectedCallID}
                        selectedAIInteractionID={selectedAIInteractionID}
                        recoveryFollowUp={
                          item.type === "CALL" &&
                          followUpCallIDs.has(item.call?.id ?? "")
                        }
                      />
                    </Fragment>
                  </MessageScrollerItem>
                ))}
              {!loading && presentedItems.length === 0 && (
                <MessageScrollerItem messageId="empty-conversation">
                  <Empty className="my-10 border-0">
                    <EmptyHeader>
                      <EmptyTitle>No activity yet</EmptyTitle>
                      <EmptyDescription>
                        Calls, texts, and Tasks for this number will appear here.
                      </EmptyDescription>
                    </EmptyHeader>
                  </Empty>
                </MessageScrollerItem>
              )}
            </MessageScrollerContent>
          </MessageScrollerViewport>
          {newActivity ? (
            <div className="pointer-events-none absolute inset-x-0 bottom-3 flex justify-center">
              <Button
                size="sm"
                className="pointer-events-auto shadow-lg"
                onClick={() => {
                  atLatest.current = true
                  void loadLatest(true)
                }}
              >
                New activity
              </Button>
            </div>
          ) : (
            <MessageScrollerButton direction="end" />
          )}
        </MessageScroller>
      </MessageScrollerProvider>
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
        destination={initialDestination ?? ""}
        disabled={
          !canMutate ||
          !locationID ||
          Boolean(conversationThread?.outboundBlocked)
        }
        disabledReason={
          !canMutate
            ? "Read only"
            : !locationID
              ? "Choose an authorized sender route"
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
        }}
      />
    </div>
  )
}

function TimelineEntry({
  item,
  contextLocationID,
  canMutate,
  onChanged,
  onTaskCreated,
  onTaskOpen,
  onCallOpen,
  onAIInteractionOpen,
  selectedTaskID,
  selectedCallID,
  selectedAIInteractionID,
  recoveryFollowUp,
}: {
  item: ConversationTimelineItem
  contextLocationID: string
  canMutate: boolean
  onChanged: () => void
  onTaskCreated: (task: Task) => void
  onTaskOpen?: (task: Task) => void
  onCallOpen?: (callID: string) => void
  onAIInteractionOpen?: (interactionID: string) => void
  selectedTaskID?: string
  selectedCallID?: string
  selectedAIInteractionID?: string
  recoveryFollowUp: boolean
}) {
  if (item.type === "MESSAGE" && item.message) {
    return (
      <MessageEntry
        message={item.message}
        showLocation={item.message.thread.locationId !== contextLocationID}
        canMutate={canMutate}
        onChanged={onChanged}
        onTaskCreated={onTaskCreated}
      />
    )
  }
  if (item.type === "CALL" && item.call) {
    const touchpoint = callTouchpoint(item.call)
    return (
      <ActivityItem
        selected={item.call.id === selectedCallID}
        title={item.call.transferReason || touchpoint.label}
        metadata={[
          item.call.transferReason ? touchpoint.label : "",
          recoveryFollowUp ? "Follow-up created" : "",
          touchpoint.detail,
          item.call.locationId !== contextLocationID
            ? item.call.locationName
            : "",
          formatTime(item.occurredAt),
        ]}
        actionLabel={
          item.call.outcome === "VOICEMAIL" ? "View voicemail" : "View call"
        }
        onOpen={onCallOpen ? () => onCallOpen(item.call!.id) : undefined}
      />
    )
  }
  if (item.type === "AI_INTERACTION" && item.aiInteraction) {
    const interaction = item.aiInteraction
    const presentation = aiCallTimelinePresentation(
      interaction.appointmentOutcome,
      interaction.status,
    )
    return (
      <ActivityItem
        selected={interaction.id === selectedAIInteractionID}
        title={presentation.title}
        metadata={[
          presentation.detail,
          interaction.locationId !== contextLocationID
            ? interaction.locationName
            : "",
          formatTime(item.occurredAt),
        ]}
        actionLabel="View AI call"
        onOpen={
          onAIInteractionOpen
            ? () => onAIInteractionOpen(interaction.id)
            : undefined
        }
      />
    )
  }
  if (item.type === "TASK" && item.task) {
    const task = item.task
    return (
      <ActivityItem
        selected={task.id === selectedTaskID}
        title={task.title}
        metadata={[
          taskActivityDetail(item.taskActivity, task),
          task.locationId !== contextLocationID ? task.locationName : "",
          formatTime(item.occurredAt),
        ]}
        actionLabel="View task"
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
  showLocation,
  canMutate,
  onChanged,
  onTaskCreated,
}: {
  message: Message
  showLocation: boolean
  canMutate: boolean
  onChanged: () => void
  onTaskCreated: (task: Task) => void
}) {
  const [pending, setPending] = useState(false)
  const [error, setError] = useState("")
  const [copyState, setCopyState] = useState<"idle" | "copied" | "failed">(
    "idle",
  )
  const [selected, setSelected] = useState(false)
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
      body: {},
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

  function copyMessage() {
    setCopyState("idle")
    void navigator.clipboard.writeText(message.body).then(
      () => setCopyState("copied"),
      () => setCopyState("failed"),
    )
  }

  const actionClassName = cn(
    "sm:opacity-0 sm:group-focus-within/message:opacity-100 sm:group-hover/message:opacity-100 motion-reduce:duration-0 motion-reduce:transition-none",
    selected && "sm:opacity-100",
  )

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
      <MessageContent>
        <Bubble variant={outbound ? "muted" : "ghost"}>
          <BubbleContent>
            {message.body && (
              <p className="whitespace-pre-wrap">{message.body}</p>
            )}
            {message.attachment && (
              <MessageAttachmentView
                attachment={message.attachment}
                canMutate={canMutate}
                onChanged={onChanged}
              />
            )}
          </BubbleContent>
        </Bubble>
        <MessageFooter className="flex-wrap gap-x-2 gap-y-1 tabular-nums">
          <time dateTime={message.createdAt}>
            {formatTime(message.createdAt)}
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
              <span>{message.delivery}</span>
            </>
          )}
          {message.createdBy?.kind === "SERVICE" && (
            <>
              <span aria-hidden="true">·</span>
              <span>Automated</span>
            </>
          )}
          {message.safeFailureCode && (
            <>
              <span aria-hidden="true">·</span>
              <span>{message.safeFailureCode}</span>
            </>
          )}
          <span className="flex items-center gap-0.5">
            {message.body && (
              <Tooltip>
                <TooltipTrigger
                  render={
                    <Button
                      type="button"
                      size="icon-sm"
                      variant="ghost"
                      aria-label="Copy message"
                      className={actionClassName}
                      onClick={copyMessage}
                    />
                  }
                >
                  {copyState === "copied" ? (
                    <CheckIcon className="size-3.5" />
                  ) : (
                    <CopyIcon className="size-3.5" />
                  )}
                </TooltipTrigger>
                <TooltipContent
                  data-testid="copy-message-tooltip"
                  className="bg-black text-white"
                  arrowClassName="bg-black fill-black"
                >
                  Copy message
                </TooltipContent>
              </Tooltip>
            )}
            {canMutate && !message.taskId && (
              <Tooltip>
                <TooltipTrigger
                  render={
                    <Button
                      type="button"
                      size="icon-sm"
                      variant="ghost"
                      aria-label="Create task"
                      className={actionClassName}
                      disabled={pending}
                      onClick={() => void createTask()}
                    />
                  }
                >
                  <CheckSquareIcon className="size-3.5" />
                </TooltipTrigger>
                <TooltipContent
                  className="bg-black text-white"
                  arrowClassName="bg-black fill-black"
                >
                  Create task
                </TooltipContent>
              </Tooltip>
            )}
            {canMutate &&
              outbound &&
              (message.delivery === "Failed" ||
                message.delivery === "Status unknown") && (
                <DropdownMenu>
                  <DropdownMenuTrigger
                    render={
                      <Button
                        type="button"
                        size="icon-sm"
                        variant="ghost"
                        aria-label="More message actions"
                        className={actionClassName}
                      />
                    }
                  >
                    <EllipsisIcon />
                  </DropdownMenuTrigger>
                  <DropdownMenuContent align={outbound ? "end" : "start"}>
                    <DropdownMenuGroup>
                      <DropdownMenuItem
                        disabled={pending}
                        onClick={() => void sendAgain()}
                      >
                        {message.delivery === "Status unknown" ? (
                          <AlertTriangleIcon />
                        ) : (
                          <RefreshCwIcon />
                        )}
                        Send again
                      </DropdownMenuItem>
                    </DropdownMenuGroup>
                  </DropdownMenuContent>
                </DropdownMenu>
              )}
            {pending && <Spinner />}
          </span>
          {error && (
            <p role="alert" className="basis-full text-destructive">
              {error}
            </p>
          )}
          <span
            role="status"
            className="sr-only"
          >
            {copyState === "copied"
              ? "Message copied"
              : copyState === "failed"
                ? "Message could not be copied"
                : ""}
          </span>
        </MessageFooter>
      </MessageContent>
    </MessageRow>
  )
}

function ActivityItem({
  title,
  metadata,
  actionLabel,
  selected,
  onOpen,
}: {
  title: string
  metadata: string[]
  actionLabel: string
  selected: boolean
  onOpen?: () => void
}) {
  const content = (
    <span className="flex flex-1 flex-col gap-1">
      <span className="line-clamp-1 w-fit text-xs/relaxed font-medium leading-snug">
        {title}
      </span>
      <span className="line-clamp-2 text-left text-xs/relaxed font-normal text-muted-foreground">
        {metadata.filter(Boolean).join(" · ")}
      </span>
    </span>
  )

  if (!onOpen) {
    return (
      <Item size="sm" className="mx-auto max-w-xl">
        {content}
      </Item>
    )
  }

  return (
    <Item
      size="sm"
      data-selected={selected || undefined}
      className="relative mx-auto max-w-xl cursor-pointer text-left before:absolute before:inset-y-2 before:left-0 before:w-0.5 before:rounded-full before:bg-transparent hover:bg-muted data-[selected=true]:bg-transparent data-[selected=true]:before:bg-foreground data-[selected=true]:hover:bg-transparent"
      render={
        <button
          type="button"
          aria-current={selected ? "true" : undefined}
          aria-label={`${actionLabel}: ${title}`}
          onClick={onOpen}
        />
      }
    >
      {content}
    </Item>
  )
}

function MessageAttachmentView({
  attachment,
  canMutate,
  onChanged,
}: {
  attachment: MessageAttachment
  canMutate: boolean
  onChanged: () => void
}) {
  const [objectURL, setObjectURL] = useState("")
  const [pending, setPending] = useState(false)
  const [error, setError] = useState("")
  const isPDF = attachment.contentType === "application/pdf"
  const unavailable =
    attachment.state === "Attachment unavailable" || Boolean(error)
  const isPreparing =
    attachment.state === "Pending" || attachment.state === "Processing"
  const presentationState = unavailable
    ? "error"
    : pending || isPreparing
      ? "processing"
      : "done"
  const description = unavailable
    ? error || "Attachment unavailable"
    : isPreparing
      ? "Preparing attachment"
      : `${isPDF ? "PDF" : "Image"} · ${formatBytes(attachment.byteSize)}`

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
      body: {},
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
    setError("")
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
    <Attachment
      state={presentationState}
      size="sm"
      className="mt-2 w-64 max-w-full"
    >
      <AttachmentMedia variant={objectURL && !isPDF ? "image" : "icon"}>
        {objectURL && !isPDF ? (
          // eslint-disable-next-line @next/next/no-img-element -- private object URL
          <img src={objectURL} alt={attachment.fileName} />
        ) : isPDF ? (
          <FileTextIcon aria-hidden="true" />
        ) : (
          <ImageIcon aria-hidden="true" />
        )}
      </AttachmentMedia>
      <AttachmentContent>
        <AttachmentTitle>{attachment.fileName}</AttachmentTitle>
        <AttachmentDescription>{description}</AttachmentDescription>
      </AttachmentContent>
      <AttachmentActions>
        {attachment.state === "Stored" && (
          <AttachmentAction
            aria-label={`Download ${attachment.fileName}`}
            disabled={pending}
            onClick={() => void download()}
          >
            {pending ? <Spinner /> : <DownloadIcon data-icon="inline-start" />}
          </AttachmentAction>
        )}
        {attachment.state === "Attachment unavailable" && canMutate && (
          <AttachmentAction
            aria-label={`Retry ${attachment.fileName}`}
            disabled={pending}
            onClick={() => void retry()}
          >
            {pending ? <Spinner /> : <RefreshCwIcon data-icon="inline-start" />}
          </AttachmentAction>
        )}
      </AttachmentActions>
    </Attachment>
  )
}

function DraftAttachment({
  file,
  pending,
  onRemove,
}: {
  file: File
  pending: boolean
  onRemove: () => void
}) {
  const isImage = file.type.startsWith("image/")
  const [objectURL, setObjectURL] = useState("")

  useEffect(() => {
    if (!isImage) return
    let nextObjectURL = ""
    const timeout = window.setTimeout(() => {
      nextObjectURL = URL.createObjectURL(file)
      setObjectURL(nextObjectURL)
    }, 0)
    return () => {
      window.clearTimeout(timeout)
      if (nextObjectURL) URL.revokeObjectURL(nextObjectURL)
    }
  }, [file, isImage])

  return (
    <Attachment
      state={pending ? "uploading" : "idle"}
      size="xs"
      className="w-64 max-w-full"
    >
      <AttachmentMedia variant={objectURL ? "image" : "icon"}>
        {objectURL ? (
          // eslint-disable-next-line @next/next/no-img-element -- local draft preview
          <img src={objectURL} alt="" />
        ) : (
          <FileTextIcon aria-hidden="true" />
        )}
      </AttachmentMedia>
      <AttachmentContent>
        <AttachmentTitle>{file.name}</AttachmentTitle>
        <AttachmentDescription>
          {file.type === "application/pdf" ? "PDF" : "Image"} ·{" "}
          {formatBytes(file.size)}
        </AttachmentDescription>
      </AttachmentContent>
      <AttachmentActions>
        <AttachmentAction
          type="button"
          aria-label="Remove attachment"
          disabled={pending}
          onClick={onRemove}
        >
          <XIcon data-icon="inline-start" />
        </AttachmentAction>
      </AttachmentActions>
    </Attachment>
  )
}

function MessageComposer({
  threadID,
  practiceID,
  locationID,
  destination,
  disabled,
  disabledReason,
  onSent,
}: {
  threadID: string
  practiceID: string
  locationID: string
  destination: string
  disabled: boolean
  disabledReason: string
  onSent: (message: Message) => void
}) {
  const [body, setBody] = useState("")
  const [file, setFile] = useState<File>()
  const [pending, setPending] = useState(false)
  const [error, setError] = useState("")
  const fileInput = useRef<HTMLInputElement>(null)
  const textarea = useRef<HTMLTextAreaElement>(null)
  const draftAttempt = useRef<
    | {
        signature: string
        idempotencyKey: string
        attachmentID: string
      }
    | undefined
  >(undefined)

  useEffect(() => {
    const element = textarea.current
    if (!element) return
    element.style.height = "0px"
    element.style.height = `${Math.min(element.scrollHeight, 160)}px`
  }, [body])

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
        idempotencyKey: attempt.idempotencyKey,
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
      className="bg-background px-3 pb-3 pt-2 sm:px-4"
      onSubmit={(event) => void submit(event)}
    >
      <div className="mx-auto max-w-2xl">
        <InputGroup
          focusStyle="quiet"
          shape="pill"
          className="h-auto min-h-16 bg-card px-1.5 shadow-sm"
        >
          <InputGroupAddon align="inline-start">
            <InputGroupButton
              type="button"
              size="icon-sm"
              aria-label="Attach one file"
              disabled={disabled || pending}
              onClick={() => fileInput.current?.click()}
            >
              <PaperclipIcon />
            </InputGroupButton>
          </InputGroupAddon>
          <InputGroupTextarea
            ref={textarea}
            aria-label="Message"
            aria-invalid={Boolean(error)}
            rows={1}
            maxLength={maximumMessageLength}
            placeholder="Message"
            className="max-h-40 min-h-12 py-3 text-sm leading-5"
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
          <InputGroupAddon align="inline-end">
            <InputGroupButton
              type="submit"
              size="icon-sm"
              variant="default"
              aria-label="Send message"
              className="size-9 rounded-full"
              disabled={
                disabled ||
                pending ||
                (!body.trim() && !file) ||
                (!threadID && !destination.trim())
              }
            >
              {pending ? <Spinner /> : <ArrowUpIcon />}
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
          <div className="mt-1.5 flex min-h-5 items-start gap-2 text-xs text-muted-foreground">
            {file && (
              <DraftAttachment
                key={`${file.name}:${file.size}:${file.lastModified}`}
                file={file}
                pending={pending}
                onRemove={() => {
                  draftAttempt.current = undefined
                  setFile(undefined)
                }}
              />
            )}
            {body.length >= 1_400 && (
              <span className="ml-auto pt-1 tabular-nums">
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

function taskActivityDetail(
  activity: ConversationTimelineItem["taskActivity"],
  task: Task,
) {
  switch (activity) {
    case "TASK_CREATED":
      return task.origin === "ABITA_AI" ? "Task created by AI" : "Task created"
    case "TITLE_CHANGED":
      return "Task title changed"
    case "TASK_COMPLETED":
      return "Task completed"
    case "TASK_REOPENED":
      return "Task reopened"
    case "INTERACTION_ATTACHED":
      return "New activity added"
    case "TASK_AUTO_COMPLETED_INBOUND_CALL":
      return "Task completed after connected call"
    case "TASK_AUTO_COMPLETED_BOOKING":
      return "Task completed after booking"
    case "TASK_AUTO_COMPLETED_DUPLICATE":
      return "Duplicate Task resolved"
    default:
      return task.state === "OPEN" ? "Task open" : "Task completed"
  }
}

function callTouchpoint(call: NonNullable<ConversationTimelineItem["call"]>) {
  const duration = call.durationSeconds > 0 ? formatDuration(call.durationSeconds) : ""
  if (call.outcome === "VOICEMAIL") {
    return {
      label: "Voicemail",
      detail: duration,
    }
  }
  if (call.outcome === "MISSED" || call.outcome === "UNANSWERED") {
    return {
      label: call.direction === "INBOUND" ? "Missed call" : "Unanswered call",
      detail: duration,
    }
  }
  if (call.direction === "INBOUND") {
    return {
      label: "Inbound call",
      detail: [sentenceCase(call.outcome), duration].filter(Boolean).join(" · "),
    }
  }
  return {
    label: "Outbound call",
    detail: [sentenceCase(call.outcome), duration].filter(Boolean).join(" · "),
  }
}

function formatTime(value: string) {
  return new Intl.DateTimeFormat(undefined, {
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
