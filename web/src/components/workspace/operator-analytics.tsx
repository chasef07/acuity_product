"use client"

import { type ReactNode, useEffect, useState } from "react"
import {
  ArrowRightIcon,
  ChartNoAxesCombinedIcon,
  CircleAlertIcon,
  Clock3Icon,
  InboxIcon,
  PhoneForwardedIcon,
  RefreshCwIcon,
  WrenchIcon,
} from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import { OperatorAnalyticsDetailDialog } from "@/components/workspace/operator-analytics-detail"
import { portalClient } from "@/lib/api/client"
import { queryOperatorAiAnalytics } from "@/lib/api/generated/sdk.gen"
import type {
  OperatorAiAnalyticsPage,
  OperatorAiAnalyticsRange,
  OperatorAiAnalyticsSummary,
  OperatorAiCallAnalytics,
} from "@/lib/api/generated/types.gen"
import { getAccessToken } from "@/lib/auth-client"

type AnalyticsLoadState = "loading" | "ready" | "unauthorized" | "unavailable"
type AnalyticsNextPageState = "idle" | "loading" | "unavailable"

const ranges: Array<{
  value: OperatorAiAnalyticsRange
  label: string
  short: string
}> = [
  { value: "24h", label: "Last 24 hours", short: "24h" },
  { value: "7d", label: "Last 7 days", short: "7d" },
  { value: "30d", label: "Last 30 days", short: "30d" },
]

export function OperatorAnalytics({
  practiceID,
  locationScopeID,
}: {
  practiceID: string
  locationScopeID: string
}) {
  const [range, setRange] = useState<OperatorAiAnalyticsRange>("7d")
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
  const requestKey = `${practiceID}:${locationScopeID}:${range}:${requestVersion}`
  const currentRequest =
    request.key === requestKey
      ? request
      : { key: requestKey, state: "loading" as const }
  const nextPageState =
    nextPageRequest.key === requestKey ? nextPageRequest.state : "idle"

  useEffect(() => {
    if (!practiceID) return
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
            locationId: locationScopeID || undefined,
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
  }, [locationScopeID, practiceID, range, requestKey])

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
          locationId: locationScopeID || undefined,
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

  return (
    <section
      aria-labelledby="operator-analytics-title"
      className="flex min-h-0 flex-1 flex-col overflow-hidden bg-muted/20"
    >
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto w-full max-w-[100rem] p-4 sm:p-6 lg:p-8">
          <header className="mb-5 flex flex-col gap-4 sm:mb-6 sm:flex-row sm:items-end sm:justify-between">
            <div>
              <div className="mb-1 flex items-center gap-2 text-xs font-medium text-muted-foreground">
                <ChartNoAxesCombinedIcon
                  className="size-3.5"
                  aria-hidden="true"
                />
                Operator evidence
                <span aria-hidden="true">·</span>
                {locationScopeID ? "Selected office" : "All offices"}
              </div>
              <h1
                id="operator-analytics-title"
                className="text-xl font-semibold tracking-[-0.02em] sm:text-2xl"
              >
                AI call analytics
              </h1>
              <p className="mt-1 max-w-2xl text-sm text-muted-foreground">
                Review call quality, response timing, tool reliability, and
                appointment outcomes in the active workspace scope.
              </p>
            </div>
            <div
              role="group"
              aria-label="Analytics range"
              className="grid grid-cols-3 rounded-lg border bg-card p-1"
            >
              {ranges.map((item) => (
                <Button
                  key={item.value}
                  aria-label={item.label}
                  aria-pressed={range === item.value}
                  variant={range === item.value ? "secondary" : "ghost"}
                  size="sm"
                  className="min-w-14"
                  onClick={() => setRange(item.value)}
                >
                  {item.short}
                </Button>
              ))}
            </div>
          </header>

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
              range={range}
              nextPageState={nextPageState}
              onLoadNextPage={loadNextPage}
              onSelect={setSelectedInteractionID}
            />
          )}
        </div>
      </div>
      <OperatorAnalyticsDetailDialog
        interactionID={selectedInteractionID}
        onClose={() => setSelectedInteractionID("")}
      />
    </section>
  )
}

