"use client"

import { useState } from "react"
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
} from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { AnalyticsFrame } from "@/components/analytics/analytics-layout"
import {
  DiagnosticsCallTrends,
  DiagnosticsTools,
  type DiagnosticFocus,
  type SelectDiagnostic,
} from "@/components/analytics/ai-diagnostics"
import { DiagnosticsQuality } from "@/components/analytics/agent-quality"
import {
  defaultVersionSelection,
  VersionToolbar,
  type VersionSelection,
  type VersionView,
} from "@/components/analytics/version-markers"
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
import { PendingCallIssues } from "@/components/workspace/call-issue-review"
import type {
  OperatorAiCallIssue,
  OperatorAiCallTags,
  OperatorAiAnalyticsRange,
  OperatorAiAnalyticsSummary,
  OperatorAiCallAnalytics,
  Location,
} from "@/lib/api/generated/types.gen"
import {
  type AiCallAnalytics,
  type AiCallLedger,
  useAiCallAnalytics,
} from "@/lib/clients/agent-calls"

type AnalyticsNextPageState = AiCallLedger["nextPage"]
type AnalyticsTab = "overview" | "cost" | "quality" | "tools" | "calls" | "review"

const analyticsTabs: Array<{ value: AnalyticsTab; label: string }> = [
  { value: "calls", label: "Calls" },
  { value: "review", label: "Needs review" },
  { value: "overview", label: "Overview" },
  { value: "cost", label: "Cost" },
  { value: "quality", label: "Quality" },
  { value: "tools", label: "Tools" },
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
  const [tab, setTab] = useState<AnalyticsTab>("calls")
  const [versionSelection, setVersionSelection] = useState<VersionSelection>(defaultVersionSelection)
  const costView = tab === "cost"
  const needsReviewOnly = tab === "review"
  const [tagFilter, setTagFilter] = useState({ practiceID, value: "" })
  const manualTag = tagFilter.practiceID === practiceID ? tagFilter.value : ""
  const setManualTag = (value: string) => setTagFilter({ practiceID, value })
  const activeTag = tab === "calls" || needsReviewOnly ? manualTag : ""
  const [requestVersion, setRequestVersion] = useState(0)
  const [selectedCall, setSelectedCall] = useState<{ id: string; focus?: DiagnosticFocus }>({ id: "" })
  const selectCall: SelectDiagnostic = (id, focus) => setSelectedCall({ id, focus })
  const requestKey = `${practiceID}:${locationID}:${range}:${activeTag}:${needsReviewOnly}:${requestVersion}`
  const currentRequest = useAiCallAnalytics({
    practiceID,
    locationID,
    range,
    manualTag: activeTag,
    needsReviewOnly,
    revision: requestVersion,
    enabled: !costView,
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

  function updateCallTags(id: string, tags: OperatorAiCallTags) {
    currentRequest.update((shown) => ({
      ...shown,
      availableTags: tags.available,
      calls: shown.calls.map((call) => call.id === id ? { ...call, manualTags: tags.selected } : call),
    }))
  }

  function updateCallIssue(issue: OperatorAiCallIssue) {
    currentRequest.update((shown) => ({
      ...shown,
      pendingIssues: shown.pendingIssues?.filter((item) => item.interactionId !== issue.interactionId),
    }))
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
        tabs={<div className="w-full space-y-3">
          <AnalyticsTabs tab={tab} onChange={setTab} />
          {(tab === "calls" || needsReviewOnly) && ((currentRequest.data?.availableTags?.length ?? 0) > 0 || manualTag) && (
            <div aria-label="Filter calls by tag" className="flex flex-wrap items-center gap-1.5">
              <span className="mr-1 text-xs text-muted-foreground">Tags</span>
              <Button size="xs" variant={manualTag ? "outline" : "secondary"} aria-pressed={!manualTag} onClick={() => setManualTag("")}>All calls</Button>
              {Array.from(new Set([...(currentRequest.data?.availableTags ?? []), ...(manualTag ? [manualTag] : [])])).map((tag) => (
                <Button key={tag} size="xs" variant={manualTag === tag ? "secondary" : "outline"} aria-pressed={manualTag === tag} onClick={() => setManualTag(tag)}>{tag}</Button>
              ))}
            </div>
          )}
        </div>}
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
                locations={locations}
                versionSelection={versionSelection}
                onVersionSelectionChange={setVersionSelection}
                tab={tab}
                range={range}
                nextPageState={nextPageState}
                onLoadNextPage={loadNextPage}
                onSelect={selectCall}
              />
            )}
          </>
        )}
      </AnalyticsFrame>
      <OperatorAnalyticsDetailSheet
        interactionID={selectedCall.id}
        focus={selectedCall.focus}
        onPrevious={previousCall ? () => selectCall(previousCall.id) : undefined}
        onNext={nextCall || canLoadNextCall ? selectNextCall : undefined}
        navigationLoading={nextPageState === "loading"}
        navigationError={canLoadNextCall && nextPageState === "unavailable"}
        onTagsChange={updateCallTags}
        onIssueChange={updateCallIssue}
        onClose={() => {
          setSelectedCall({ id: "" })
          if (manualTag) setRequestVersion((current) => current + 1)
        }}
      />
    </section>
  )
}

