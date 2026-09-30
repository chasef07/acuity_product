import { useCallback, useEffect, useRef, useState } from "react"

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
  Message,
  UploadMessageAttachmentRequest,
} from "../api/generated/types.gen"
import { presentTimeline } from "../workspace-history"
import { type PortalFailure, portalRequest } from "./portal-request"

/** A just-sent Message stays as sent this long before its delivery status reloads. */
const sentMessageHoldMilliseconds = 750

export type EngagementTimeline = {
  items: ConversationTimelineItem[]
  nextCursor: string
  loading: boolean
  loadingOlder: boolean
  failure?: PortalFailure
  /** Set while a Message still sending is held in place of the loaded one. */
  sent?: { id: string; visibleUntil: number }
}

/**
 * Engagement History for one phone. `refresh` replaces the newest page and
 * resolves true only once that page is shown; it supersedes every request in
 * flight, and so does unmount. `showOlder` appends the next page. `showSent`
 * places a staff Message at the end without waiting for a reload.
 */
export function useEngagementTimeline({
  practiceID,
  phone,
}: {
  practiceID: string
  phone: string
}) {
  const [timeline, setTimeline] = useState<EngagementTimeline>({
    items: [],
    nextCursor: "",
    loading: true,
    loadingOlder: false,
  })
  const latest = useRef<AbortController>(undefined)
  const older = useRef<AbortController>(undefined)
  useEffect(
    () => () => {
      latest.current?.abort()
      older.current?.abort()
    },
    [],
  )

  const readPage = useCallback(
    (cursor: string, signal: AbortSignal) =>
      portalRequest(
        (transport) =>
          getEngagementTimeline({
            ...transport,
            path: { phone },
            query: {
              practiceId: practiceID,
              groupCalls: true,
              ...(cursor ? { cursor } : {}),
              limit: 50,
            },
          }),
        signal,
      ),
    [practiceID, phone],
  )

  const refresh = useCallback(async () => {
    latest.current?.abort()
    older.current?.abort()
    const controller = new AbortController()
    latest.current = controller
    setTimeline((current) => ({ ...current, loading: true }))
    const outcome = await readPage("", controller.signal).catch(() => undefined)
    // portalRequest rejects only after a newer request or unmount aborted it.
    if (!outcome) return false
    if (!outcome.ok) {
      setTimeline((current) => ({
        ...current,
        loading: false,
        failure: outcome.failure,
      }))
      return false
    }
    const page = outcome.data
    setTimeline((current) => {
      const { sent } = current
      const held =
        sent && Date.now() < sent.visibleUntil
          ? current.items.find((item) => item.id === sent.id)
          : undefined
      return {
        items: held
          ? [...page.items.filter((item) => item.id !== held.id), held]
          : page.items,
        nextCursor: page.nextCursor,
        loading: false,
        loadingOlder: current.loadingOlder,
        ...(held ? { sent } : {}),
      }
    })
    return true
  }, [readPage])

  async function showOlder() {
    const cursor = timeline.nextCursor
    if (!cursor || timeline.loadingOlder) return
    const controller = new AbortController()
    older.current = controller
    setTimeline((current) => ({ ...current, loadingOlder: true }))
    const outcome = await readPage(cursor, controller.signal).catch(() => undefined)
    setTimeline((current) =>
      outcome?.ok
        ? {
            ...current,
            items: presentTimeline([...current.items, ...outcome.data.items]),
            nextCursor: outcome.data.nextCursor,
            loadingOlder: false,
          }
        : { ...current, loadingOlder: false },
    )
  }

  const showSent = useCallback((message: Message) => {
    setTimeline((current) => ({
      ...current,
      items: [
        ...current.items.filter((item) => item.id !== message.id),
        { type: "MESSAGE", id: message.id, occurredAt: message.createdAt, message },
      ],
      loading: false,
      ...(message.delivery === "Sending"
        ? {
            sent: {
              id: message.id,
              visibleUntil: Date.now() + sentMessageHoldMilliseconds,
            },
          }
        : {}),
    }))
  }, [])

  return { ...timeline, refresh, showOlder, showSent }
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

/** Reuse `idempotencyKey` for every retry of the same send-again attempt. */
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

/** Stored attachment bytes. Rejects only when `signal` aborts. */
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

/** Uploads one draft file; the caller has already checked its type and size. */
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

/**
 * Queues one staff Message. An existing thread addresses the conversation;
 * otherwise `destination` starts one. Reuse `idempotencyKey` for every retry of
 * the same draft.
 */
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
