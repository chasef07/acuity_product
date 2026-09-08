"use client"

import { useEffect, useState } from "react"
import {
  CalendarCheck2Icon,
  ChevronRightIcon,
  CircleAlertIcon,
  PhoneForwardedIcon,
} from "lucide-react"

import {
  latencyLabel,
  type DiagnosticFocus,
} from "@/components/analytics/ai-diagnostics"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Bubble, BubbleContent } from "@/components/ui/bubble"
import { Button } from "@/components/ui/button"
import {
  Message,
  MessageContent,
  MessageHeader,
  MessageFooter,
} from "@/components/ui/message"
import {
  MessageScrollerProvider,
  MessageScroller,
  MessageScrollerViewport,
  MessageScrollerContent,
  MessageScrollerItem,
} from "@/components/ui/message-scroller"
import { Badge } from "@/components/ui/badge"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import { Spinner } from "@/components/ui/spinner"
import { appointmentOutcomeTitle } from "@/lib/ai-interactions"
import { portalClient } from "@/lib/api/client"
import { getOperatorAiInteractionAnalytics } from "@/lib/api/generated/sdk.gen"
import type {
  AiAppointmentFacts,
  OperatorAiInteractionAnalytics,
  OperatorAiTimelineItem,
} from "@/lib/api/generated/types.gen"
import { getAccessToken } from "@/lib/auth-client"

export function OperatorAnalyticsDetailSheet({
  interactionID,
  focus,
  onClose,
}: {
  interactionID: string
  focus?: DiagnosticFocus
  onClose: () => void
}) {
  const [request, setRequest] = useState<{
    interactionID: string
    detail?: OperatorAiInteractionAnalytics
    error?: string
  }>({ interactionID: "" })
  const currentRequest =
    request.interactionID === interactionID ? request : undefined
  const detail = currentRequest?.detail
  const error = currentRequest?.error ?? ""
  const loading = Boolean(interactionID) && !currentRequest

  useEffect(() => {
    if (!interactionID) return
    const controller = new AbortController()
    void getAccessToken().then(async (token) => {
      if (controller.signal.aborted) return
      if (!token) {
        setRequest({
          interactionID,
          error: "Your session is not authorized to load this AI call.",
        })
        return
      }
      const result = await getOperatorAiInteractionAnalytics({
        client: portalClient(token),
        path: { interactionId: interactionID },
        signal: controller.signal,
      }).catch(() => undefined)
      if (controller.signal.aborted) return
      if (result?.data) {
        setRequest({ interactionID, detail: result.data })
        return
      }
      const unauthorized =
        result?.response?.status === 401 || result?.response?.status === 403
      setRequest({
        interactionID,
        error: unauthorized
          ? "Your session is not authorized to load this AI call."
          : "This AI call evidence is temporarily unavailable.",
      })
    })
    return () => controller.abort()
  }, [interactionID])

  return (
    <Sheet
      open={Boolean(interactionID)}
      onOpenChange={(open) => !open && onClose()}
    >
      <SheetContent className="h-full overflow-hidden p-0 data-[side=right]:w-full data-[side=right]:sm:max-w-2xl">
        <SheetHeader className="shrink-0 border-b px-5 py-4 pr-12 sm:px-6">
          <div className="flex flex-wrap items-center gap-2">
            <SheetTitle className="text-base">AI call evidence</SheetTitle>
            {detail && (
              <Badge variant="secondary">
                {detail.status === "ESCALATED" && (
                  <PhoneForwardedIcon aria-hidden="true" />
                )}
                {detail.status === "ESCALATED"
                  ? "Transferred"
                  : detail.status.replaceAll("_", " ").toLowerCase()}
              </Badge>
            )}
          </div>
          <SheetDescription className="sr-only">
            Transcript, timing, tool activity, and receipt-backed outcomes.
          </SheetDescription>
        </SheetHeader>

        {loading && (
          <div className="flex min-h-80 items-center justify-center gap-2 text-muted-foreground">
            <Spinner />
            Loading call evidence
          </div>
        )}
        {!loading && error && (
          <div className="p-5 sm:p-6">
            <Alert variant="destructive">
              <CircleAlertIcon aria-hidden="true" />
              <AlertTitle>Call evidence unavailable</AlertTitle>
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          </div>
        )}
        {!loading && detail && (
          <OperatorAnalyticsDetailView
            key={interactionID}
            detail={detail}
            focus={focus}
          />
        )}
      </SheetContent>
    </Sheet>
  )
}

