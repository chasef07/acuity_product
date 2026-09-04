"use client"

import { useEffect, useState } from "react"
import {
  ArrowRightIcon,
  CalendarCheck2Icon,
  CalendarClockIcon,
  CalendarX2Icon,
  ChartNoAxesCombinedIcon,
  CircleAlertIcon,
  Clock3Icon,
  InboxIcon,
  PhoneForwardedIcon,
  RefreshCwIcon,
  WrenchIcon,
} from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { AnalyticsFrame } from "@/components/analytics/analytics-layout"
import { CostOverview } from "@/components/analytics/cost-overview"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import { Skeleton } from "@/components/ui/skeleton"
import { SidebarTrigger } from "@/components/ui/sidebar"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { OperatorAnalyticsDetailSheet } from "@/components/workspace/operator-analytics-detail"
import { portalClient } from "@/lib/api/client"
import { queryOperatorAiAnalytics } from "@/lib/api/generated/sdk.gen"
import type {
  OperatorAiAnalyticsPage,
  OperatorAiAnalyticsRange,
  OperatorAiAnalyticsSummary,
  OperatorAiCallAnalytics,
  Location,
} from "@/lib/api/generated/types.gen"
import { getAccessToken } from "@/lib/auth-client"

type AnalyticsLoadState = "loading" | "ready" | "unauthorized" | "unavailable"
type AnalyticsNextPageState = "idle" | "loading" | "unavailable"
type AnalyticsTab = "overview" | "cost" | "performance" | "tools" | "calls"

const analyticsTabs: Array<{ value: AnalyticsTab; label: string }> = [
  { value: "overview", label: "Overview" },
  { value: "cost", label: "Cost" },
  { value: "performance", label: "Performance" },
  { value: "tools", label: "Tools" },
  { value: "calls", label: "Calls" },
]

const ranges: Array<{
  value: OperatorAiAnalyticsRange
  label: string
  short: string
}> = [
  { value: "24h", label: "Last 24 hours", short: "24 hours" },
  { value: "7d", label: "Last 7 days", short: "7 days" },
  { value: "30d", label: "Last 30 days", short: "30 days" },
]