function AnalyticsReady({
  data,
  range,
  nextPageState,
  onLoadNextPage,
  onSelect,
}: {
  data: OperatorAiAnalyticsPage
  range: OperatorAiAnalyticsRange
  nextPageState: AnalyticsNextPageState
  onLoadNextPage: () => void
  onSelect: (interactionID: string) => void
}) {
  return (
    <>
      <AnalyticsKPIs summary={data.summary} />
      {data.calls.length === 0 ? (
        <div className="mt-5 flex min-h-72 flex-col items-center justify-center rounded-xl border border-dashed bg-card px-6 text-center sm:mt-6">
          <span className="mb-3 flex size-10 items-center justify-center rounded-full bg-muted text-muted-foreground">
            <InboxIcon className="size-5" aria-hidden="true" />
          </span>
          <h2 className="text-sm font-semibold">No AI calls in this range</h2>
          <p className="mt-1 max-w-md text-sm text-muted-foreground">
            Change the time range or workspace office to review another slice of
            call evidence.
          </p>
        </div>
      ) : (
        <CallLedger
          calls={data.calls}
          totalCalls={data.summary.totalCalls}
          range={range}
          hasNextPage={Boolean(data.nextCursor)}
          nextPageState={nextPageState}
          onLoadNextPage={onLoadNextPage}
          onSelect={onSelect}
        />
      )}
    </>
  )
}

function AnalyticsKPIs({ summary }: { summary: OperatorAiAnalyticsSummary }) {
  const items = [
    {
      label: "Total calls",
      value: summary.totalCalls.toLocaleString(),
      note: "All calls in selected range",
      icon: ChartNoAxesCombinedIcon,
    },
    {
      label: "P50 E2E latency",
      value: formatLatency(summary.p50TotalLatencyMs),
      note: "Median conversational turn",
      icon: Clock3Icon,
    },
    {
      label: "Transfer rate",
      value: formatRate(summary.transferRate),
      note: `${summary.transferCount.toLocaleString()} transferred`,
      icon: PhoneForwardedIcon,
    },
    {
      label: "Tool failure rate",
      value: formatRate(summary.toolFailureRate),
      note: `${summary.toolErrorCount.toLocaleString()} errors / ${summary.toolCallCount.toLocaleString()} tool calls`,
      icon: WrenchIcon,
    },
  ]
  return (
    <div className="grid grid-cols-2 gap-3 xl:grid-cols-4">
      {items.map((item) => (
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
        <div className="overflow-x-auto">
          <table className="w-full min-w-[78rem] border-collapse text-left text-xs">
            <thead className="bg-muted/70 text-[0.6875rem] text-muted-foreground">
              <tr>
                <TableHeading>Date / time</TableHeading>
                <TableHeading>Caller</TableHeading>
                <TableHeading>Office</TableHeading>
                <TableHeading>Duration</TableHeading>
                <TableHeading>P50 STT</TableHeading>
                <TableHeading>P50 TTFT</TableHeading>
                <TableHeading>P50 TTS</TableHeading>
                <TableHeading>P50 E2E</TableHeading>
                <TableHeading>Actions</TableHeading>
                <TableHeading>Tool errors</TableHeading>
                <TableHeading>Transfer</TableHeading>
              </tr>
            </thead>
            <tbody className="divide-y">
              {calls.map((call) => (
                <tr
                  key={call.id}
                  className="group transition-colors hover:bg-muted/40"
                  onClick={() => onSelect(call.id)}
                >
                  <td className="px-3 py-3">
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
                  </td>
                  <TableCell className="font-mono">{formatPhone(call.phone)}</TableCell>
                  <TableCell>{call.locationName}</TableCell>
                  <TableCell className="font-mono">
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
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>

      <div className="grid gap-3 xl:hidden">
        {calls.map((call) => (
          <button
            key={call.id}
            type="button"
            aria-label={`Open analytics for call from ${formatDateTime(call.startedAt)}`}
            className="rounded-xl border bg-card p-4 text-left outline-none transition-colors hover:bg-muted/40 focus-visible:ring-2 focus-visible:ring-ring/40"
            onClick={() => onSelect(call.id)}
          >
            <div className="flex items-start justify-between gap-3">
              <div className="min-w-0">
                <p className="font-mono text-sm font-semibold">{formatPhone(call.phone)}</p>
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

function TableHeading({ children }: { children: ReactNode }) {
  return <th className="px-3 py-2.5 font-medium whitespace-nowrap">{children}</th>
}

function TableCell({
  children,
  className = "",
}: {
  children: ReactNode
  className?: string
}) {
  return <td className={`px-3 py-3 whitespace-nowrap ${className}`}>{children}</td>
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

function CompactValue({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0">
      <dt className="text-[0.6875rem] text-muted-foreground">{label}</dt>
      <dd className="mt-1 truncate font-mono text-xs font-semibold">{value}</dd>
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