function OperatorAnalyticsDetailView({
  detail,
  focus,
}: {
  detail: OperatorAiInteractionAnalytics
  focus?: DiagnosticFocus
}) {
  const [showTiming, setShowTiming] = useState(true)
  const messageCount = detail.timeline.filter(
    (item) => item.kind === "CALLER_MESSAGE" || item.kind === "AGENT_MESSAGE",
  ).length

  const entries = timelineEntries(detail.timeline)
  const focusedTimelineIndex = entries.findIndex(({ item }) =>
    Boolean(
      (focus?.itemID && item.itemId === focus.itemID) ||
        (focus?.callID && item.callId === focus.callID),
    ),
  )
  // Historical executions can exist without a transcript tool event.
  const focusedExecutionID =
    focusedTimelineIndex === -1
      ? detail.toolExecutions.find(
          (execution) => execution.callId === focus?.callID,
        )?.callId
      : undefined

  return (
    <>
      <section className="shrink-0 border-b px-5 py-4 sm:px-6">
        <p className="text-sm font-medium">{formatPhone(detail.phone)}</p>
        <p className="mt-1 text-sm text-muted-foreground">
          {detail.locationName}
        </p>
        <p className="mt-1 text-xs text-muted-foreground">
          {formatDateTime(detail.startedAt)} ·{" "}
          {formatDuration(detail.startedAt, detail.endedAt)}
        </p>
        {detail.summary ? (
          <p className="mt-3 text-sm leading-6">{detail.summary}</p>
        ) : (
          <p className="mt-3 text-xs text-muted-foreground">
            No call summary recorded.
          </p>
        )}
      </section>
      <dl
        aria-label="Median call timing"
        className="grid shrink-0 grid-cols-4 border-b px-2 sm:px-3"
      >
        <LatencyValue label="P50 STT" value={detail.p50SttMs} />
        <LatencyValue label="P50 TTFT" value={detail.p50TtftMs} />
        <LatencyValue label="P50 TTS" value={detail.p50TtsTtfbMs} />
        <LatencyValue label="P50 E2E" value={detail.p50TotalLatencyMs} />
      </dl>
      <MessageScrollerProvider
        autoScroll={false}
        defaultScrollPosition={focus ? "last-anchor" : "start"}
      >
        <MessageScroller className="min-h-0 flex-1">
          <MessageScrollerViewport aria-label="Scrollable call evidence">
            <MessageScrollerContent className="gap-0" aria-live="off">
              <MessageScrollerItem
                messageId="conversation"
                className="[content-visibility:visible]"
              >
                <section
                  aria-label="Call conversation"
                  className="border-b px-5 py-5 sm:px-6"
                >
                  <div className="mb-5 flex items-center justify-between gap-4">
                    <h2 id="timeline-title" className="text-sm font-semibold">
                      Conversation
                    </h2>
                    <Button
                      variant="ghost"
                      size="xs"
                      aria-pressed={showTiming}
                      onClick={() => setShowTiming((current) => !current)}
                    >
                      Turn timing
                    </Button>
                  </div>

                  {messageCount === 0 && (
                    <Alert className="mb-4">
                      <CircleAlertIcon aria-hidden="true" />
                      <AlertTitle>
                        Transcript unavailable or incomplete
                      </AlertTitle>
                      <AlertDescription>
                        No caller or agent turns were returned. Tool and
                        appointment evidence is shown where available.
                      </AlertDescription>
                    </Alert>
                  )}

                  {detail.timeline.length > 0 ? (
                    <div role="list" className="flex flex-col gap-4">
                      {entries.map(({ item, result }, index) => (
                        <TimelineItem
                          key={`${item.occurredAt}-${item.kind}-${item.callId ?? index}`}
                          item={item}
                          result={result}
                          showTiming={showTiming}
                          selected={index === focusedTimelineIndex}
                        />
                      ))}
                    </div>
                  ) : (
                    <p className="rounded-lg border border-dashed px-4 py-8 text-center text-sm text-muted-foreground">
                      No transcript or tool timeline was recorded for this call.
                    </p>
                  )}
                </section>
              </MessageScrollerItem>
              <MessageScrollerItem messageId="appointments">
                <AppointmentEvidence detail={detail} />
              </MessageScrollerItem>
              <MessageScrollerItem
                messageId="technical-evidence"
                className="[content-visibility:visible]"
              >
                <section className="px-5 py-5 sm:px-6">
                  <details
                    open={Boolean(focusedExecutionID) || undefined}
                    className="group rounded-lg border px-4 py-3"
                  >
                    <summary className="cursor-pointer text-sm font-medium focus-visible:rounded-sm focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-ring">
                      Technical evidence
                    </summary>
                    <div className="mt-4 space-y-4">
                      <dl className="grid gap-3 text-xs sm:grid-cols-3">
                        <EvidenceValue label="Interaction" value={detail.id} />
                        <EvidenceValue
                          label="Source call"
                          value={detail.sourceCallId}
                        />
                        <EvidenceValue
                          label="Location"
                          value={detail.locationId}
                        />
                        {detail.externalPatientId && (
                          <EvidenceValue
                            label="External patient"
                            value={detail.externalPatientId}
                          />
                        )}
                      </dl>
                      {detail.toolExecutions.length > 0 && (
                        <div>
                          <p className="mb-2 text-xs font-medium text-muted-foreground">
                            Sanitized tool executions
                          </p>
                          <div className="space-y-2">
                            {detail.toolExecutions.map((execution) => (
                              <MessageScrollerItem
                                key={execution.callId}
                                messageId={`execution-${execution.callId}`}
                                scrollAnchor={
                                  execution.callId === focusedExecutionID
                                }
                                data-diagnostic-selected={
                                  execution.callId === focusedExecutionID ||
                                  undefined
                                }
                                className="rounded-md bg-muted px-3 py-2 data-[diagnostic-selected]:ring-2 data-[diagnostic-selected]:ring-ring data-[diagnostic-selected]:ring-offset-4"
                              >
                                <div className="flex flex-wrap items-center gap-2">
                                  <span className="font-mono text-xs font-medium">
                                    {execution.name}
                                  </span>
                                  <Badge
                                    variant={
                                      execution.status === "ERROR"
                                        ? "destructive"
                                        : execution.status === "INCOMPLETE"
                                          ? "outline"
                                          : "secondary"
                                    }
                                  >
                                    execution: {execution.status}
                                  </Badge>
                                  {(execution.domainOutcome ||
                                    execution.outputClass) && (
                                    <Badge variant="outline">
                                      outcome:{" "}
                                      {execution.domainOutcome ??
                                        execution.outputClass}
                                    </Badge>
                                  )}
                                  {execution.domainStatus && (
                                    <Badge variant="outline">
                                      domain: {execution.domainStatus}
                                    </Badge>
                                  )}
                                  {execution.taskId && (
                                    <Badge variant="outline">
                                      task: {execution.taskId}
                                    </Badge>
                                  )}
                                  <time className="ml-auto text-[0.625rem] tabular-nums text-muted-foreground">
                                    {formatTime(execution.occurredAt)}
                                  </time>
                                </div>
                                <p className="mt-1 truncate font-mono text-[0.6875rem] text-muted-foreground">
                                  {execution.callId}
                                </p>
                              </MessageScrollerItem>
                            ))}
                          </div>
                        </div>
                      )}
                    </div>
                  </details>
                </section>
              </MessageScrollerItem>
            </MessageScrollerContent>
          </MessageScrollerViewport>
        </MessageScroller>
      </MessageScrollerProvider>
    </>
  )
}

