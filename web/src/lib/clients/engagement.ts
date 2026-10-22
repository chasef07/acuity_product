import { useCallback, useMemo, useState } from "react"

import {
  createMessageFollowUpTask,
  getEngagementTimeline,
  getMessageAttachment,
  retryInboundMessageAttachment,
  sendMessage as sendMessageRequest,
  sendMessageAgain as sendMessageAgainRequest,
  uploadMessageAttachment as uploadMessageAttachmentRequest,
} from "../api/generated/sdk.gen"
import type {
  ConversationTimelineItem,
  ConversationTimelinePage,
  Message,
  UploadMessageAttachmentRequest,
} from "../api/generated/types.gen"
import { readFailure, usePages } from "../data/queries"
import { portalRequest } from "./portal-request"

const sentMessageHoldMilliseconds = 750

const noItems: ConversationTimelineItem[] = []

type SentMessage = { item: ConversationTimelineItem; visibleUntil: number }

export function useEngagementTimeline({
  practiceID,
  phone,
}: {
  practiceID: string
  phone: string
}) {
  const timeline = usePages({
    live: "task-detail",
    name: "engagement-timeline",
    identity: { practiceID, phone },
    read: async (identity, cursor, transport) => {
      const requestedAt = Date.now()
      const result = await getEngagementTimeline({
        ...transport,
        path: { phone: identity.phone },
        query: {
          practiceId: identity.practiceID,
          groupCalls: true,
          ...(cursor ? { cursor } : {}),
          limit: 50,
        },
      })
      return { ...result, data: result.data && { ...result.data, requestedAt } }
    },
    combine: combineTimelinePages,
  })
  const [sent, setSent] = useState<SentMessage[]>([])
  const loaded = timeline.status === "ready" ? timeline.data : undefined
  const requestedAt = loaded?.requestedAt ?? 0
  const items = useMemo(() => {
    const held = sent.filter((message) => requestedAt < message.visibleUntil)
    const heldIDs = new Set(held.map((message) => message.item.id))
    const shown = loaded?.items ?? noItems
    return held.length === 0
      ? shown
      : [...shown.filter((item) => !heldIDs.has(item.id)), ...held.map((message) => message.item)]
  }, [loaded?.items, requestedAt, sent])
  const showSent = useCallback((message: Message) => {
    const visibleUntil =
      Date.now() + (message.delivery === "Sending" ? sentMessageHoldMilliseconds : 0)
    setSent((current) => [
      ...current.filter((entry) => entry.item.id !== message.id),
      {
        item: { type: "MESSAGE", id: message.id, occurredAt: message.createdAt, message },
        visibleUntil,
      },
    ])
  }, [])
  const sending = sent.filter((entry) => entry.item.message?.delivery === "Sending").at(-1)
  return {
    items,
    nextCursor: loaded?.nextCursor ?? "",
    loading: timeline.status === "loading",
    refreshing: timeline.status === "ready" && timeline.refreshing,
    loadingOlder: timeline.loadingMore,
    requestedAt,
    failure: readFailure(timeline),
    ...(sending ? { sent: { id: sending.item.id, visibleUntil: sending.visibleUntil } } : {}),
    refresh: timeline.retry,
    showOlder: () => void timeline.loadMore(),
    showSent,
  }
}

function combineTimelinePages(pages: (ConversationTimelinePage & { requestedAt: number })[]) {
  return {
    items: pages.flatMap((page) => page.items),
    nextCursor: pages.at(-1)!.nextCursor,
    requestedAt: pages[0].requestedAt,
  }
}

export function createFollowUpTask(message: Pick<Message, "id">) {
  return portalRequest((transport) =>
    createMessageFollowUpTask({
      ...transport,
      path: { messageId: message.id },
      body: {},
    }),
  )
}

export function sendMessageAgain(
  message: Pick<Message, "id">,
  {
    idempotencyKey,
    duplicateRiskAcknowledged,
  }: { idempotencyKey: string; duplicateRiskAcknowledged: boolean },
) {
  return portalRequest((transport) =>
    sendMessageAgainRequest({
      ...transport,
      path: { messageId: message.id },
      body: { idempotencyKey, duplicateRiskAcknowledged },
    }),
  )
}

export function readMessageAttachment(
  attachment: { id: string },
  signal?: AbortSignal,
) {
  return portalRequest(
    (transport) =>
      getMessageAttachment({
        ...transport,
        path: { attachmentId: attachment.id },
      }),
    signal,
  )
}

export function retryMessageAttachment(attachment: { id: string }) {
  return portalRequest((transport) =>
    retryInboundMessageAttachment({
      ...transport,
      path: { attachmentId: attachment.id },
      body: {},
    }),
  )
}

export function uploadMessageAttachment({
  practiceID,
  locationID,
  file,
}: {
  practiceID: string
  locationID: string
  file: File
}) {
  return portalRequest(async (transport) =>
    uploadMessageAttachmentRequest({
      ...transport,
      body: {
        practiceId: practiceID,
        locationId: locationID,
        fileName: file.name,
        contentType: file.type as UploadMessageAttachmentRequest["contentType"],
        contentBase64: await fileToBase64(file),
      },
    }),
  )
}

export function sendMessage({
  practiceID,
  locationID,
  threadID,
  destination,
  body,
  attachmentID,
  idempotencyKey,
}: {
  practiceID: string
  locationID: string
  threadID: string
  destination: string
  body: string
  attachmentID: string
  idempotencyKey: string
}) {
  return portalRequest((transport) =>
    sendMessageRequest({
      ...transport,
      body: {
        practiceId: practiceID,
        locationId: locationID,
        ...(threadID ? { threadId: threadID } : { destination }),
        body,
        ...(attachmentID ? { attachmentId: attachmentID } : {}),
        idempotencyKey,
      },
    }),
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
