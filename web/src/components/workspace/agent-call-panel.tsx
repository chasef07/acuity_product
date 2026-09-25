"use client"

import { useEffect, useState } from "react"
import { ChevronLeftIcon, ChevronRightIcon, FlagIcon } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Textarea } from "@/components/ui/textarea"
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetDescription,
} from "@/components/ui/sheet"
import { portalClient } from "@/lib/api/client"
import { flagAgentCallIssue, getAgentCall } from "@/lib/api/generated/sdk.gen"
import type { AgentCall, AgentCallDetail } from "@/lib/api/generated/types.gen"
import { getAccessToken } from "@/lib/auth-client"
import { formatUSPhone } from "@/lib/phone"
import { Alert, AlertTitle, AlertDescription } from "@/components/ui/alert"
import {
  Empty,
  EmptyHeader,
  EmptyTitle,
  EmptyDescription,
} from "@/components/ui/empty"
import { FieldGroup, FieldError } from "@/components/ui/field"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import {
  Message,
  MessageContent,
  MessageHeader,
  MessageFooter,
} from "@/components/ui/message"
import { Bubble, BubbleContent } from "@/components/ui/bubble"
import { Badge } from "@/components/ui/badge"

const actions = {
  BOOKED: "Booked",
  RESCHEDULED: "Rescheduled",
  CANCELLED: "Cancelled",
}

export function AppointmentActions({ call }: { call: AgentCall }) {
  return call.appointmentActions.length ? (
    <span className="flex flex-wrap gap-1">
      {call.appointmentActions.map((action) => (
        <Badge key={action} variant="secondary">
          {actions[action]}
        </Badge>
      ))}
    </span>
  ) : (
    <span
      aria-label="No confirmed appointment action"
      className="text-muted-foreground"
    >
      —
    </span>
  )
}