function AppointmentEvidence({
  detail,
}: {
  detail: OperatorAiInteractionAnalytics
}) {
  const receipts = [
    detail.bookingResult
      ? { label: "Booking receipt", value: detail.bookingResult }
      : undefined,
    detail.cancellationResult
      ? { label: "Cancellation receipt", value: detail.cancellationResult }
      : undefined,
  ].filter(
    (receipt): receipt is { label: string; value: Record<string, unknown> } =>
      Boolean(receipt),
  )
  const hasCurrentAppointment = hasAppointmentFacts(detail.appointment)
  const hasPreviousAppointment =
    detail.previousAppointment &&
    hasAppointmentFacts(detail.previousAppointment)

  if (
    !hasCurrentAppointment &&
    !hasPreviousAppointment &&
    receipts.length === 0 &&
    !detail.newAppointmentId &&
    !detail.oldAppointmentId &&
    detail.appointmentOutcome === "INDETERMINATE"
  )
    return null

  return (
    <section
      aria-labelledby="appointments-title"
      className="border-b px-5 py-5 sm:px-6"
    >
      <div className="mb-3 flex items-center gap-2">
        <CalendarCheck2Icon
          className="size-4 text-muted-foreground"
          aria-hidden="true"
        />
        <h2 id="appointments-title" className="text-sm font-semibold">
          Appointment and receipt evidence
        </h2>
      </div>
      <div className="grid gap-3 lg:grid-cols-2">
        <div className="rounded-lg border bg-card px-4 py-3">
          <div className="flex flex-wrap items-center gap-2">
            <p className="text-sm font-medium">
              {appointmentOutcomeTitle(detail.appointmentOutcome)}
            </p>
            <Badge variant={receipts.length > 0 ? "secondary" : "outline"}>
              {receipts.length > 0 ? "Receipt backed" : "No provider receipt"}
            </Badge>
          </div>
          {hasCurrentAppointment ? (
            <AppointmentFacts facts={detail.appointment} />
          ) : (
            <p className="mt-2 text-xs text-muted-foreground">
              No current appointment facts were recorded.
            </p>
          )}
          <EvidenceIdentifiers
            appointmentID={detail.newAppointmentId}
            previousAppointmentID={detail.oldAppointmentId}
          />
        </div>

        {hasPreviousAppointment && detail.previousAppointment && (
          <div className="rounded-lg border border-dashed bg-card px-4 py-3">
            <p className="text-sm font-medium">Previous appointment</p>
            <AppointmentFacts facts={detail.previousAppointment} />
          </div>
        )}

        {receipts.map((receipt) => (
          <details
            key={receipt.label}
            className="rounded-lg border bg-card px-4 py-3"
          >
            <summary className="cursor-pointer text-sm font-medium">
              {receipt.label}
            </summary>
            <pre className="mt-3 max-h-64 overflow-auto whitespace-pre-wrap rounded-md bg-muted p-3 font-mono text-[0.6875rem] leading-5">
              {formatPayload(receipt.value)}
            </pre>
          </details>
        ))}
      </div>
    </section>
  )
}

