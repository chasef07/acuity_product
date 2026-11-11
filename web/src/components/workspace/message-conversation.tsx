"use client"

import { Fragment, useEffect, useEffectEvent, useRef, useState } from "react"
import { RefreshCwIcon } from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "@/components/ui/empty"
import { Marker, MarkerContent } from "@/components/ui/marker"
import {
  MessageScroller,
  MessageScrollerButton,
  MessageScrollerContent,
  MessageScrollerItem,
  MessageScrollerProvider,
  MessageScrollerViewport,
  useMessageScroller,
} from "@/components/ui/message-scroller"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import type { Task } from "@/lib/api/generated/types.gen"
import { useEngagementSummary, useEngagementTimeline } from "@/lib/clients/engagement"
import { formatDate } from "@/lib/format-time"
import { labelOptions, threadTabLabels } from "@/lib/vocabulary"
import {
  type NumberHistory,
  type ThreadTab,
  presentTimeline,
  sameConversationDate,
  withCurrentTasks,
} from "@/lib/workspace-history"

import { MessageComposer } from "./message-composer"
import { TimelineEntry } from "./timeline-entry"

const tabOptions = labelOptions(threadTabLabels)

const tabSearchPages = 3

const latestThreshold = 72

const placeSettleMilliseconds = 1000

const noTasks: Task[] = []

type ConversationProps = {
  reviewKey: string
  onReviewed: (reviewKey: string) => void
  phone: string
  practiceID: string
  locationID: string
  selectedTaskID?: string
  selectedCallID?: string
  selectedAIInteractionID?: string
  onTaskCreated: (task: Task) => void
  onTaskOpen?: (task: Task) => void
  onCallOpen?: (callID: string) => void
  onAIInteractionOpen?: (interactionID: string) => void
  onHistoryChange?: (history: NumberHistory) => void
}

type Place = { messageID: string; offset: number }

function readPlace(viewport: HTMLElement): Place | undefined {
  if (viewport.scrollHeight - viewport.scrollTop - viewport.clientHeight < latestThreshold) return undefined
  const top = viewport.getBoundingClientRect().top
  for (const element of viewport.querySelectorAll<HTMLElement>("[data-message-id]")) {
    const box = element.getBoundingClientRect()
    if (box.bottom > top) return { messageID: element.dataset.messageId!, offset: box.top - top }
  }
  return undefined
}

function placeTop(viewport: HTMLElement, place: Place) {
  const anchor = [...viewport.querySelectorAll<HTMLElement>("[data-message-id]")]
    .find((element) => element.dataset.messageId === place.messageID)
  return anchor
    ? viewport.scrollTop + anchor.getBoundingClientRect().top - viewport.getBoundingClientRect().top - place.offset
    : undefined
}

const emptyTitles = {
  all: "No activity yet",
  texts: "No texts yet",
  calls: "No calls yet",
} satisfies Record<ThreadTab, string>

export function MessageConversation(props: ConversationProps) {
  return (
    <MessageScrollerProvider
      autoScroll
      defaultScrollPosition="end"
      scrollEdgeThreshold={latestThreshold}
    >
      <Conversation {...props} />
    </MessageScrollerProvider>
  )
}