export function callDuration(seconds?: number) {
  if (seconds === undefined) return "—"
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`
}

export function AgentCallPanel({
  id,
  onClose,
  onFlagged,
  position,
  loadedCount,
  hasMore,
  loadingNext,
  navigationError,
  onPrevious,
  onNext,
}: {
  position: number
  loadedCount: number
  hasMore: boolean
  loadingNext: boolean
  navigationError?: string
  onPrevious?: () => void
  onNext?: () => void
  id: string
  onClose: () => void
  onFlagged: (id: string) => void
}) {
  const [drafts, setDrafts] = useState<Record<string, string>>({})
  const [saving, setSaving] = useState(false)
  function updateDraft(callID: string, note: string) {
    setDrafts((previous) => {
      const next = { ...previous }
      if (note) next[callID] = note
      else delete next[callID]
      return next
    })
  }
  return (
    <Sheet
      open={Boolean(id)}
      onOpenChange={(open) => !open && !saving && onClose()}
    >
      <SheetContent className="flex h-full flex-col gap-0 overflow-hidden p-0 data-[side=right]:w-full data-[side=right]:sm:max-w-2xl">
        {id && (
          <>
            <nav
              aria-label="Call navigation"
              className="shrink-0 border-b px-6 py-3 pr-12"
            >
              <div className="flex items-center gap-2">
                <Button
                  variant="outline"
                  size="icon"
                  aria-label="Previous call"
                  disabled={!onPrevious || loadingNext || saving}
                  onClick={onPrevious}
                >
                  <ChevronLeftIcon />
                </Button>
                <span
                  aria-live="polite"
                  className="min-w-0 text-sm tabular-nums text-muted-foreground"
                >
                  {loadingNext
                    ? "Loading next call…"
                    : position > 0
                      ? `Call ${position} of ${loadedCount}${hasMore ? "+" : ""}`
                      : "Call details"}
                </span>
                <Button
                  variant="outline"
                  size="icon"
                  aria-label="Next call"
                  disabled={!onNext || loadingNext || saving}
                  onClick={onNext}
                >
                  <ChevronRightIcon />
                </Button>
              </div>
              {navigationError && (
                <Alert variant="destructive" className="mt-2">
                  <AlertTitle>Next call unavailable</AlertTitle>
                  <AlertDescription>
                    {navigationError} Select Next call to retry.
                  </AlertDescription>
                </Alert>
              )}
            </nav>
            <CallContent
              key={id}
              id={id}
              note={drafts[id] ?? ""}
              onNoteChange={(note) => updateDraft(id, note)}
              saving={saving}
              onSavingChange={setSaving}
              onFlagged={(callID) => {
                updateDraft(callID, "")
                onFlagged(callID)
              }}
            />
          </>
        )}
      </SheetContent>
    </Sheet>
  )
}

function CallContent({
  id,
  onFlagged,
  note,
  onNoteChange,
  saving,
  onSavingChange,
}: {
  id: string
  onFlagged: (id: string) => void
  note: string
  onNoteChange: (note: string) => void
  saving: boolean
  onSavingChange: (saving: boolean) => void
}) {
  const [detail, setDetail] = useState<AgentCallDetail>()
  const [error, setError] = useState("")
  const [version, setVersion] = useState(0)
  const [reporting, setReporting] = useState(Boolean(note))
  const [saveError, setSaveError] = useState("")
  useEffect(() => {
    const controller = new AbortController()
    async function load() {
      try {
        const token = await getAccessToken()
        if (!token) throw new Error()
        const result = await getAgentCall({
          client: portalClient(token),
          signal: controller.signal,
          path: { interactionId: id },
        })
        if (!result.data) throw new Error()
        if (!controller.signal.aborted) {
          setDetail(result.data)
          setError("")
        }
      } catch {
        if (!controller.signal.aborted)
          setError("This call could not be loaded. Try again.")
      }
    }
    void load()
    return () => controller.abort()
  }, [id, version])

  async function flagIssue() {
    if (!note.trim() || saving) return
    onSavingChange(true)
    setSaveError("")
    try {
      const token = await getAccessToken()
      if (!token) throw new Error()
      const result = await flagAgentCallIssue({
        client: portalClient(token),
        path: { interactionId: id },
        body: { note: note.trim() },
      })
      if (result.response?.status === 409) {
        setSaveError(
          "This call already has a report. Your note has not been saved; copy it before closing.",
        )
        return
      }
      if (!result.data) throw new Error()
      const issue = result.data
      setDetail((previous) => (previous ? { ...previous, issue } : previous))
      setReporting(false)
      onFlagged(id)
    } catch {
      setSaveError(
        "Your issue could not be saved. Your note is still here; try again.",
      )
    } finally {
      onSavingChange(false)
    }
  }

  return (
    <>
      <SheetHeader className="shrink-0 border-b p-6">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <SheetTitle>
            {detail ? formatUSPhone(detail.call.phone) : "Call transcript"}
          </SheetTitle>
          {detail && !detail.issue && (
            <Button
              variant="outline"
              size="sm"
              disabled={reporting}
              onClick={() => setReporting(true)}
            >
              <FlagIcon data-icon="inline-start" />
              Flag an issue
            </Button>
          )}
        </div>
        <SheetDescription>
          {detail
            ? `${new Date(detail.call.startedAt).toLocaleString()} · ${detail.locationName}`
            : "Conversation with your agent"}
        </SheetDescription>
        {detail && (
          <div className="mt-2 flex flex-wrap items-center gap-3 text-sm">
            <AppointmentActions call={detail.call} />
            <span className="text-muted-foreground">
              {callDuration(detail.call.durationSeconds)}
            </span>
            <span className="text-muted-foreground">
              Transferred: {detail.call.transferred ? "Yes" : "No"}
            </span>
          </div>
        )}
      </SheetHeader>
      {error ? (
        <div className="p-6">
          <Alert variant="destructive">
            <AlertTitle>Call unavailable</AlertTitle>
            <AlertDescription>{error}</AlertDescription>
            <Button
              variant="outline"
              className="mt-3 w-fit"
              onClick={() => setVersion((value) => value + 1)}
            >
              Try again
            </Button>
          </Alert>
        </div>
      ) : !detail ? (
        <div
          role="status"
          aria-label="Loading transcript"
          className="flex flex-col gap-6 p-6"
        >
          <span className="sr-only">Loading transcript…</span>
          <Skeleton className="h-16 w-3/4" />
          <Skeleton className="h-20 w-full" />
          <Skeleton className="h-16 w-2/3" />
        </div>
      ) : (
        <div
          aria-label="Call transcript"
          className="min-h-0 flex-1 overflow-y-auto overscroll-contain p-6 scrollbar-thin"
        >
          <div className="flex flex-col gap-6">
            {detail.issue ? (
              <div>
                <Alert role="status">
                  <FlagIcon aria-hidden="true" />
                  <AlertTitle>Issue flagged</AlertTitle>
                  <AlertDescription>
                    <p className="whitespace-pre-wrap break-words">
                      {detail.issue.note}
                    </p>
                    <p>
                      Saved for Acuity review ·{" "}
                      {new Date(detail.issue.createdAt).toLocaleString()}
                    </p>
                  </AlertDescription>
                </Alert>
                {note && (
                  <FieldGroup className="mt-4">
                    <label htmlFor="agent-unsaved-note" className="text-sm font-medium">
                      Your unsaved note
                    </label>
                    <Textarea id="agent-unsaved-note" value={note} readOnly aria-describedby="agent-unsaved-note-help" />
                    <p id="agent-unsaved-note-help" className="text-xs text-muted-foreground">
                      A report was saved while you were writing. Your note was not submitted; you can select and copy it.
                    </p>
                    <Button type="button" variant="ghost" className="w-fit" onClick={() => onNoteChange("")}>
                      Discard unsaved note
                    </Button>
                  </FieldGroup>
                )}
              </div>
            ) : (
              reporting && (
                <form
                  onSubmit={(event) => {
                    event.preventDefault()
                    void flagIssue()
                  }}
                >
                  <FieldGroup>
                    <label
                      htmlFor="agent-issue-note"
                      className="text-sm font-medium"
                    >
                      What went wrong?
                    </label>
                    <Textarea
                      id="agent-issue-note"
                      autoFocus
                      className="min-h-24"
                      value={note}
                      onChange={(event) => onNoteChange(event.target.value)}
                      maxLength={2000}
                      required
                      disabled={saving}
                      aria-describedby={
                        saveError
                          ? "agent-issue-help agent-issue-error"
                          : "agent-issue-help"
                      }
                    />
                    <p
                      id="agent-issue-help"
                      className="text-xs text-muted-foreground"
                    >
                      This call and your note will be saved for Acuity review.
                    </p>
                    {saveError && (
                      <FieldError id="agent-issue-error">
                        {saveError}
                      </FieldError>
                    )}
                    <div className="flex gap-2">
                      <Button type="submit" disabled={saving || !note.trim()}>
                        {saving && <Spinner data-icon="inline-start" />}
                        {saving ? "Saving…" : "Flag issue"}
                      </Button>
                      <Button
                        type="button"
                        variant="ghost"
                        disabled={saving}
                        onClick={() => setReporting(false)}
                      >
                        Cancel
                      </Button>
                    </div>
                  </FieldGroup>
                </form>
              )
            )}
            <h2 className="text-sm font-medium">Transcript</h2>
            {detail.messages.length ? (
              detail.messages.map((message, index) => (
                <Message key={index}>
                  <MessageContent>
                    <MessageHeader>{message.speaker}</MessageHeader>
                    <Bubble
                      variant={
                        message.speaker === "Agent" ? "muted" : "ghost"
                      }
                    >
                      <BubbleContent className="whitespace-pre-wrap">
                        {message.text}
                      </BubbleContent>
                    </Bubble>
                    <MessageFooter>
                      <time dateTime={message.occurredAt}>
                        {new Date(message.occurredAt).toLocaleTimeString(
                          undefined,
                          {
                            hour: "numeric",
                            minute: "2-digit",
                            second: "2-digit",
                          },
                        )}
                      </time>
                    </MessageFooter>
                  </MessageContent>
                </Message>
              ))
            ) : (
              <Empty>
                <EmptyHeader>
                  <EmptyTitle>No transcript available</EmptyTitle>
                  <EmptyDescription>
                    No conversation was recorded for this call.
                  </EmptyDescription>
                </EmptyHeader>
              </Empty>
            )}
          </div>
        </div>
      )}
    </>
  )
}