function AppointmentFacts({ facts }: { facts: AiAppointmentFacts }) {
  return (
    <dl className="mt-3 grid grid-cols-2 gap-x-4 gap-y-3 text-xs">
      <DetailValue
        label="When"
        value={formatAppointmentDateTime(facts) ?? "—"}
      />
      <DetailValue label="Patient" value={facts.patientName ?? "—"} />
      <DetailValue label="Visit" value={visitLabel(facts)} />
      <DetailValue label="Doctor" value={facts.providerName ?? "—"} />
      <DetailValue label="Office" value={facts.locationName ?? "—"} />
    </dl>
  )
}

function TimelineItem({
  item,
  result,
  selected,
  showTiming,
}: {
  item: OperatorAiTimelineItem
  result?: OperatorAiTimelineItem
  selected: boolean
  showTiming: boolean
}) {
  if (item.kind === "CALLER_MESSAGE" || item.kind === "AGENT_MESSAGE") {
    const agent = item.kind === "AGENT_MESSAGE"
    return (
      <MessageScrollerItem
        role="listitem"
        messageId={item.itemId ?? item.callId ?? item.occurredAt}
        scrollAnchor={selected}
        data-diagnostic-selected={selected || undefined}
        className="rounded-md data-[diagnostic-selected]:ring-2 data-[diagnostic-selected]:ring-ring data-[diagnostic-selected]:ring-offset-4"
      >
        <Message
          align={agent ? "start" : "end"}
          aria-label={`${agent ? "Agent" : "Caller"} message`}
        >
          <MessageContent>
            <MessageHeader>{agent ? "Acuity" : "Caller"}</MessageHeader>
            <Bubble
              align={agent ? "start" : "end"}
              variant={agent ? "muted" : "outline"}
            >
              <BubbleContent className="whitespace-pre-wrap">
                {item.text || "Message content unavailable"}
              </BubbleContent>
            </Bubble>
            {(showTiming || selected) && (
              <MessageFooter className="flex-wrap gap-x-3 gap-y-1">
                <time>{formatTime(item.occurredAt)}</time>
                {[
                  ["STT final", item.sttMs],
                  ["TTFT", item.ttftMs],
                  ["TTS", item.ttsTtfbMs],
                  ["Response", item.totalLatencyMs],
                ].map(
                  ([label, value]) =>
                    typeof value === "number" && (
                      <span key={String(label)}>
                        {label} {latencyLabel(value)}
                      </span>
                    ),
                )}
              </MessageFooter>
            )}
          </MessageContent>
        </Message>
      </MessageScrollerItem>
    )
  }

  const toolResult = result ?? (item.kind === "TOOL_RESULT" ? item : undefined)
  const toolCall = item.kind === "TOOL_CALL" ? item : undefined
  const failed = Boolean(toolResult?.error)
  return (
    <MessageScrollerItem
      role="listitem"
      messageId={item.itemId ?? item.callId ?? item.occurredAt}
      scrollAnchor={selected}
      data-diagnostic-selected={selected || undefined}
      className="rounded-md data-[diagnostic-selected]:ring-2 data-[diagnostic-selected]:ring-ring data-[diagnostic-selected]:ring-offset-4"
    >
      <details
        open={selected || undefined}
        className="group rounded-md bg-muted/50 px-3 py-2.5"
      >
        <summary className="cursor-pointer list-none focus-visible:rounded-sm focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-ring [&::-webkit-details-marker]:hidden">
          <div className="flex flex-wrap items-center gap-2">
            <ChevronRightIcon
              className="size-3.5 text-muted-foreground transition-transform group-open:rotate-90"
              aria-hidden="true"
            />
            <span className="text-xs font-semibold">
              {formatToolLabel(item.name)}
            </span>
            <Badge variant={failed ? "destructive" : "secondary"}>
              {failed ? "Error" : toolResult ? "Complete" : "Called"}
            </Badge>
            {(toolResult?.durationMs ?? item.durationMs) !== undefined && (
              <span className="ml-auto text-[0.625rem] tabular-nums text-muted-foreground">
                {formatLatency(toolResult?.durationMs ?? item.durationMs)}
              </span>
            )}
          </div>
        </summary>
        <div className="mt-3 space-y-3 border-t pt-3">
          <p className="text-xs text-muted-foreground">
            <code>{item.name ?? "unknown_tool"}</code> ·{" "}
            {formatTime(item.occurredAt)} ·{" "}
            {(toolResult?.durationMs ?? item.durationMs) !== undefined
              ? `${formatLatency(toolResult?.durationMs ?? item.durationMs)} execution`
              : "Execution timing unavailable"}
          </p>
          {toolCall && <ToolPayload label="Request" value={toolCall.payload} />}
          {toolResult && (
            <ToolPayload
              label={failed ? "Error" : "Response"}
              value={
                failed
                  ? { error: toolResult.error, payload: toolResult.payload }
                  : toolResult.payload
              }
              error={failed}
            />
          )}
          {!toolCall && !toolResult && (
            <p className="text-xs text-muted-foreground">
              No request or response payload was recorded.
            </p>
          )}
        </div>
      </details>
    </MessageScrollerItem>
  )
}