function Conversation({
  reviewKey,
  onReviewed,
  phone,
  practiceID,
  locationID,
  selectedTaskID,
  selectedCallID,
  selectedAIInteractionID,
  onTaskCreated,
  onTaskOpen,
  onCallOpen,
  onAIInteractionOpen,
  onHistoryChange,
}: ConversationProps) {
  const [awayFromLatest, setAwayFromLatest] = useState(false)
  const [tabChoice, setTabChoice] = useState<{ phone: string; tab: ThreadTab }>({ phone, tab: "all" })
  const {
    items,
    nextCursor: cursor,
    loading,
    refreshing,
    loadingOlder,
    failure,
    olderFailure,
    sent,
    requestedAt,
    behind,
    refresh,
    showOlder,
    showSent,
  } = useEngagementTimeline({ practiceID, phone, hold: awayFromLatest })
  const failed = Boolean(failure || olderFailure)
  const summary = useEngagementSummary({ practiceID, phone, enabled: awayFromLatest })
  const currentTasks = summary.status === "ready"
    ? [...summary.data.openTasks, ...summary.data.closedToday]
    : noTasks
  const { scrollToStart } = useMessageScroller()
  const scroller = useRef<HTMLDivElement | null>(null)
  const reviewSeenAt = useRef(0)
  useEffect(() => {
    onHistoryChange?.({ phone, items })
  }, [phone, items, onHistoryChange])

  const sentVisibleUntil = sent?.visibleUntil
  useEffect(() => {
    if (sentVisibleUntil === undefined) return
    const timeout = window.setTimeout(
      () => void refresh(),
      Math.max(0, sentVisibleUntil - Date.now()),
    )
    return () => window.clearTimeout(timeout)
  }, [refresh, sentVisibleUntil])

  function loadLatest() {
    setAwayFromLatest(false)
    void refresh()
  }

  const seenReviewKey = useRef(reviewKey)
  useEffect(() => {
    if (seenReviewKey.current === reviewKey) return
    seenReviewKey.current = reviewKey
    reviewSeenAt.current = Date.now()
    void refresh()
  }, [refresh, reviewKey])

  const showLatest = useEffectEvent((requestedAt: number) => {
    if (awayFromLatest) return
    if (requestedAt >= reviewSeenAt.current) onReviewed(reviewKey)
    window.requestAnimationFrame(() =>
      scroller.current?.scrollTo({
        top: scroller.current.scrollHeight,
        behavior: "smooth",
      }),
    )
  })
  useEffect(() => {
    if (requestedAt) showLatest(requestedAt)
  }, [requestedAt])

  const conversationThread = items.find(
    (item) => item.message?.thread.locationId === locationID,
  )?.message?.thread
  const composerThreadID = conversationThread?.id ?? ""
  const tab = tabChoice.phone === phone ? tabChoice.tab : "all"
  const presentedItems = presentTimeline(withCurrentTasks(items, currentTasks), tab)
  const tabIsEmpty = tab !== "all" && presentedItems.length === 0

  const everythingPlace = useRef<{ phone: string; place?: Place }>({ phone })
  function showTab(next: ThreadTab) {
    if (next === tab) return
    const viewport = scroller.current
    if (tab === "all" && viewport) everythingPlace.current = { phone, place: readPlace(viewport) }
    const place = next === "all" && everythingPlace.current.phone === phone
      ? everythingPlace.current.place
      : undefined
    setTabChoice({ phone, tab: next })
    setAwayFromLatest(Boolean(place))
    showPlace(place)
  }

  const releasePlace = useRef(() => {})
  useEffect(() => () => releasePlace.current(), [])
  function showPlace(place: Place | undefined) {
    releasePlace.current()
    const frame = window.requestAnimationFrame(() => {
      const shown = scroller.current
      if (!shown) return
      if (!place || placeTop(shown, place) === undefined) {
        shown.scrollTo({ top: shown.scrollHeight - shown.clientHeight })
        return
      }
      const hold = () => {
        if (placeTop(shown, place) === undefined) return
        scrollToStart()
        shown.scrollTo({ top: placeTop(shown, place) })
      }
      hold()
      const content = shown.firstElementChild
      if (!content || typeof ResizeObserver === "undefined") return
      const sizes = new ResizeObserver(hold)
      sizes.observe(content)
      const settled = window.setTimeout(() => sizes.disconnect(), placeSettleMilliseconds)
      releasePlace.current = () => {
        sizes.disconnect()
        window.clearTimeout(settled)
      }
    })
    releasePlace.current = () => window.cancelAnimationFrame(frame)
  }

  const [searched, setSearched] = useState<{ scope: string; count: number }>()
  async function loadEarlier() {
    const scope = `${phone}:${tab}`
    const page = await showOlder()
    if (!page) return
    const found = tab === "all" || presentTimeline(page.items, tab).length > 0
    setSearched((earlier) => found
      ? undefined
      : { scope, count: (earlier?.scope === scope ? earlier.count : 0) + page.items.length })
  }
  const tabNoun = threadTabLabels[tab].toLowerCase()
  const earlierNote = searched?.scope !== `${phone}:${tab}`
    ? ""
    : cursor
      ? `No ${tabNoun} in the next ${searched.count} items. Load more to keep looking.`
      : `No earlier ${tabNoun}.`

  const search = useRef({ scope: "", cursors: new Set<string>() })
  const searchOlder = useEffectEvent(() => {
    const scope = `${phone}:${tab}`
    if (search.current.scope !== scope) search.current = { scope, cursors: new Set() }
    const { cursors } = search.current
    if (cursors.has(cursor) || cursors.size >= tabSearchPages) return
    cursors.add(cursor)
    void showOlder()
  })
  useEffect(() => {
    if (tabIsEmpty && cursor && !loading && !loadingOlder && !olderFailure) searchOlder()
  }, [tabIsEmpty, cursor, loading, loadingOlder, olderFailure])

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex justify-center border-b px-4 py-2">
        <ToggleGroup
          aria-label="Conversation view"
          variant="segmented"
          size="sm"
          spacing={0}
          value={[tab]}
          onValueChange={(values) => {
            const next = tabOptions.find((option) => option.value === values[0])
            if (next) showTab(next.value)
          }}
        >
          {tabOptions.map((option) => (
            <ToggleGroupItem
              key={option.value}
              value={option.value}
              aria-label={`Show ${option.label.toLowerCase()}`}
              className="h-7 px-3 text-xs data-pressed:bg-background data-pressed:shadow-xs"
            >
              {option.label}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
      </div>
      <MessageScroller className="min-h-0 flex-1 bg-background">
        <MessageScrollerViewport
          ref={scroller}
          aria-label="Conversation activity"
          data-testid="message-timeline"
          preserveScrollOnPrepend
          className="px-4 py-6 sm:px-6"
          onWheel={() => releasePlace.current()}
          onTouchMove={() => releasePlace.current()}
          onKeyDown={() => releasePlace.current()}
          onScroll={(event) => {
            const element = event.currentTarget
            setAwayFromLatest(
              element.scrollHeight - element.scrollTop - element.clientHeight >=
                latestThreshold,
            )
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
                  onClick={() => void loadEarlier()}
                >
                  {loadingOlder ? <Spinner /> : <RefreshCwIcon />}
                  Earlier activity
                </Button>
              </MessageScrollerItem>
            )}
            {earlierNote && !tabIsEmpty && (
              <MessageScrollerItem messageId="earlier-note">
                <p role="status" className="text-center text-xs text-muted-foreground">
                  {earlierNote}
                </p>
              </MessageScrollerItem>
            )}
            {loading && items.length === 0 && (
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
            {presentedItems.map((item, index) => (
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
                    <Marker className="my-2">
                      <MarkerContent>
                        {formatDate(item.occurredAt)}
                      </MarkerContent>
                    </Marker>
                  )}
                  <TimelineEntry
                    item={item}
                    contextLocationID={locationID}
                    onChanged={loadLatest}
                    onTaskCreated={onTaskCreated}
                    onTaskOpen={onTaskOpen}
                    onCallOpen={onCallOpen}
                    onAIInteractionOpen={onAIInteractionOpen}
                    selectedTaskID={selectedTaskID}
                    selectedCallID={selectedCallID}
                    selectedAIInteractionID={selectedAIInteractionID}
                  />
                </Fragment>
              </MessageScrollerItem>
            ))}
            {!loading && !loadingOlder && !failed && presentedItems.length === 0 && (
              <MessageScrollerItem messageId="empty-conversation">
                <Empty className="my-10 border-0">
                  <EmptyHeader>
                    <EmptyTitle>{emptyTitles[tab]}</EmptyTitle>
                    <EmptyDescription role={earlierNote ? "status" : undefined}>
                      {earlierNote || (cursor
                        ? "Earlier activity is not loaded. Load it to keep looking."
                        : "Calls, texts, and Tasks for this number will appear here.")}
                    </EmptyDescription>
                  </EmptyHeader>
                </Empty>
              </MessageScrollerItem>
            )}
          </MessageScrollerContent>
        </MessageScrollerViewport>
        {behind ? (
          <div className="pointer-events-none absolute inset-x-0 bottom-3 flex justify-center">
            <Button
              size="sm"
              className="pointer-events-auto shadow-lg"
              onClick={loadLatest}
            >
              New activity
            </Button>
          </div>
        ) : (
          <MessageScrollerButton direction="end" />
        )}
      </MessageScroller>
      {failed && (
        <Alert variant="destructive" className="rounded-none border-x-0">
          <AlertTitle>
            {failure ? "Conversation unavailable" : "Earlier activity could not be loaded"}
          </AlertTitle>
          <AlertDescription>
            {failure && "The conversation could not be loaded."}
            <Button
              size="sm"
              variant="outline"
              disabled={loading || refreshing || loadingOlder}
              onClick={failure ? loadLatest : () => void loadEarlier()}
            >
              {(loading || refreshing || loadingOlder) && <Spinner />}
              Try again
            </Button>
          </AlertDescription>
        </Alert>
      )}
      <MessageComposer
        threadID={composerThreadID}
        practiceID={practiceID}
        locationID={locationID}
        destination={phone}
        disabled={
          !locationID ||
          Boolean(conversationThread?.outboundBlocked)
        }
        disabledReason={
          !locationID
            ? "Choose an authorized sender route"
            : conversationThread?.outboundBlocked
              ? "Outbound messaging is blocked after STOP"
              : ""
        }
        onSent={(message) => {
          if (tab === "calls") setTabChoice({ phone, tab: "all" })
          setAwayFromLatest(false)
          showSent(message)
        }}
      />
    </div>
  )
}
