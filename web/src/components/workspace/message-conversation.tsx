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
} from "@/components/ui/message-scroller"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import type { Task } from "@/lib/api/generated/types.gen"
import { useEngagementTimeline } from "@/lib/clients/engagement"
import { formatDate } from "@/lib/format-time"
import { labelOptions, threadTabLabels } from "@/lib/vocabulary"
import {
  type NumberHistory,
  type ThreadTab,
  presentTimeline,
  sameConversationDate,
} from "@/lib/workspace-history"

import { MessageComposer } from "./message-composer"
import { TimelineEntry } from "./timeline-entry"

const tabOptions = labelOptions(threadTabLabels)

const tabSearchPages = 3

const emptyTitles = {
  all: "No activity yet",
  texts: "No texts yet",
  calls: "No calls yet",
} satisfies Record<ThreadTab, string>

export function MessageConversation({
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
}: {
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
}) {
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
  const error = failure
    ? "The conversation could not be loaded."
    : olderFailure ? "Earlier activity could not be loaded." : ""
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
  const presentedItems = presentTimeline(items, tab)
  const tabIsEmpty = tab !== "all" && presentedItems.length === 0

  const everythingPlace = useRef({ phone, fromLatest: 0 })
  function showTab(next: ThreadTab) {
    if (next === tab) return
    const viewport = scroller.current
    if (tab === "all" && viewport) {
      everythingPlace.current = {
        phone,
        fromLatest: viewport.scrollHeight - viewport.scrollTop - viewport.clientHeight,
      }
    }
    const fromLatest =
      next === "all" && everythingPlace.current.phone === phone
        ? everythingPlace.current.fromLatest
        : 0
    setTabChoice({ phone, tab: next })
    setAwayFromLatest(fromLatest >= 72)
    window.requestAnimationFrame(() => {
      const shown = scroller.current
      shown?.scrollTo({ top: shown.scrollHeight - shown.clientHeight - fromLatest })
    })
  }

  const search = useRef({ scope: "", cursors: new Set<string>() })
  const searchOlder = useEffectEvent(() => {
    const scope = `${phone}:${tab}`
    if (search.current.scope !== scope) search.current = { scope, cursors: new Set() }
    const { cursors } = search.current
    if (cursors.has(cursor) || cursors.size >= tabSearchPages) return
    cursors.add(cursor)
    showOlder()
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
            className="px-4 py-6 sm:px-6"
            onScroll={(event) => {
              const element = event.currentTarget
              setAwayFromLatest(
                element.scrollHeight - element.scrollTop - element.clientHeight >=
                  72,
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
                    onClick={() => void showOlder()}
                  >
                    {loadingOlder ? <Spinner /> : <RefreshCwIcon />}
                    Earlier activity
                  </Button>
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
              {!loading && !loadingOlder && !error && presentedItems.length === 0 && (
                <MessageScrollerItem messageId="empty-conversation">
                  <Empty className="my-10 border-0">
                    <EmptyHeader>
                      <EmptyTitle>{emptyTitles[tab]}</EmptyTitle>
                      <EmptyDescription>
                        {cursor
                          ? "Earlier activity is not loaded. Load it to keep looking."
                          : "Calls, texts, and Tasks for this number will appear here."}
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
      </MessageScrollerProvider>
      {error && (
        <Alert variant="destructive" className="rounded-none border-x-0">
          <AlertTitle>Conversation unavailable</AlertTitle>
          <AlertDescription>
            {error}
            <Button
              size="sm"
              variant="outline"
              disabled={loading || refreshing || loadingOlder}
              onClick={failure ? loadLatest : showOlder}
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