function ToolPayload({
  label,
  value,
  error = false,
}: {
  label: string
  value: unknown
  error?: boolean
}) {
  return (
    <div>
      <p className="mb-1.5 text-[0.6875rem] font-medium text-muted-foreground">
        {label}
      </p>
      <pre
        className={`max-h-72 overflow-auto whitespace-pre-wrap break-words rounded-md border p-3 font-mono text-[0.6875rem] leading-5 ${
          error
            ? "border-destructive/30 bg-destructive/5 text-destructive"
            : "bg-muted"
        }`}
      >
        {formatPayload(value)}
      </pre>
    </div>
  )
}

function timelineEntries(items: OperatorAiTimelineItem[]) {
  const entries: Array<{
    item: OperatorAiTimelineItem
    result?: OperatorAiTimelineItem
  }> = []
  const calls = new Map<string, number>()

  for (const item of items) {
    if (item.kind === "TOOL_CALL" && item.callId) {
      calls.set(item.callId, entries.length)
      entries.push({ item })
      continue
    }
    if (item.kind === "TOOL_RESULT" && item.callId) {
      const callIndex = calls.get(item.callId)
      if (callIndex !== undefined) {
        entries[callIndex] = { ...entries[callIndex], result: item }
        continue
      }
    }
    entries.push({ item })
  }

  return entries
}

function formatToolLabel(name?: string) {
  switch (name) {
    case "book_appt":
    case "book_appointment":
      return "Book appointment"
    case "cancel_appt":
    case "cancel_appointment":
      return "Cancel appointment"
    case "reschedule_appt":
    case "reschedule_appointment":
      return "Reschedule appointment"
    case "transfer_call":
      return "Transfer call"
    default:
      return name
        ? name
            .replaceAll("_", " ")
            .replace(/\b\w/g, (character) => character.toUpperCase())
        : "Unknown tool"
  }
}

