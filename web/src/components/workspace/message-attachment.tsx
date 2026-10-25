"use client"

import { useCallback, useEffect, useState } from "react"
import {
  DownloadIcon,
  FileTextIcon,
  ImageIcon,
  RefreshCwIcon,
} from "lucide-react"

import {
  Attachment,
  AttachmentAction,
  AttachmentActions,
  AttachmentContent,
  AttachmentDescription,
  AttachmentMedia,
  AttachmentTitle,
} from "@/components/ui/attachment"
import { Button } from "@/components/ui/button"
import { Sheet, SheetTrigger, SheetContent, SheetHeader, SheetTitle, SheetDescription } from "@/components/ui/sheet"
import { Spinner } from "@/components/ui/spinner"
import type { MessageAttachment } from "@/lib/api/generated/types.gen"
import {
  readMessageAttachment,
  retryMessageAttachment,
} from "@/lib/clients/engagement"

export function MessageAttachmentView({
  attachment,
  onChanged,
}: {
  attachment: MessageAttachment
  onChanged: () => void
}) {
  const [objectURL, setObjectURL] = useState("")
  const [previewAttempt, setPreviewAttempt] = useState(0)
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

  const loadBlob = useCallback(async (signal?: AbortSignal) => {
    if (attachment.state !== "Stored") return
    const outcome = await readMessageAttachment({ id: attachment.id }, signal)
    if (!outcome.ok) {
      setError(
        outcome.failure.kind === "signedOut" ||
          outcome.failure.kind === "unauthenticated"
          ? "Sign in again to load the attachment."
          : "Attachment unavailable.",
      )
      return
    }
    return outcome.data
  }, [attachment.id, attachment.state])

  useEffect(() => {
    if (isPDF) return
    const request = new AbortController()
    let previewURL = ""
    const timeout = window.setTimeout(() => {
      setPending(true)
      loadBlob(request.signal).then(
        (blob) => {
          if (request.signal.aborted) return
          setPending(false)
          if (!blob) return
          previewURL = URL.createObjectURL(blob)
          setObjectURL(previewURL)
        },
        () => undefined,
      )
    }, 0)
    return () => {
      request.abort()
      window.clearTimeout(timeout)
      if (previewURL) URL.revokeObjectURL(previewURL)
    }
  }, [isPDF, loadBlob, previewAttempt])

  async function retry() {
    setError("")
    if (attachment.state === "Stored") {
      setObjectURL("")
      setPreviewAttempt((attempt) => attempt + 1)
      return
    }
    setPending(true)
    const outcome = await retryMessageAttachment(attachment)
    setPending(false)
    if (!outcome.ok) {
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
    <Sheet>
      <Attachment
        state={presentationState}
        size="sm"
        className="mt-2 w-64 max-w-full"
      >
        <AttachmentMedia variant={objectURL && !isPDF ? "image" : "icon"}>
          {objectURL && !isPDF ? (
            <SheetTrigger
              type="button"
              aria-label={`Preview ${attachment.fileName}`}
              className="size-full cursor-zoom-in rounded-sm focus-visible:outline-2 focus-visible:outline-ring"
            >
              {/* eslint-disable-next-line @next/next/no-img-element -- private object URL */}
              <img src={objectURL} alt={attachment.fileName} className="size-full object-cover" />
            </SheetTrigger>
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
          {(attachment.state === "Attachment unavailable" || (attachment.state === "Stored" && !isPDF && !objectURL)) && (
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
      <SheetContent className="data-[side=right]:w-full data-[side=right]:sm:max-w-xl">
        <SheetHeader className="shrink-0 border-b pr-14">
          <SheetTitle className="break-words">{attachment.fileName}</SheetTitle>
          <SheetDescription>Image · {formatBytes(attachment.byteSize)}</SheetDescription>
        </SheetHeader>
        <div className="flex min-h-0 flex-1 items-center justify-center overflow-auto p-6">
          {objectURL && (
            // eslint-disable-next-line @next/next/no-img-element -- private object URL
            <img src={objectURL} alt={attachment.fileName} className="max-h-full max-w-full object-contain" />
          )}
        </div>
        <div className="shrink-0 border-t p-4">
          <Button variant="outline" onClick={() => void download()} disabled={pending}>
            {pending ? <Spinner /> : <DownloadIcon />} Download
          </Button>
          {error && <p role="alert" className="mt-2 text-sm text-destructive">{error}</p>}
        </div>
      </SheetContent>
    </Sheet>
  )
}

export function formatBytes(bytes: number) {
  if (bytes < 1_024) return `${bytes} B`
  return `${Math.round(bytes / 1_024)} KB`
}
