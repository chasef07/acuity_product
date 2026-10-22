"use client"

import {
  type ChangeEvent,
  type FormEvent,
  useEffect,
  useRef,
  useState,
} from "react"
import { ArrowUpIcon, FileTextIcon, PaperclipIcon, XIcon } from "lucide-react"

import {
  Attachment,
  AttachmentAction,
  AttachmentActions,
  AttachmentContent,
  AttachmentDescription,
  AttachmentMedia,
  AttachmentTitle,
} from "@/components/ui/attachment"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupTextarea,
} from "@/components/ui/input-group"
import { Spinner } from "@/components/ui/spinner"
import type { Message } from "@/lib/api/generated/types.gen"
import { sendMessage, uploadMessageAttachment } from "@/lib/clients/engagement"
import { messageAttachmentTypes } from "@/lib/vocabulary"
import { cn } from "@/lib/utils"

import { formatBytes } from "./message-attachment"

const maximumMessageLength = 1_600

const maximumAttachmentBytes = 600 * 1_024

const acceptedAttachmentTypes = new Set(messageAttachmentTypes)

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

export function MessageComposer({
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
    let attachmentID = attempt.attachmentID
    if (file && !attachmentID) {
      const upload = await uploadMessageAttachment({ practiceID, locationID, file })
      if (!upload.ok) {
        setPending(false)
        setError("The attachment could not be prepared.")
        return
      }
      attachmentID = upload.data.id
      attempt.attachmentID = attachmentID
    }
    const outcome = await sendMessage({
      practiceID,
      locationID,
      threadID,
      destination: destination.trim(),
      body: body.trim(),
      attachmentID,
      idempotencyKey: attempt.idempotencyKey,
    })
    setPending(false)
    if (!outcome.ok) {
      setError(
        outcome.failure.kind === "conflict"
          ? "This destination cannot be messaged from the selected office."
          : "The message was not queued. Nothing was sent.",
      )
      return
    }
    setBody("")
    setFile(undefined)
    draftAttempt.current = undefined
    onSent(outcome.data.message)
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
          className={cn("h-auto min-h-16 flex-col justify-center bg-card px-1.5 shadow-sm", file && "rounded-2xl")}
        >
          {file && (
            <div className="w-full px-3 pt-3">
              <DraftAttachment
                key={`${file.name}:${file.size}:${file.lastModified}`}
                file={file}
                pending={pending}
                onRemove={() => {
                  draftAttempt.current = undefined
                  setFile(undefined)
                }}
              />
            </div>
          )}
          <div className="flex w-full min-w-0 items-center">
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
          </div>
        </InputGroup>
        <input
          ref={fileInput}
          type="file"
          className="sr-only"
          accept={[...acceptedAttachmentTypes].join(",")}
          disabled={disabled || pending}
          onChange={chooseFile}
        />
        {body.length >= 1_400 && (
          <p className="mt-1.5 text-right text-xs tabular-nums text-muted-foreground">
            {body.length}/{maximumMessageLength}
          </p>
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
