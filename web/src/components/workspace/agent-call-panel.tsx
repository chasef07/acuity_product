"use client"

import { useState } from "react"
import { ChevronLeftIcon, ChevronRightIcon, FlagIcon } from "lucide-react"
import { Button } from "@/components/ui/button"
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetDescription,
} from "@/components/ui/sheet"
import type {
  AgentCall,
  AgentCallIssue,
  AgentCallIssueReason,
  AgentCallMessage,
} from "@/lib/api/generated/types.gen"
import { flagAgentCallIssue, useAgentCall } from "@/lib/clients/agent-calls"
import { formatClockTime, formatTimestamp } from "@/lib/format-time"
import { appointmentActionLabels, formatClock, formatPhone, issueReasons } from "@/lib/vocabulary"
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
import { Textarea } from "@/components/ui/textarea"
import {
  Message,
  MessageContent,
  MessageHeader,
  MessageFooter,
} from "@/components/ui/message"
import { Bubble, BubbleContent } from "@/components/ui/bubble"
import { Badge } from "@/components/ui/badge"
import { CallFlow } from "./scorecard-flow"

export function AppointmentActions({ call }: { call: AgentCall }) {
  return call.appointmentActions.length ? (
    <span className="flex flex-wrap gap-1">
      {call.appointmentActions.map((action) => (
        <Badge key={action} variant="secondary">
          {appointmentActionLabels[action]}
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
  const [saving, setSaving] = useState(false)
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
              saving={saving}
              onSavingChange={setSaving}
              onFlagged={onFlagged}
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
  saving,
  onSavingChange,
}: {
  id: string
  onFlagged: (id: string) => void
  saving: boolean
  onSavingChange: (saving: boolean) => void
}) {
  const call = useAgentCall(id)
  const [flagged, setFlagged] = useState<AgentCallIssue>()
  const [reporting, setReporting] = useState(false)
  const [reason, setReason] = useState<AgentCallIssueReason | null>(null)
  const [note, setNote] = useState("")
  const [saveError, setSaveError] = useState("")
  const detail =
    call.status === "ready"
      ? { ...call.data, issue: call.data.issue ?? flagged }
      : undefined
  const refreshFailed = call.status === "ready" && Boolean(call.refreshFailure)

  async function flagIssue() {
    if (!reason || saving) return
    onSavingChange(true)
    setSaveError("")
    const outcome = await flagAgentCallIssue(id, reason, note)
    if (!outcome.ok && outcome.failure.kind !== "conflict") {
      onSavingChange(false)
      setSaveError("Your issue could not be saved. Try again.")
      return
    }
    if (outcome.ok) setFlagged(outcome.data)
    else await call.retry()
    onSavingChange(false)
    setReporting(false)
    onFlagged(id)
  }

  return (
    <>
      <SheetHeader className="shrink-0 border-b p-6">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <SheetTitle>
            {detail ? formatPhone(detail.call.phone) : "Call transcript"}
          </SheetTitle>
          {detail && !detail.issue && !refreshFailed && (
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
            ? `${formatTimestamp(detail.call.startedAt)} · ${detail.locationName}`
            : "Conversation with your agent"}
        </SheetDescription>
        {detail && (
          <div className="mt-2 flex flex-wrap items-center gap-3 text-sm">
            <AppointmentActions call={detail.call} />
            <span className="text-muted-foreground">
              {formatClock(detail.call.durationSeconds)}
            </span>
            <span className="text-muted-foreground">
              Transferred: {detail.call.transferred ? "Yes" : "No"}
            </span>
          </div>
        )}
      </SheetHeader>
      {call.status === "failed" ? (
        <div className="p-6">
          <Alert variant="destructive">
            <AlertTitle>Call unavailable</AlertTitle>
            <AlertDescription>This call could not be loaded. Try again.</AlertDescription>
            <Button
              variant="outline"
              className="mt-3 w-fit"
              onClick={call.retry}
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
            {refreshFailed && (
              <Alert variant="destructive">
                <AlertTitle>Call could not refresh</AlertTitle>
                <AlertDescription>
                  This is the call as last loaded.
                </AlertDescription>
                <Button
                  variant="outline"
                  className="mt-3 w-fit"
                  onClick={call.retry}
                >
                  Try again
                </Button>
              </Alert>
            )}
            <section aria-label="Call flow" className="@container text-xs">
              <CallFlow call={detail.call} result={detail.checkResult} />
            </section>
            {detail.issue ? (
              <Alert role="status">
                <FlagIcon aria-hidden="true" />
                <AlertTitle>Issue flagged</AlertTitle>
                <AlertDescription>
                  <p>{issueReasons[detail.issue.reason]}</p>
                  {detail.issue.note && (
                    <p className="whitespace-pre-wrap">{detail.issue.note}</p>
                  )}
                  <p>
                    Saved for Acuity review ·{" "}
                    {formatTimestamp(detail.issue.createdAt)}
                  </p>
                </AlertDescription>
              </Alert>
            ) : (
              reporting && (
                <form
                  onSubmit={(event) => {
                    event.preventDefault()
                    void flagIssue()
                  }}
                >
                  <FieldGroup>
                    <span
                      id="agent-issue-reason-label"
                      className="text-sm font-medium"
                    >
                      What went wrong?
                    </span>
                    <Select
                      value={reason}
                      items={issueReasons}
                      onValueChange={setReason}
                      disabled={saving}
                      required
                    >
                      <SelectTrigger
                        aria-labelledby="agent-issue-reason-label"
                        aria-describedby={
                          saveError
                            ? "agent-issue-help agent-issue-error"
                            : "agent-issue-help"
                        }
                        className="w-full sm:w-64"
                      >
                        <SelectValue placeholder="Select a reason" />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectGroup>
                          {Object.entries(issueReasons).map(([value, label]) => (
                            <SelectItem key={value} value={value}>
                              {label}
                            </SelectItem>
                          ))}
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                    <label
                      htmlFor="agent-issue-note"
                      className="text-sm font-medium"
                    >
                      What happened? (optional)
                    </label>
                    <Textarea
                      id="agent-issue-note"
                      value={note}
                      onChange={(event) => setNote(event.target.value)}
                      maxLength={2000}
                      disabled={saving}
                      placeholder="For example, the caller asked for a contact lens fitting but was booked for a routine exam."
                      className="max-h-40"
                    />
                    <p
                      id="agent-issue-help"
                      className="text-xs text-muted-foreground"
                    >
                      This call, the reason, and your note will be saved for Acuity review.
                    </p>
                    {saveError && (
                      <FieldError id="agent-issue-error">
                        {saveError}
                      </FieldError>
                    )}
                    <div className="flex gap-2">
                      <Button type="submit" disabled={saving || !reason}>
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
            <CallTranscript messages={detail.messages} />
          </div>
        </div>
      )}
    </>
  )
}

export function CallTranscript({ messages }: { messages: AgentCallMessage[] }) {
  if (!messages.length) {
    return (
      <Empty>
        <EmptyHeader>
          <EmptyTitle>No transcript available</EmptyTitle>
          <EmptyDescription>
            No conversation was recorded for this call.
          </EmptyDescription>
        </EmptyHeader>
      </Empty>
    )
  }
  return messages.map((message, index) => {
    const agent = message.speaker === "Agent"
    const align = agent ? "end" : "start"
    return (
      <Message key={index} align={align}>
        <MessageContent>
          <MessageHeader>{message.speaker}</MessageHeader>
          <Bubble align={align} variant={agent ? "muted" : "outline"}>
            <BubbleContent className="whitespace-pre-wrap">
              {message.text}
            </BubbleContent>
          </Bubble>
          <MessageFooter>
            <time dateTime={message.occurredAt}>
              {formatClockTime(message.occurredAt)}
            </time>
          </MessageFooter>
        </MessageContent>
      </Message>
    )
  })
}