export function OperatorAnalytics({
  practiceID,
  locationScopeID,
  locations,
}: {
  practiceID: string
  locationScopeID: string
  locations: Location[]
}) {
  const scopeKey = `${practiceID}:${locationScopeID}`
  const [officeSelection, setOfficeSelection] = useState({
    key: scopeKey,
    value: locationScopeID || "all",
  })
  const office =
    officeSelection.key === scopeKey
      ? officeSelection.value
      : locationScopeID || "all"
  const setOffice = (value: string) =>
    setOfficeSelection({ key: scopeKey, value })
  const locationID = office === "all" ? "" : office
  const [range, setRange] = useState<OperatorAiAnalyticsRange>("7d")
  const [tab, setTab] = useState<AnalyticsTab>("overview")
  const costView = tab === "cost"
  const [requestVersion, setRequestVersion] = useState(0)
  const [request, setRequest] = useState<{
    key: string
    state: AnalyticsLoadState
    data?: OperatorAiAnalyticsPage
  }>({ key: "", state: "loading" })
  const [selectedInteractionID, setSelectedInteractionID] = useState("")
  const [nextPageRequest, setNextPageRequest] = useState<{
    key: string
    state: AnalyticsNextPageState
  }>({ key: "", state: "idle" })
  const requestKey = `${practiceID}:${locationID}:${range}:${requestVersion}`
  const currentRequest =
    request.key === requestKey
      ? request
      : { key: requestKey, state: "loading" as const }
  const nextPageState =
    nextPageRequest.key === requestKey ? nextPageRequest.state : "idle"

  useEffect(() => {
    if (!practiceID || costView) return
    const controller = new AbortController()
    void getAccessToken().then(async (token) => {
      if (controller.signal.aborted) return
      if (!token) {
        setRequest({ key: requestKey, state: "unauthorized" })
        return
      }
      try {
        const result = await queryOperatorAiAnalytics({
          client: portalClient(token),
          body: {
            practiceId: practiceID,
            locationId: locationID || undefined,
            range,
            limit: 50,
          },
          signal: controller.signal,
        }).catch(() => undefined)
        if (controller.signal.aborted) return
        if (result?.data) {
          setRequest({ key: requestKey, state: "ready", data: result.data })
          return
        }
        const status = result?.response?.status
        setRequest({
          key: requestKey,
          state:
            status === 401 || status === 403 ? "unauthorized" : "unavailable",
        })
      } catch {
        if (!controller.signal.aborted) {
          setRequest({ key: requestKey, state: "unavailable" })
        }
      }
    })
    return () => controller.abort()
  }, [locationID, practiceID, range, requestKey, costView])

  async function loadNextPage() {
    if (
      currentRequest.state !== "ready" ||
      !currentRequest.data?.nextCursor ||
      nextPageState === "loading"
    ) {
      return
    }
    const cursor = currentRequest.data.nextCursor
    setNextPageRequest({ key: requestKey, state: "loading" })
    try {
      const token = await getAccessToken()
      if (!token) {
        setRequest((current) =>
          current.key === requestKey
            ? { key: requestKey, state: "unauthorized" }
            : current,
        )
        setNextPageRequest({ key: requestKey, state: "idle" })
        return
      }
      const result = await queryOperatorAiAnalytics({
        client: portalClient(token),
        body: {
          practiceId: practiceID,
          locationId: locationID || undefined,
          range,
          cursor,
          limit: 50,
        },
      }).catch(() => undefined)
      if (!result?.data) {
        setNextPageRequest({ key: requestKey, state: "unavailable" })
        return
      }
      setRequest((current) => {
        if (
          current.key !== requestKey ||
          current.state !== "ready" ||
          !current.data
        ) {
          return current
        }
        return {
          key: requestKey,
          state: "ready",
          data: {
            ...current.data,
            calls: [...current.data.calls, ...result.data.calls],
            nextCursor: result.data.nextCursor,
          },
        }
      })
      setNextPageRequest({ key: requestKey, state: "idle" })
    } catch {
      setNextPageRequest({ key: requestKey, state: "unavailable" })
    }
  }

  const offices = [
    { value: "all", label: "All offices" },
    ...locations.map((location) => ({
      value: location.id,
      label: location.name,
    })),
  ]
  return (
    <section
      aria-label="AI call analytics"
      className="flex min-h-0 flex-1 flex-col overflow-hidden"
    >
      <AnalyticsFrame
        section="AI diagnostics"
        title={analyticsTabs.find((item) => item.value === tab)!.label}
        headerLeading={<SidebarTrigger collapsedOnly />}
        periodLabel={ranges.find((item) => item.value === range)!.label}
        controls={
          <>
            <Select
              value={office}
              items={offices}
              onValueChange={(value) => {
                if (value !== null) setOffice(value)
              }}
            >
              <SelectTrigger aria-label="Office">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {offices.map((item) => (
                  <SelectItem key={item.value} value={item.value}>
                    {item.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <ToggleGroup
              variant="segmented"
              spacing={1}
              value={[range]}
              aria-label="Analytics range"
              onValueChange={(values) => {
                const value = values[0]
                if (ranges.some((item) => item.value === value))
                  setRange(value as OperatorAiAnalyticsRange)
              }}
            >
              {ranges.map((item) => (
                <ToggleGroupItem
                  key={item.value}
                  value={item.value}
                  aria-label={item.label}
                >
                  {item.short}
                </ToggleGroupItem>
              ))}
            </ToggleGroup>
          </>
        }
        tabs={<AnalyticsTabs tab={tab} onChange={setTab} />}
      >
        {costView ? (
          <CostOverview
            practiceID={practiceID}
            locationID={locationID}
            range={range}
          />
        ) : (
          <>
            {currentRequest.state === "loading" && <AnalyticsLoading />}
            {currentRequest.state === "unauthorized" && (
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
            {currentRequest.state === "ready" && currentRequest.data && (
              <AnalyticsReady
                data={currentRequest.data}
                tab={tab}
                range={range}
                nextPageState={nextPageState}
                onLoadNextPage={loadNextPage}
                onSelect={setSelectedInteractionID}
              />
            )}
          </>
        )}
      </AnalyticsFrame>
      <OperatorAnalyticsDetailSheet
        interactionID={selectedInteractionID}
        onClose={() => setSelectedInteractionID("")}
      />
    </section>
  )
}

function AnalyticsReady({
  data,
  tab,
  range,
  nextPageState,
  onLoadNextPage,
  onSelect,
}: {
  data: OperatorAiAnalyticsPage
  tab: AnalyticsTab
  range: OperatorAiAnalyticsRange
  nextPageState: AnalyticsNextPageState
  onLoadNextPage: () => void
  onSelect: (interactionID: string) => void
}) {
  return (
    <>
      {tab === "overview" && <AnalyticsOverview summary={data.summary} />}
      {tab === "performance" && <AnalyticsPerformance summary={data.summary} />}
      {tab === "tools" && <AnalyticsTools summary={data.summary} />}
      {tab === "calls" && data.calls.length === 0 ? (
        <Empty className="mt-5 min-h-72 border bg-card sm:mt-6">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <InboxIcon aria-hidden="true" />
            </EmptyMedia>
            <EmptyTitle>No AI calls in this range</EmptyTitle>
            <EmptyDescription>
              Change the time range or workspace office to review another slice
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

function AnalyticsTabs({
  tab,
  onChange,
}: {
  tab: AnalyticsTab
  onChange: (tab: AnalyticsTab) => void
}) {
  return (
    <div className="max-w-full overflow-x-auto">
      <ToggleGroup
        variant="segmented"
        spacing={1}
        value={[tab]}
        aria-label="AI diagnostics view"
        onValueChange={(values) => {
          const value = values[0]
          if (analyticsTabs.some((item) => item.value === value))
            onChange(value as AnalyticsTab)
        }}
      >
        {analyticsTabs.map((item) => (
          <ToggleGroupItem key={item.value} value={item.value}>
            {item.label}
          </ToggleGroupItem>
        ))}
      </ToggleGroup>
    </div>
  )
}

function AnalyticsOverview({ summary }: { summary: OperatorAiAnalyticsSummary }) {
  const outcomes = [
    {
      label: "Total calls",
      value: summary.totalCalls.toLocaleString(),
      note: "All calls in selected range",
      icon: ChartNoAxesCombinedIcon,
    },
    {
      label: "Booked",
      value: summary.bookingCount.toLocaleString(),
      note: "Appointments booked",
      icon: CalendarCheck2Icon,
    },
    {
      label: "Cancelled",
      value: summary.cancellationCount.toLocaleString(),
      note: "Appointments cancelled",
      icon: CalendarX2Icon,
    },
    {
      label: "Rescheduled",
      value: summary.rescheduleCount.toLocaleString(),
      note: "Appointments moved",
      icon: CalendarClockIcon,
    },
  ]
  return (
    <div className="space-y-3">
      <div className="grid grid-cols-2 gap-3 xl:grid-cols-4">
        {outcomes.map((item) => (
          <Card key={item.label} size="sm" className="min-w-0">
            <CardHeader className="grid-cols-[1fr_auto]">
              <div>
                <CardDescription>{item.label}</CardDescription>
                <CardTitle className="mt-1 font-mono text-xl font-semibold tracking-[-0.03em] sm:text-2xl">
                  {item.value}
                </CardTitle>
              </div>
              <span className="flex size-7 items-center justify-center rounded-md bg-muted text-muted-foreground">
                <item.icon className="size-3.5" aria-hidden="true" />
              </span>
            </CardHeader>
            <CardContent className="truncate text-[0.6875rem] text-muted-foreground">
              {item.note}
            </CardContent>
          </Card>
        ))}
      </div>

      <div className="mt-3 grid gap-3 lg:grid-cols-2">
        <OperationalHealth summary={summary} />
      </div>
    </div>
  )
}

function AnalyticsPerformance({
  summary,
}: {
  summary: OperatorAiAnalyticsSummary
}) {
  return (
    <div className="space-y-6">
      <LatencyPipeline summary={summary} />
      <LatencyPercentiles summary={summary} />
    </div>
  )
}

function LatencyPercentiles({
  summary,
}: {
  summary: OperatorAiAnalyticsSummary
}) {
  const rows = [
    {
      label: "STT final transcript",
      p50: summary.p50SttMs,
      p90: summary.p90SttMs,
      p99: summary.p99SttMs,
    },
    {
      label: "LLM TTFT",
      p50: summary.p50TtftMs,
      p90: summary.p90TtftMs,
      p99: summary.p99TtftMs,
    },
    {
      label: "TTS TTFB",
      p50: summary.p50TtsTtfbMs,
      p90: summary.p90TtsTtfbMs,
      p99: summary.p99TtsTtfbMs,
    },
    {
      label: "E2E response",
      p50: summary.p50TotalLatencyMs,
      p90: summary.p90TotalLatencyMs,
      p99: summary.p99TotalLatencyMs,
    },
  ]

  return (
    <Card size="sm" className="gap-0 py-0">
      <CardHeader className="border-b py-4">
        <CardTitle>Latency percentiles</CardTitle>
        <CardDescription>
          Median, tail, and worst-tail response timing across measured turns.
        </CardDescription>
      </CardHeader>
      <CardContent className="p-0">
        <Table className="min-w-[38rem]">
          <TableHeader className="bg-muted text-muted-foreground">
            <TableRow className="hover:bg-transparent">
              <TableHead className="px-4">Pipeline stage</TableHead>
              <TableHead>P50</TableHead>
              <TableHead>P90</TableHead>
              <TableHead>P99</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((row) => (
              <TableRow key={row.label}>
                <TableCell className="px-4 py-3 font-medium">{row.label}</TableCell>
                <PercentileCell value={row.p50} />
                <PercentileCell value={row.p90} />
                <PercentileCell value={row.p99} emphasized />
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </CardContent>
    </Card>
  )
}

function PercentileCell({
  value,
  emphasized = false,
}: {
  value?: number
  emphasized?: boolean
}) {
  return (
    <TableCell
      className={emphasized ? "px-3 py-3 font-mono font-semibold" : "px-3 py-3 font-mono"}
    >
      {formatLatency(value)}
    </TableCell>
  )
}

function AnalyticsTools({ summary }: { summary: OperatorAiAnalyticsSummary }) {
  const items = [
    {
      label: "Total tool calls",
      value: summary.toolCallCount.toLocaleString(),
      note: "Across selected calls",
      icon: WrenchIcon,
    },
    {
      label: "Failure rate",
      value: formatRate(summary.toolFailureRate),
      note: `${summary.toolErrorCount.toLocaleString()} / ${summary.toolCallCount.toLocaleString()} tool calls`,
      icon: CircleAlertIcon,
    },
    {
      label: "Avg tools / call",
      value:
        summary.totalCalls > 0
          ? (summary.toolCallCount / summary.totalCalls).toFixed(1)
          : "—",
      note: "Tool activity per conversation",
      icon: ChartNoAxesCombinedIcon,
    },
  ]

  return (
    <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
      {items.map((item) => (
        <Card key={item.label} size="sm">
          <CardHeader className="grid-cols-[1fr_auto]">
            <div>
              <CardDescription>{item.label}</CardDescription>
              <CardTitle className="mt-1 font-mono text-2xl font-semibold tracking-[-0.03em]">
                {item.value}
              </CardTitle>
            </div>
            <span className="flex size-7 items-center justify-center rounded-md bg-muted text-muted-foreground">
              <item.icon className="size-3.5" aria-hidden="true" />
            </span>
          </CardHeader>
          <CardContent className="text-[0.6875rem] text-muted-foreground">
            {item.note}
          </CardContent>
        </Card>
      ))}
    </div>
  )
}

function LatencyPipeline({ summary }: { summary: OperatorAiAnalyticsSummary }) {
  const stages = [
    {
      label: "P50 STT",
      value: summary.p50SttMs,
      note: "Final transcript",
    },
    {
      label: "P50 LLM TTFT",
      value: summary.p50TtftMs,
      note: "First token",
    },
    {
      label: "P50 TTS TTFB",
      value: summary.p50TtsTtfbMs,
      note: "First audio byte",
    },
    {
      label: "P50 E2E",
      value: summary.p50TotalLatencyMs,
      note: "Caller stop to audio",
    },
  ]

  return (
    <Card
      role="region"
      aria-label="Median response pipeline"
      size="sm"
      className="min-w-0 gap-0 overflow-hidden py-0"
    >
      <CardHeader className="border-b">
        <div className="flex items-center gap-2">
          <Clock3Icon className="size-3.5 text-muted-foreground" aria-hidden="true" />
          <CardTitle className="text-sm">Median response pipeline</CardTitle>
        </div>
        <CardDescription>
          Measured turns across the selected range
        </CardDescription>
      </CardHeader>
      <CardContent className="grid grid-cols-2 gap-px bg-border p-0 sm:grid-cols-4">
        {stages.map((stage, index) => (
          <div key={stage.label} className="relative min-w-0 bg-card px-4 py-3">
            <p className="text-[0.6875rem] font-medium text-muted-foreground">
              {stage.label}
            </p>
            <p className="mt-1 font-mono text-base font-semibold tracking-[-0.02em]">
              {formatLatency(stage.value)}
            </p>
            <p className="mt-0.5 truncate text-[0.625rem] text-muted-foreground">
              {stage.note}
            </p>
            {index < stages.length - 1 && (
              <ArrowRightIcon
                className="absolute top-1/2 right-0 z-10 hidden size-3 -translate-y-1/2 translate-x-1/2 rounded-full bg-card text-muted-foreground sm:block"
                aria-hidden="true"
              />
            )}
          </div>
        ))}
      </CardContent>
    </Card>
  )
}

function OperationalHealth({ summary }: { summary: OperatorAiAnalyticsSummary }) {
  return (
    <Card size="sm" className="min-w-0">
      <CardHeader>
        <CardTitle className="text-sm">Operational health</CardTitle>
        <CardDescription>Transfers and tool reliability</CardDescription>
      </CardHeader>
      <CardContent>
        <dl className="grid grid-cols-2 gap-4 border-t pt-3">
          <CompactValue
            label="Transfer rate"
            value={formatRate(summary.transferRate)}
            note={`${summary.transferCount.toLocaleString()} calls`}
          />
          <CompactValue
            label="Tool failure rate"
            value={formatRate(summary.toolFailureRate)}
            note={`${summary.toolErrorCount.toLocaleString()} / ${summary.toolCallCount.toLocaleString()} tool calls`}
          />
        </dl>
      </CardContent>
    </Card>
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
  totalCalls: number
  range: OperatorAiAnalyticsRange
  hasNextPage: boolean
  nextPageState: AnalyticsNextPageState
  onLoadNextPage: () => void
  onSelect: (interactionID: string) => void
}) {
  return (
    <section aria-labelledby="call-ledger-title" className="mt-5 sm:mt-6">
      <div className="mb-3 flex items-end justify-between gap-4">
        <div>
          <p className="text-[0.6875rem] font-medium tracking-wide text-muted-foreground uppercase">
            Call ledger
          </p>
          <h2 id="call-ledger-title" className="mt-0.5 text-sm font-semibold">
            AI calls
          </h2>
        </div>
        <p className="text-xs text-muted-foreground">
          {calls.length.toLocaleString()} shown · {totalCalls.toLocaleString()} total
          · {range}
        </p>
      </div>

      <div className="hidden overflow-hidden rounded-xl border bg-card xl:block">
          <Table className="min-w-[78rem]">
            <TableHeader className="bg-muted text-[0.6875rem] text-muted-foreground">
              <TableRow className="hover:bg-transparent">
                <TableHead>Date / time</TableHead>
                <TableHead>Caller</TableHead>
                <TableHead>Office</TableHead>
                <TableHead>Duration</TableHead>
                <TableHead>P50 STT</TableHead>
                <TableHead>P50 TTFT</TableHead>
                <TableHead>P50 TTS</TableHead>
                <TableHead>P50 E2E</TableHead>
                <TableHead>Actions</TableHead>
                <TableHead>Tool errors</TableHead>
                <TableHead>Transfer</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {calls.map((call) => (
                <TableRow
                  key={call.id}
                  className="group cursor-pointer hover:bg-muted"
                  onClick={() => onSelect(call.id)}
                >
                  <TableCell className="px-3 py-3">
                    <button
                      type="button"
                      className="rounded-sm text-left font-medium outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
                      aria-label={`Open analytics for call from ${formatDateTime(call.startedAt)}`}
                      onClick={(event) => {
                        event.stopPropagation()
                        onSelect(call.id)
                      }}
                    >
                      {formatDateTime(call.startedAt)}
                    </button>
                  </TableCell>
                  <TableCell className="tabular-nums">{formatPhone(call.phone)}</TableCell>
                  <TableCell>{call.locationName}</TableCell>
                  <TableCell className="tabular-nums">
                    {formatDuration(call.durationSeconds)}
                  </TableCell>
                  <TableCell className="font-mono">{formatLatency(call.p50SttMs)}</TableCell>
                  <TableCell className="font-mono">{formatLatency(call.p50TtftMs)}</TableCell>
                  <TableCell className="font-mono">{formatLatency(call.p50TtsTtfbMs)}</TableCell>
                  <TableCell className="font-mono font-semibold">
                    {formatLatency(call.p50TotalLatencyMs)}
                  </TableCell>
                  <TableCell>
                    <ActionBadges actions={call.toolActions} />
                  </TableCell>
                  <TableCell>
                    <Badge variant={call.toolErrorCount > 0 ? "destructive" : "outline"}>
                      {call.toolErrorCount} / {call.toolCallCount}
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
            aria-label={`Open analytics for call from ${formatDateTime(call.startedAt)}`}
            className="rounded-xl border bg-card p-4 text-left outline-none transition-colors hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring/40"
            onClick={() => onSelect(call.id)}
          >
            <div className="flex items-start justify-between gap-3">
              <div className="min-w-0">
                <p className="text-sm font-semibold tabular-nums">{formatPhone(call.phone)}</p>
                <p className="mt-0.5 truncate text-xs text-muted-foreground">
                  {formatDateTime(call.startedAt)} · {call.locationName}
                </p>
              </div>
              <ArrowRightIcon
                className="mt-0.5 size-4 shrink-0 text-muted-foreground"
                aria-hidden="true"
              />
            </div>
            <dl className="mt-4 grid grid-cols-3 gap-3 border-y py-3">
              <CompactValue label="Duration" value={formatDuration(call.durationSeconds)} />
              <CompactValue label="P50 E2E" value={formatLatency(call.p50TotalLatencyMs)} />
              <CompactValue
                label="Tool errors"
                value={`${call.toolErrorCount} / ${call.toolCallCount}`}
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

function AnalyticsLoading() {
  return (
    <div aria-label="Loading AI call analytics" aria-busy="true">
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

function formatDateTime(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return "Unknown"
  return new Intl.DateTimeFormat(undefined, {
    month: "short",
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
  }).format(date)
}

function formatDuration(seconds: number): string {
  if (!Number.isFinite(seconds)) return "—"
  return `${Math.floor(seconds / 60)}m ${Math.max(0, Math.round(seconds)) % 60}s`
}

function formatLatency(value?: number): string {
  if (value === undefined) return "—"
  return value >= 1000 ? `${(value / 1000).toFixed(2)} s` : `${Math.round(value)} ms`
}

function formatRate(value: number): string {
  if (!Number.isFinite(value)) return "—"
  return new Intl.NumberFormat(undefined, {
    style: "percent",
    maximumFractionDigits: 1,
  }).format(value)
}

function formatPhone(value: string): string {
  const digits = value.replace(/\D/g, "")
  const local = digits.length === 11 && digits.startsWith("1") ? digits.slice(1) : digits
  if (local.length !== 10) return value
  return `(${local.slice(0, 3)}) ${local.slice(3, 6)}-${local.slice(6)}`
}
