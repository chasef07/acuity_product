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
  AgentCallVerdict,
} from "@/lib/api/generated/types.gen"
import { flagAgentCallIssue, useAgentCall } from "@/lib/clients/agent-calls"
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

export const issueReasons: Record<AgentCallIssueReason, string> = {
  WRONG_APPOINTMENT_TYPE: "Wrong Appointment Type",
  INSURANCE_ISSUE: "Insurance Issue",
  OTHER: "Other",
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
  const [saveError, setSaveError] = useState("")
  const detail =
    call.status === "ready"
      ? { ...call.data, issue: call.data.issue ?? flagged }
      : undefined
  const error =
    call.status === "failed" ? "This call could not be loaded. Try again." : ""

  async function flagIssue() {
    if (!reason || saving) return
    onSavingChange(true)
    setSaveError("")
    const outcome = await flagAgentCallIssue(id, reason)
    onSavingChange(false)
    if (!outcome.ok && outcome.failure.kind !== "conflict") {
      setSaveError("Your issue could not be saved. Try again.")
      return
    }
    if (outcome.ok) setFlagged(outcome.data)
    else call.retry()
    setReporting(false)
    onFlagged(id)
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
            <CallScorecard scorecard={detail.scorecard} sentiment={detail.sentiment} />
            {detail.issue ? (
              <Alert role="status">
                <FlagIcon aria-hidden="true" />
                <AlertTitle>Issue flagged</AlertTitle>
                <AlertDescription>
                  <p>{issueReasons[detail.issue.reason]}</p>
                  <p>
                    Saved for Acuity review ·{" "}
                    {new Date(detail.issue.createdAt).toLocaleString()}
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
                    <p
                      id="agent-issue-help"
                      className="text-xs text-muted-foreground"
                    >
                      This call and the reason will be saved for Acuity review.
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
            {detail.messages.length ? (
              detail.messages.map((message, index) => {
                const align = message.speaker === "Agent" ? "start" : "end"
                return (
                  <Message key={index} align={align}>
                    <MessageContent>
                      <MessageHeader>{message.speaker}</MessageHeader>
                      <Bubble
                        align={align}
                        variant={align === "start" ? "muted" : "outline"}
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
                )
              })
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

const sentimentLabels = ["Very negative", "Negative", "Neutral or mixed", "Positive", "Very positive"]

export function CallScorecard({ scorecard, sentiment }: { scorecard: AgentCallVerdict[]; sentiment?: number }) {
  if (scorecard.length === 0 && sentiment === undefined) return null
  return (
    <section aria-label="Call scorecard" className="rounded-lg border">
      <h3 className="border-b px-3 py-2 text-sm font-medium">Scorecard</h3>
      <ul className="divide-y text-sm">
        {scorecard.map((item) => (
          <li key={item.code} className="flex items-start justify-between gap-3 px-3 py-2">
            <span>{item.prompt}</span>
            <span className={item.answer === "no" ? "font-medium" : "text-muted-foreground"}>{item.answer === "no" ? "No" : item.answer === "unsure" ? "Unsure" : "Yes"}</span>
          </li>
        ))}
        {sentiment !== undefined && (
          <li className="flex items-start justify-between gap-3 px-3 py-2">
            <span>Caller sentiment</span>
            <span className="text-muted-foreground">{sentimentLabels[Math.round(sentiment)]}</span>
          </li>
        )}
      </ul>
    </section>
  )
}
