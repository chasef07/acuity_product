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
import type { Task } from "@/lib/api/generated/types.gen"
import { useEngagementTimeline } from "@/lib/clients/engagement"
import { formatDate } from "@/lib/format-time"
import {
  type NumberHistory,
  presentTimeline,
  sameConversationDate,
} from "@/lib/workspace-history"

import { MessageComposer } from "./message-composer"
import { TimelineEntry } from "./timeline-entry"

type TimelineSource = {
  kind: "engagement"
  practiceID: string
  phone: string
}

export function MessageConversation({
  reviewKey,
  onReviewed,
  timelineSource,
  practiceID,
  locationID,
  initialDestination,
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
  timelineSource: TimelineSource
  practiceID: string
  locationID: string
  initialDestination?: string
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
  const {
    items,
    nextCursor: cursor,
    loading,
    refreshing,
    loadingOlder,
    failure,
    sent,
    requestedAt,
    behind,
    refresh,
    showOlder,
    showSent,
  } = useEngagementTimeline({ ...timelineSource, hold: awayFromLatest })
  const error = failure ? "The conversation could not be loaded." : ""
  const scroller = useRef<HTMLDivElement | null>(null)
  const reviewSeenAt = useRef(0)
  const historyPhone = timelineSource.phone
  useEffect(() => {
    onHistoryChange?.({ phone: historyPhone, items })
  }, [historyPhone, items, onHistoryChange])

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
  const presentedItems = presentTimeline(items)
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
                      <Marker variant="separator" className="my-2">
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
              {!loading && !error && presentedItems.length === 0 && (
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
              disabled={loading || refreshing}
              onClick={loadLatest}
            >
              {(loading || refreshing) && <Spinner />}
              Try again
            </Button>
          </AlertDescription>
        </Alert>
      )}
      <MessageComposer
        threadID={composerThreadID}
        practiceID={practiceID}
        locationID={locationID}
        destination={initialDestination ?? ""}
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
          setAwayFromLatest(false)
          showSent(message)
        }}
      />
    </div>
  )
}
