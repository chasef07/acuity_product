"use client"

import { useState } from "react"
import {
  ArrowRightIcon,
  CircleAlertIcon,
  InboxIcon,
  PhoneForwardedIcon,
  RefreshCwIcon,
} from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { commandCenterPages, type CommandCenterPage } from "./analytics-navigation"
import {
  DiagnosticsTools,
  type DiagnosticFocus,
  type SelectDiagnostic,
} from "@/components/analytics/ai-diagnostics"
import { OverviewTrend } from "./overview-trend"
import { BreakdownControls, CallBreakdowns, initialBreakdownSelection, type BreakdownSelection } from "./call-breakdowns"
import { CostOverview } from "@/components/analytics/cost-overview"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardHeader,
} from "@/components/ui/card"
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import { Skeleton } from "@/components/ui/skeleton"
import { OptionSelect } from "./manage-agent-page"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { OperatorAnalyticsDetailSheet } from "@/components/workspace/operator-analytics-detail"
import { PendingCallIssues } from "@/components/workspace/call-issue-review"
import { CallReviewWorkspace } from "@/components/workspace/call-review"
import { CallLabel, CallLabelFilters, CallLabels, type IntentFilter, type OutcomeFilter } from "./call-labels"
import { CallFailureFilter } from "./call-failure-filter"
import type {
  OperatorAiCallIssue,
  OperatorAiAnalyticsRange,
  OperatorAiAnalyticsSummary,
  OperatorAiCallAnalytics,
} from "@/lib/api/generated/types.gen"
import {
  type AiCallAnalytics,
  type AiCallLedger,
  useAiCallAnalytics,
} from "@/lib/clients/agent-calls"
import { formatTimestamp } from "@/lib/format-time"
import { formatUSPhoneDigits } from "@/lib/phone"

type AnalyticsNextPageState = AiCallLedger["nextPage"]
type AnalyticsTab = CommandCenterPage

const ranges: Array<{
  value: OperatorAiAnalyticsRange
  label: string
}> = [
  { value: "24h", label: "Last 24 hours" },
  { value: "7d", label: "Last 7 days" },
  { value: "30d", label: "Last 30 days" },
]