function AnalyticsReady({
  data,
  locations,
  versionSelection,
  onVersionSelectionChange,
  tab,
  range,
  nextPageState,
  onLoadNextPage,
  onSelect,
}: {
  data: AiCallAnalytics
  locations: Location[]
  versionSelection: VersionSelection
  onVersionSelectionChange: (selection: VersionSelection) => void
  tab: AnalyticsTab
  range: OperatorAiAnalyticsRange
  nextPageState: AnalyticsNextPageState
  onLoadNextPage: () => void
  onSelect: SelectDiagnostic
}) {
  const versionView: VersionView = {
    versions: data.summary.versions,
    selection: versionSelection,
    locations,
  }
  return (
    <>
      {(tab === "overview" || tab === "quality") && (
        <VersionToolbar view={versionView} onChange={onVersionSelectionChange} />
      )}
      {tab === "overview" && <AnalyticsOverview summary={data.summary} versionView={versionView} />}
      {tab === "quality" && <DiagnosticsQuality summary={data.summary} versionView={versionView} />}
      {tab === "tools" && <DiagnosticsTools summary={data.summary} onSelect={onSelect} />}
      {(tab === "calls" || tab === "review") && data.calls.length === 0 ? (
        <Empty className="mt-5 min-h-72 border bg-card sm:mt-6">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <InboxIcon aria-hidden="true" />
            </EmptyMedia>
            <EmptyTitle>{tab === "review" ? "No flagged calls in this range" : "No AI calls in this range"}</EmptyTitle>
            <EmptyDescription>
              Change the time range or workspace office to review another slice
              of call evidence.
            </EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : tab === "calls" || tab === "review" ? (
        <CallLedger
          calls={data.calls}
          totalCalls={tab === "review" ? undefined : data.summary.totalCalls}
          needsReviewOnly={tab === "review"}
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

function AnalyticsOverview({
  summary,
  versionView,
}: {
  summary: OperatorAiAnalyticsSummary
  versionView: VersionView
}) {
  const outcomes = [
    {
      label: "Total calls",
      value: summary.totalCalls.toLocaleString(),
      note: "All calls in selected range",
      icon: ChartNoAxesCombinedIcon,
    },
    {
      label: "Call minutes",
      value: Math.round(summary.totalCallMinutes).toLocaleString(),
      note: "Completed AI call time",
      icon: Clock3Icon,
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
      <div className="grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-5">
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

      <DiagnosticsCallTrends summary={summary} versionView={versionView} />

      <div className="mt-3 grid gap-3 lg:grid-cols-2">
        <OperationalHealth summary={summary} />
      </div>
    </div>
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
  needsReviewOnly,
  calls,
  totalCalls,
  range,
  hasNextPage,
  nextPageState,
  onLoadNextPage,
  onSelect,
}: {
  calls: OperatorAiCallAnalytics[]
  needsReviewOnly: boolean
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
            {needsReviewOnly ? "Calls needing review" : "AI calls"}
          </h2>
        </div>
        <p className="text-xs text-muted-foreground">
          {calls.length.toLocaleString()} shown{totalCalls !== undefined && <> · {totalCalls.toLocaleString()} total</>}
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
                <TableHead>Duration</TableHead>
                <TableHead>Actions</TableHead>
                <TableHead>Tool errors</TableHead>
                <TableHead>Tags</TableHead>
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
                      aria-label={`Open analytics for call from ${formatDateTime(call.startedAt)}`}
                      onClick={(event) => {
                        event.stopPropagation()
                        onSelect(call.id)
                      }}
                    >
                      {formatDateTime(call.startedAt)}
                    </button>
                  </TableCell>
                  <TableCell className="tabular-nums">
                    {formatPhone(call.phone)}
                    <ReviewReasons reasons={call.reviewReasons} />
                  </TableCell>
                  <TableCell>{call.locationName}</TableCell>
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
                    <div className="flex max-w-56 flex-wrap gap-1">
                      {call.manualTags?.map((tag) => <Badge key={tag} variant="secondary" className="max-w-full whitespace-normal break-words">{tag}</Badge>)}
                    </div>
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
            className={`rounded-xl border p-4 text-left outline-none transition-colors focus-visible:ring-2 focus-visible:ring-ring/40 ${call.reviewReasons?.length ? "border-destructive/25 bg-destructive/5 hover:bg-destructive/10" : "bg-card hover:bg-muted"}`}
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
            <ReviewReasons reasons={call.reviewReasons} />
            <dl className="mt-4 grid grid-cols-2 gap-3 border-y py-3">
              <CompactValue label="Duration" value={formatDuration(call.durationSeconds)} />
              <CompactValue
                label="Tool errors"
                value={String(call.toolErrorCount)}
              />
            </dl>
            <div className="mt-3 flex flex-wrap items-center gap-1.5">
              <ActionBadges actions={call.toolActions} />
              {call.manualTags?.map((tag) => <Badge key={tag} variant="secondary">{tag}</Badge>)}
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
