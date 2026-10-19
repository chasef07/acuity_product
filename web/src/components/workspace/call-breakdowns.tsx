"use client"

import { ArrowRightIcon, CircleAlertIcon } from "lucide-react"
import type { OperatorAiAnalyticsDistributionCounts, OperatorAiAnalyticsDistributions } from "@/lib/api/generated/types.gen"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card"
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { DailyBarChart } from "@/components/analytics/daily-bar-chart"
import { cn } from "@/lib/utils"
import { CallLabelFilters, intentLabels, outcomeLabels, type IntentFilter, type OutcomeFilter } from "./call-labels"
import { CallFlowOverview, type CallsFilter, type FlowSelection } from "./call-flow-overview"

type Kind = "intents" | "outcomes" | "failures"
export type BreakdownSelection = { kind: Kind; intent: IntentFilter; outcome: OutcomeFilter }
export const initialBreakdownSelection: BreakdownSelection = { kind: "intents", intent: "all", outcome: "all" }

export function BreakdownControls({ selection, onSelection }: { selection: BreakdownSelection; onSelection: (selection: BreakdownSelection) => void }) {
  return <div className="mb-5 flex flex-wrap items-center justify-between gap-3">
    <ToggleGroup aria-label="Breakdown" variant="segmented" size="sm" value={[selection.kind]} onValueChange={(values) => {
      const kind = values[0] as Kind | undefined
      if (kind) onSelection(kind === "intents" ? initialBreakdownSelection : { ...selection, kind, outcome: kind === "outcomes" ? "all" : selection.outcome })
    }}>
      {views.map((view) => <ToggleGroupItem key={view.value} value={view.value}>{view.label}</ToggleGroupItem>)}
    </ToggleGroup>
    <div className="flex flex-wrap items-center gap-2">
      <CallLabelFilters intent={selection.intent} outcome={selection.outcome}
        onIntent={(intent) => onSelection({ kind: "outcomes", intent, outcome: "all" })}
        onOutcome={(outcome) => onSelection({ ...selection, kind: "failures", outcome })} />
      {(selection.intent !== "all" || selection.outcome !== "all") && <Button variant="ghost" size="sm" onClick={() => onSelection(initialBreakdownSelection)}>Reset filters</Button>}
    </div>
  </div>
}
type DistributionRow = { key: string; label: string; calls: number; unlabeled?: boolean }
const views: { value: Kind; label: string }[] = [{ value: "intents", label: "Intents" }, { value: "outcomes", label: "Outcomes" }, { value: "failures", label: "Judge failures" }]
const colors = ["var(--chart-1)", "var(--chart-2)", "var(--chart-3)", "var(--chart-4)", "var(--chart-5)", "var(--foreground)", "var(--muted-foreground)"]

function rowsFor(data: OperatorAiAnalyticsDistributionCounts, kind: Kind): DistributionRow[] {
  const rows: DistributionRow[] = kind === "intents"
    ? data.intents.map((item) => ({ key: item.intent, label: intentLabels[item.intent], calls: item.calls }))
    : kind === "outcomes"
      ? data.outcomes.map((item) => ({ key: item.outcome, label: outcomeLabels[item.outcome], calls: item.calls }))
      : data.failures.map((item) => ({ key: item.key, label: item.label, calls: item.calls }))
  const missing = kind === "intents" ? data.unlabeledIntentCalls : kind === "outcomes" ? data.unlabeledOutcomeCalls : 0
  rows.sort((a, b) => b.calls - a.calls || a.label.localeCompare(b.label))
  if (missing) rows.push({ key: "unlabeled", label: "Unlabeled", calls: missing, unlabeled: true })
  return rows
}

export function CallBreakdowns({ data, selection, onSelection, onRetry, onViewCalls }: {
  data?: OperatorAiAnalyticsDistributions
  selection: BreakdownSelection
  onSelection: (selection: BreakdownSelection) => void
  onRetry: () => void
  onViewCalls: (filter: CallsFilter) => void
}) {
  if (!data || data.totalCalls === undefined) return (
    <Alert>
      <CircleAlertIcon aria-hidden="true" />
      <AlertTitle>Breakdowns unavailable</AlertTitle>
      <AlertDescription><p>Distribution data could not be loaded.</p><Button size="sm" variant="outline" onClick={onRetry}>Retry</Button></AlertDescription>
    </Alert>
  )
  if (!data.totalCalls) return <Empty><EmptyHeader><EmptyTitle>No calls in this selection</EmptyTitle><EmptyDescription>Change a filter, date range, or office.</EmptyDescription></EmptyHeader></Empty>
  const unfiltered = selection.kind === "intents" && selection.intent === "all" && selection.outcome === "all"
  const explore = (flow: FlowSelection) => onSelection(flow.intent
    ? { kind: "outcomes", intent: flow.intent, outcome: "all" }
    : { kind: "failures", intent: "all", outcome: flow.outcome ?? "all" })
  return <>
    {unfiltered && <CallFlowOverview data={data} onSelect={explore} onViewCalls={onViewCalls} />}
    <BreakdownView data={data} kind={selection.kind} onSelect={(value) => onSelection(selection.kind === "intents"
      ? { kind: "outcomes", intent: value as IntentFilter, outcome: "all" }
      : { ...selection, kind: "failures", outcome: value as OutcomeFilter })} />
  </>
}