export function OperatorAnalytics({
  tab,
  revision = 0,
  practiceID,
  locationScopeID,
}: {
  tab: AnalyticsTab
  revision?: number
  practiceID: string
  locationScopeID: string
}) {
  const locationID = locationScopeID
  const [range, setRange] = useState<OperatorAiAnalyticsRange>("24h")
  const [intent, setIntent] = useState<IntentFilter>("all")
  const [outcome, setOutcome] = useState<OutcomeFilter>("all")
  const [judgeFlag, setJudgeFlag] = useState("any")
  const [breakdownSelection, setBreakdownSelection] = useState<BreakdownSelection>(initialBreakdownSelection)
  const costView = tab === "cost"
  const reviewView = tab === "review"
  const [requestVersion, setRequestVersion] = useState(0)
  const [selectedCall, setSelectedCall] = useState<{ id: string; focus?: DiagnosticFocus }>({ id: "" })
  const selectCall: SelectDiagnostic = (id, focus) => setSelectedCall({ id, focus })
  const requestKey = `${practiceID}:${locationID}:${range}:${intent}:${outcome}:${judgeFlag}:${revision}:${requestVersion}`
  const currentRequest = useAiCallAnalytics({
    practiceID,
    locationID,
    range,
    revision: revision + requestVersion,
    enabled: !costView && !reviewView,
    intent: tab === "breakdowns" ? (breakdownSelection.intent === "all" ? undefined : breakdownSelection.intent) : tab === "calls" && intent !== "all" ? intent : undefined,
    outcome: tab === "breakdowns" ? (breakdownSelection.outcome === "all" ? undefined : breakdownSelection.outcome) : tab === "calls" && outcome !== "all" ? outcome : undefined,
    failuresOnly: tab === "calls" && judgeFlag !== "all" ? true : undefined,
    failingJudge: tab === "calls" && judgeFlag !== "all" && judgeFlag !== "any" ? judgeFlag : undefined,
  })
  const nextPageState = currentRequest.nextPage

  async function loadNextPage() {
    const page = await currentRequest.loadNextPage()
    return page?.calls[0]?.id
  }

  const calls = currentRequest.data?.calls ?? []
  const selectedIndex = calls.findIndex((call) => call.id === selectedCall.id)
  const previousCall = selectedIndex > 0 ? calls[selectedIndex - 1] : undefined
  const nextCall = selectedIndex >= 0 ? calls[selectedIndex + 1] : undefined
  const canLoadNextCall = selectedIndex === calls.length - 1 && selectedIndex >= 0 && Boolean(currentRequest.data?.nextCursor)

  async function selectNextCall() {
    if (nextCall) {
      selectCall(nextCall.id)
    } else if (canLoadNextCall) {
      const nextID = await loadNextPage()
      if (nextID) {
        setSelectedCall((current) => current.id === selectedCall.id ? { id: nextID } : current)
      }
    }
  }

  function updateCallIssue(issue: OperatorAiCallIssue) {
    currentRequest.update((shown) => ({
      ...shown,
      pendingIssues: shown.pendingIssues?.filter((item) => item.interactionId !== issue.interactionId),
    }))
  }

  return (
    <section
      aria-label="Command Center"
      className="flex min-h-0 flex-1 flex-col overflow-hidden"
    >
      <div className="flex min-h-0 min-w-0 flex-1 flex-col overflow-y-auto bg-background">
        <h1 className="sr-only">{commandCenterPages.find((item) => item.value === tab)!.label}</h1>
        <main className="mx-auto w-full max-w-[1320px] p-4 sm:p-6">
          <div className="mb-5 flex flex-wrap items-center justify-end gap-4">
            {reviewView && <p className="mr-auto text-[13px] font-medium">Daily call review and golden set</p>}
            {!reviewView && <div className="flex min-w-0 flex-wrap items-center gap-3">
            <OptionSelect label="Date range" value={range} items={ranges} onChange={setRange} />
            {tab === "calls" && <>
              <CallFailureFilter value={judgeFlag} onChange={setJudgeFlag} />
              <CallLabelFilters intent={intent} outcome={outcome} onIntent={setIntent} onOutcome={setOutcome} />
            </>}
            </div>}
          </div>
        {tab === "breakdowns" && <BreakdownControls selection={breakdownSelection} onSelection={setBreakdownSelection} />}
        {reviewView ? (
          <CallReviewWorkspace key={practiceID} practiceID={practiceID} onOpenCall={(id) => selectCall(id)} />
        ) : costView ? (
          <CostOverview
            practiceID={practiceID}
            locationID={locationID}
            range={range}
          />
        ) : (
          <>
            {currentRequest.state === "loading" && <AnalyticsLoading overview={tab === "overview"} />}
            {currentRequest.state === "denied" && (
              <AnalyticsFailure
                title="Analytics access unavailable"
                description="This session is not authorized to load Platform Operator call evidence."
              />
            )}
            {currentRequest.state === "unavailable" && (
              <AnalyticsFailure
                title="Analytics temporarily unavailable"
                description="No call evidence was reconstructed. Retry when the analytics service is available."
                onRetry={() => setRequestVersion((current) => current + 1)}
              />
            )}
            {currentRequest.state === "ready" && tab === "calls" && (
              <PendingCallIssues
                issues={currentRequest.data?.pendingIssues ?? []}
                onSelect={selectCall}
              />
            )}
            {currentRequest.state === "ready" && currentRequest.data && (
              <AnalyticsReady
                key={requestKey}
                data={currentRequest.data}
                breakdownSelection={breakdownSelection}
                onBreakdownSelection={setBreakdownSelection}
                onRetry={() => setRequestVersion((current) => current + 1)}
                tab={tab}
                range={range}
                nextPageState={nextPageState}
                onLoadNextPage={loadNextPage}
                onSelect={selectCall}
              />
            )}
          </>
        )}
        </main>
      </div>
      <OperatorAnalyticsDetailSheet
        interactionID={selectedCall.id}
        focus={selectedCall.focus}
        onPrevious={!reviewView && previousCall ? () => selectCall(previousCall.id) : undefined}
        onNext={!reviewView && (nextCall || canLoadNextCall) ? selectNextCall : undefined}
        navigationLoading={nextPageState === "loading"}
        navigationError={canLoadNextCall && nextPageState === "unavailable"}
        onIssueChange={updateCallIssue}
        onClose={() => setSelectedCall({ id: "" })}
      />
    </section>
  )
}