function DetailValue({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="mt-1 truncate font-medium" title={value}>
        {value}
      </dd>
    </div>
  )
}

function LatencyValue({ label, value }: { label: string; value?: number }) {
  return (
    <div className="bg-card px-3 py-3">
      <dt className="text-[0.6875rem] text-muted-foreground">{label}</dt>
      <dd className="mt-1 font-mono text-sm font-semibold">
        {formatLatency(value)}
      </dd>
    </div>
  )
}

function EvidenceIdentifiers({
  appointmentID,
  previousAppointmentID,
}: {
  appointmentID?: string
  previousAppointmentID?: string
}) {
  const identifiers = [
    ["Appointment", appointmentID],
    ["Previous", previousAppointmentID],
  ].filter((entry): entry is [string, string] => Boolean(entry[1]))
  if (identifiers.length === 0) return null
  return (
    <dl className="mt-3 space-y-1 font-mono text-[0.6875rem] text-muted-foreground">
      {identifiers.map(([label, value]) => (
        <div key={label} className="flex gap-2">
          <dt>{label}</dt>
          <dd className="min-w-0 truncate text-foreground">{value}</dd>
        </div>
      ))}
    </dl>
  )
}

function EvidenceValue({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0">
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="mt-1 truncate font-mono text-foreground" title={value}>
        {value}
      </dd>
    </div>
  )
}

function hasAppointmentFacts(facts: AiAppointmentFacts) {
  return Object.values(facts).some(Boolean)
}

function visitLabel(facts: AiAppointmentFacts) {
  if (facts.appointmentTypeName) return facts.appointmentTypeName
  if (facts.careLane === "medical_md") return "Medical"
  if (facts.careLane === "routine_od") return "Routine vision"
  return "—"
}

function formatAppointmentDateTime(facts: AiAppointmentFacts) {
  if (facts.startDatetime) {
    const value = new Date(facts.startDatetime)
    if (!Number.isNaN(value.getTime())) {
      return new Intl.DateTimeFormat(undefined, {
        dateStyle: "long",
        timeStyle: "short",
      }).format(value)
    }
  }
  if (!facts.appointmentDate) return facts.appointmentTime
  const match = facts.appointmentDate.match(/^(\d{4})-(\d{2})-(\d{2})$/)
  const date = match
    ? new Date(Number(match[1]), Number(match[2]) - 1, Number(match[3]))
    : new Date(facts.appointmentDate)
  const dateLabel = Number.isNaN(date.getTime())
    ? facts.appointmentDate
    : new Intl.DateTimeFormat(undefined, { dateStyle: "long" }).format(date)
  return facts.appointmentTime
    ? `${dateLabel} · ${facts.appointmentTime}`
    : dateLabel
}

function formatDateTime(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return "Unknown"
  return new Intl.DateTimeFormat(undefined, {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(date)
}

function formatTime(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return "Unknown time"
  return new Intl.DateTimeFormat(undefined, {
    hour: "numeric",
    minute: "2-digit",
    second: "2-digit",
  }).format(date)
}

function formatDuration(startedAt: string, endedAt?: string): string {
  if (!endedAt) return "In progress"
  const seconds = Math.max(
    0,
    Math.round(
      (new Date(endedAt).getTime() - new Date(startedAt).getTime()) / 1000,
    ),
  )
  if (!Number.isFinite(seconds)) return "Unknown"
  return `${Math.floor(seconds / 60)}m ${seconds % 60}s`
}

function formatLatency(value?: number): string {
  if (value === undefined) return "—"
  return value >= 1000
    ? `${(value / 1000).toFixed(2)} s`
    : `${Math.round(value)} ms`
}

function formatPhone(value: string): string {
  const digits = value.replace(/\D/g, "")
  const local =
    digits.length === 11 && digits.startsWith("1") ? digits.slice(1) : digits
  if (local.length !== 10) return value
  return `(${local.slice(0, 3)}) ${local.slice(3, 6)}-${local.slice(6)}`
}

function formatPayload(value: unknown): string {
  if (value === undefined) return "No payload recorded"
  if (typeof value === "string") return value
  try {
    return JSON.stringify(value, null, 2)
  } catch {
    return "Payload could not be displayed"
  }
}