export function BreakdownView({ data, kind, onSelect }: { data: OperatorAiAnalyticsDistributions; kind: Kind; onSelect?: (value: string) => void }) {
  const totalCalls = data.totalCalls
  const rows = rowsFor(data, kind)
  const flags = rows.reduce((total, item) => total + item.calls, 0)
  const total = kind === "failures" ? flags : totalCalls
  const title = views.find((view) => view.value === kind)!.label
  const detail = kind === "failures" ? `${flags.toLocaleString()} flags across ${data.failedCalls.toLocaleString()} calls` : `${totalCalls.toLocaleString()} calls · share of selected calls`
  const footnote = kind === "failures"
    ? `${data.evaluatedCalls.toLocaleString()} of ${totalCalls.toLocaleString()} calls evaluated · ${data.unevaluatedCalls.toLocaleString()} unevaluated. Counts are No answers to these checks. Percentages show share of failure flags; a call can have more than one. Evaluated means at least one failure check has an answer.`
    : "Select a category to explore it. Unlabeled calls are included in the total."
  const known = rows.filter((row) => !row.unlabeled)
  const top = known.slice(0, 5)
  const remaining = known.slice(5)
  const groups = [
    ...top.map((row) => ({ label: row.label, keys: [row.key] })),
    ...(remaining.length ? [{ label: "Remaining categories", keys: remaining.map((row) => row.key) }] : []),
    ...(rows.some((row) => row.unlabeled) ? [{ label: "Unlabeled", keys: ["unlabeled"] }] : []),
  ]
  const series = groups.map((group, index) => ({ key: `series${index}`, label: group.label, color: group.label === "Unlabeled" ? "var(--muted-foreground)" : colors[index] }))
  const daily = data.daily?.map((day) => {
    const counts = new Map(rowsFor(day, kind).map((row) => [row.key, row.calls]))
    return { date: day.date, ...Object.fromEntries(groups.map((group, index) => [`series${index}`, group.keys.reduce((sum, key) => sum + (counts.get(key) ?? 0), 0)])) }
  })
  return (
    <div className="grid items-start gap-5 xl:grid-cols-[minmax(0,0.8fr)_minmax(0,1.2fr)]">
      <Card role="region" aria-label={title}>
        <CardHeader><CardTitle>{title}</CardTitle><CardDescription>{detail}</CardDescription></CardHeader>
        <CardContent>
          {rows.length ? <dl className="flex flex-col gap-5">
            {rows.map((item) => {
              const share = total ? item.calls / total : 0
              return <div key={item.key}>
                <div className="flex items-baseline justify-between gap-4 text-xs">
                  <dt className={cn(item.unlabeled && "text-muted-foreground")}>{onSelect && kind !== "failures" && !item.unlabeled
                    ? <Button variant="link" size="sm" className="h-auto max-w-full justify-start p-0 text-left whitespace-normal" aria-label={`Explore ${item.label}`} onClick={() => onSelect(item.key)}>{item.label}<ArrowRightIcon data-icon="inline-end" /></Button>
                    : item.label}</dt>
                  <dd className="flex shrink-0 gap-3 tabular-nums"><span className="text-muted-foreground">{item.calls.toLocaleString()} {item.calls === 1 ? "call" : "calls"}</span><span className="w-11 text-right font-medium">{share.toLocaleString(undefined, { style: "percent", maximumFractionDigits: 1 })}</span></dd>
                </div>
                <div aria-hidden="true" className="mt-2 h-1.5 overflow-hidden rounded-full bg-muted"><div className={cn("h-full rounded-full", item.unlabeled ? "bg-muted-foreground/30" : "bg-foreground/75")} style={{ width: `${share * 100}%` }} /></div>
              </div>
            })}
          </dl> : <p className="text-sm text-muted-foreground">{data.evaluatedCalls ? "No judge failures recorded." : "No evaluated calls in this selection."}</p>}
        </CardContent>
        <CardFooter><p className="text-[11px] leading-relaxed text-muted-foreground">{footnote}</p></CardFooter>
      </Card>
      <Card role="region" aria-label={`${title} trend`}>
        <CardHeader><CardTitle>Over time</CardTitle><CardDescription>{kind === "failures" ? "Failure flags per day" : "Calls per day"}{remaining.length ? " · Top five categories; remaining grouped." : ""}</CardDescription></CardHeader>
        <CardContent>
          {!daily?.length ? <p className="text-sm text-muted-foreground">Daily breakdowns unavailable.</p> : !series.length ? <p className="text-sm text-muted-foreground">No recorded failure flags to chart. Missing evaluations are not passes.</p> : <DailyBarChart label={`${title} over time`} data={daily} series={series} />}
        </CardContent>
      </Card>
    </div>
  )
}