function AnalyticsReady({
  data,
  breakdownSelection,
  onBreakdownSelection,
  onRetry,
  tab,
  range,
  nextPageState,
  onLoadNextPage,
  onSelect,
}: {
  data: AiCallAnalytics
  breakdownSelection: BreakdownSelection
  onBreakdownSelection: (selection: BreakdownSelection) => void
  onRetry: () => void
  tab: AnalyticsTab
  range: OperatorAiAnalyticsRange
  nextPageState: AnalyticsNextPageState
  onLoadNextPage: () => void
  onSelect: SelectDiagnostic
}) {
  return (
    <>
      {tab === "overview" && <AnalyticsOverview summary={data.summary} />}
      {tab === "breakdowns" && <CallBreakdowns data={data.summary.distributions} selection={breakdownSelection} onSelection={onBreakdownSelection} onRetry={onRetry} />}
      {tab === "tools" && <DiagnosticsTools summary={data.summary} onSelect={onSelect} />}
      {tab === "calls" && data.calls.length === 0 ? (
        <Empty className="mt-5 min-h-72 border bg-card sm:mt-6">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <InboxIcon aria-hidden="true" />
            </EmptyMedia>
            <EmptyTitle>No calls match these filters</EmptyTitle>
            <EmptyDescription>
              Change judge flags, intent, outcome, time range, or workspace office to review another slice
              of call evidence.
            </EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : tab === "calls" ? (
        <CallLedger
          calls={data.calls}
          totalCalls={data.summary.totalCalls}
          range={range}
          hasNextPage={Boolean(data.nextCursor)}
          nextPageState={nextPageState}
          onLoadNextPage={onLoadNextPage}
          onSelect={onSelect}
        />
      ) : null}
    </>
  )
}

function AnalyticsOverview({ summary }: { summary: OperatorAiAnalyticsSummary }) {
  const metrics = [
    { label: "Booked appointments", value: summary.bookingCount.toLocaleString() },
    { label: "Cancelled appointments", value: summary.cancellationCount.toLocaleString() },
    { label: "Rescheduled appointments", value: summary.rescheduleCount.toLocaleString() },
    { label: "Total calls", value: summary.totalCalls.toLocaleString() },
    { label: "Total minutes", value: summary.totalCallMinutes.toLocaleString(undefined, { maximumFractionDigits: 1 }) },
    { label: "Avg. time / call", value: summary.totalCalls ? formatDuration(Math.round(summary.totalCallMinutes * 60 / summary.totalCalls)) : "—" },
    { label: "Transfer rate", value: summary.totalCalls ? formatRate(summary.transferRate) : "—", note: `${summary.transferCount.toLocaleString()} of ${summary.totalCalls.toLocaleString()} calls` },
    { label: "Tool failure rate", value: summary.toolCallCount ? formatRate(summary.toolFailureRate) : "—", note: `${summary.toolErrorCount.toLocaleString()} of ${summary.toolCallCount.toLocaleString()} tool calls` },
  ]
  return (
    <section aria-label="Overall stats">
      <dl className="grid grid-cols-1 gap-px overflow-hidden rounded-xl border bg-border min-[380px]:grid-cols-2 xl:grid-cols-4">
        {metrics.map((metric) => (
          <div key={metric.label} className="min-w-0 bg-background px-5 py-6 sm:px-6 sm:py-8">
            <dt className="text-[13px] font-medium text-foreground">{metric.label}</dt>
            <dd className="mt-5 text-3xl leading-none font-medium tracking-tight tabular-nums sm:text-4xl">{metric.value}</dd>
            {metric.note && <dd className="mt-3 text-xs text-muted-foreground">{metric.note}</dd>}
          </div>
        ))}
      </dl>
      <OverviewTrend days={summary.daily} />
    </section>
  )
}

function CallLedger({
  calls,
  totalCalls,
  range,
  hasNextPage,
  nextPageState,
  onLoadNextPage,
  onSelect,
}: {
  calls: OperatorAiCallAnalytics[]
  totalCalls?: number
  range: OperatorAiAnalyticsRange
  hasNextPage: boolean
  nextPageState: AnalyticsNextPageState
  onLoadNextPage: () => void
  onSelect: SelectDiagnostic
}) {
  return (
    <section aria-labelledby="call-ledger-title" className="mt-5 sm:mt-6">
      <div className="mb-3 flex items-end justify-between gap-4">
        <div>
          <p className="text-[0.6875rem] font-medium tracking-wide text-muted-foreground uppercase">
            Call ledger
          </p>
          <h2 id="call-ledger-title" className="mt-0.5 text-sm font-semibold">
            Calls
          </h2>
        </div>
        <p className="text-xs text-muted-foreground">
          {calls.length.toLocaleString()} shown{totalCalls !== undefined && <> · {totalCalls.toLocaleString()} calls in range</>}
          · {range}
        </p>
      </div>

      <div className="hidden overflow-hidden rounded-xl border bg-card xl:block">
          <Table className="min-w-[54rem]">
            <TableHeader className="bg-muted text-[0.6875rem] text-muted-foreground">
              <TableRow className="hover:bg-transparent">
                <TableHead>Date / time</TableHead>
                <TableHead>Caller</TableHead>
                <TableHead>Office</TableHead>
                <TableHead>Intent</TableHead>
                <TableHead>Outcome</TableHead>
                <TableHead>Duration</TableHead>
                <TableHead>Actions</TableHead>
                <TableHead>Tool errors</TableHead>
                <TableHead>Transfer</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {calls.map((call) => (
                <TableRow
                  key={call.id}
                  className={call.reviewReasons?.length ? "group cursor-pointer bg-destructive/5 hover:bg-destructive/10" : "group cursor-pointer hover:bg-muted"}
                  onClick={() => onSelect(call.id)}
                >
                  <TableCell className="px-3 py-3">
                    <button
                      type="button"
                      className="rounded-sm text-left font-medium outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
                      aria-label={`Open analytics for call from ${formatTimestamp(call.startedAt)}`}
                      onClick={(event) => {
                        event.stopPropagation()
                        onSelect(call.id)
                      }}
                    >
                      {formatTimestamp(call.startedAt)}
                    </button>
                  </TableCell>
                  <TableCell className="tabular-nums">
                    {formatUSPhoneDigits(call.phone)}
                    <ReviewReasons reasons={call.reviewReasons} />
                  </TableCell>
                  <TableCell>{call.locationName}</TableCell>
                  <TableCell><CallLabel call={call} kind="intent" /></TableCell>
                  <TableCell><CallLabel call={call} kind="outcome" /></TableCell>
                  <TableCell className="tabular-nums">
                    {formatDuration(call.durationSeconds)}
                  </TableCell>
                  <TableCell>
                    <ActionBadges actions={call.toolActions} />
                  </TableCell>
                  <TableCell>
                    <Badge variant={call.toolErrorCount > 0 ? "destructive" : "outline"}>
                      {call.toolErrorCount}
                    </Badge>
                  </TableCell>
                  <TableCell>
                    <TransferBadge transferred={call.transferred} />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
      </div>

      <div className="grid gap-3 xl:hidden">
        {calls.map((call) => (
          <button
            key={call.id}
            type="button"
            aria-label={`Open analytics for call from ${formatTimestamp(call.startedAt)}`}
            className={`rounded-xl border p-4 text-left outline-none transition-colors focus-visible:ring-2 focus-visible:ring-ring/40 ${call.reviewReasons?.length ? "border-destructive/25 bg-destructive/5 hover:bg-destructive/10" : "bg-card hover:bg-muted"}`}
            onClick={() => onSelect(call.id)}
          >
            <div className="flex items-start justify-between gap-3">
              <div className="min-w-0">
                <p className="text-sm font-semibold tabular-nums">{formatUSPhoneDigits(call.phone)}</p>
                <p className="mt-0.5 truncate text-xs text-muted-foreground">
                  {formatTimestamp(call.startedAt)} · {call.locationName}
                </p>
              </div>
              <ArrowRightIcon
                className="mt-0.5 size-4 shrink-0 text-muted-foreground"
                aria-hidden="true"
              />
            </div>
            <ReviewReasons reasons={call.reviewReasons} />
            <div className="mt-3"><CallLabels call={call} /></div>
            <dl className="mt-4 grid grid-cols-2 gap-3 border-y py-3">
              <CompactValue label="Duration" value={formatDuration(call.durationSeconds)} />
              <CompactValue
                label="Tool errors"
                value={String(call.toolErrorCount)}
              />
            </dl>
            <div className="mt-3 flex flex-wrap items-center gap-1.5">
              <ActionBadges actions={call.toolActions} />
              <TransferBadge transferred={call.transferred} />
              {!call.transcriptAvailable && (
                <Badge variant="outline">Transcript missing</Badge>
              )}
            </div>
          </button>
        ))}
      </div>

      {(hasNextPage || nextPageState === "unavailable") && (
        <div className="mt-4 flex flex-col items-center gap-2">
          <Button
            type="button"
            variant="outline"
            disabled={nextPageState === "loading"}
            onClick={onLoadNextPage}
          >
            {nextPageState === "loading" && (
              <RefreshCwIcon className="animate-spin" aria-hidden="true" />
            )}
            {nextPageState === "unavailable"
              ? "Retry loading calls"
              : "Load more calls"}
          </Button>
          {nextPageState === "unavailable" && (
            <p className="text-xs text-destructive" role="status">
              More call evidence could not be loaded. The calls already shown are
              unchanged.
            </p>
          )}
        </div>
      )}
    </section>
  )
}

function ReviewReasons({ reasons }: { reasons?: string[] }) {
  if (!reasons?.length) return null
  return <span className="mt-1 flex flex-col gap-0.5 text-xs text-destructive">
    {reasons.map((reason) => <span key={reason}>{reason}</span>)}
  </span>
}

function AnalyticsLoading({ overview }: { overview: boolean }) {
  if (overview) return (
    <div role="status" aria-label="Loading Command Center" aria-busy="true" className="grid grid-cols-1 gap-px overflow-hidden rounded-xl border bg-border min-[380px]:grid-cols-2 xl:grid-cols-4">
      {Array.from({ length: 8 }, (_, index) => (
        <div key={index} className="bg-background px-5 py-6 sm:px-6 sm:py-8">
          <Skeleton className="h-4 w-32 max-w-full" />
          <Skeleton className="mt-5 h-10 w-24" />
        </div>
      ))}
    </div>
  )
  return (
    <div role="status" aria-label="Loading Command Center" aria-busy="true">
      <div className="grid grid-cols-2 gap-3 xl:grid-cols-4">
        {Array.from({ length: 4 }, (_, index) => (
          <Card key={index} size="sm">
            <CardHeader>
              <Skeleton className="h-3 w-24" />
              <Skeleton className="mt-2 h-7 w-20" />
            </CardHeader>
            <CardContent>
              <Skeleton className="h-3 w-32 max-w-full" />
            </CardContent>
          </Card>
        ))}
      </div>
      <div className="mt-3 grid gap-3 lg:grid-cols-[minmax(0,2fr)_minmax(17rem,0.75fr)]">
        <Skeleton className="h-36 rounded-xl" />
        <Skeleton className="h-36 rounded-xl" />
      </div>
      <div className="mt-5 space-y-px overflow-hidden rounded-xl border bg-border sm:mt-6">
        <Skeleton className="h-10 rounded-none" />
        {Array.from({ length: 5 }, (_, index) => (
          <Skeleton key={index} className="h-14 rounded-none bg-card" />
        ))}
      </div>
    </div>
  )
}

function AnalyticsFailure({
  title,
  description,
  onRetry,
}: {
  title: string
  description: string
  onRetry?: () => void
}) {
  return (
    <Alert className="max-w-2xl py-3" variant={onRetry ? "default" : "destructive"}>
      <CircleAlertIcon aria-hidden="true" />
      <AlertTitle>{title}</AlertTitle>
      <AlertDescription>
        <p>{description}</p>
        {onRetry && (
          <Button className="mt-3" size="sm" variant="outline" onClick={onRetry}>
            <RefreshCwIcon aria-hidden="true" />
            Retry
          </Button>
        )}
      </AlertDescription>
    </Alert>
  )
}

function ActionBadges({ actions }: { actions: string[] }) {
  const visible = actions.slice(0, 2)
  if (visible.length === 0) {
    return <span className="text-muted-foreground">—</span>
  }
  return (
    <span className="flex max-w-56 flex-wrap gap-1">
      {visible.map((action) => (
        <Badge key={action} variant="outline" className="max-w-28 truncate">
          {action}
        </Badge>
      ))}
      {actions.length > visible.length && (
        <Badge variant="secondary">+{actions.length - visible.length}</Badge>
      )}
    </span>
  )
}

function TransferBadge({ transferred }: { transferred: boolean }) {
  return transferred ? (
    <Badge variant="secondary">
      <PhoneForwardedIcon aria-hidden="true" />
      Transferred
    </Badge>
  ) : (
    <Badge variant="outline">No</Badge>
  )
}

function CompactValue({
  label,
  value,
  note,
}: {
  label: string
  value: string
  note?: string
}) {
  return (
    <div className="min-w-0">
      <dt className="text-[0.6875rem] text-muted-foreground">{label}</dt>
      <dd className="mt-1 truncate font-mono text-xs font-semibold">{value}</dd>
      {note && <dd className="mt-0.5 truncate text-[0.625rem] text-muted-foreground">{note}</dd>}
    </div>
  )
}

function formatDuration(seconds: number): string {
  if (!Number.isFinite(seconds)) return "—"
  return `${Math.floor(seconds / 60)}m ${Math.max(0, Math.round(seconds)) % 60}s`
}

function formatRate(value: number): string {
  if (!Number.isFinite(value)) return "—"
  return new Intl.NumberFormat(undefined, {
    style: "percent",
    maximumFractionDigits: 1,
  }).format(value)
}

