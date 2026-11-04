"use client"

import { useState } from "react"
import { CartesianGrid, Line, LineChart, XAxis, YAxis } from "recharts"
import type { CallIntent, CallOutcome, OperatorAiAnalyticsDistributionDay } from "@/lib/api/generated/types.gen"
import { DailyBarChart, type DailyBarDatum } from "@/components/analytics/daily-bar-chart"
import { Card, CardContent, CardFooter, CardHeader, CardTitle } from "@/components/ui/card"
import { ChartContainer, ChartTooltip } from "@/components/ui/chart"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { formatDay } from "@/lib/booking-analytics"
import { formatShare, intentLabels, outcomeLabels } from "@/lib/vocabulary"
import { matchesSelection, sumCalls, type CallSelection } from "./call-paths"

const minIntentDayCalls = 5
const maxTrendIntents = 5

const outcomeColors: Record<CallOutcome, string> = {
  resolved: "var(--success)",
  transferred: "var(--outcome-transferred)",
  abandoned: "var(--destructive)",
}
const outcomes = Object.keys(outcomeLabels) as CallOutcome[]
const outcomeSeries = outcomes.map((outcome) => ({ key: outcome, label: outcomeLabels[outcome], color: outcomeColors[outcome] }))

type Days = OperatorAiAnalyticsDistributionDay[]
type TrendView = "overall" | "intent"

export function outcomeShareDays(days: Days, selection: CallSelection): DailyBarDatum[] {
  return days.map((day) => {
    const counted = day.paths.filter((path) => path.outcome && matchesSelection(path, selection))
    const total = sumCalls(counted)
    return {
      date: day.date,
      ...Object.fromEntries(outcomes.map((outcome) => [outcome, total ? sumCalls(counted.filter((path) => path.outcome === outcome)) / total : null])),
    }
  })
}

export function intentResolution(days: Days, selection: CallSelection) {
  const selected = days.map((day) => ({ date: day.date, paths: day.paths.filter((path) => path.intent && path.outcome && matchesSelection(path, selection)) }))
  const volume = new Map<CallIntent, number>()
  for (const day of selected) for (const path of day.paths) volume.set(path.intent!, (volume.get(path.intent!) ?? 0) + path.calls)
  const intents = [...volume].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0])).slice(0, maxTrendIntents).map(([intent]) => intent)
  const resolution = (paths: Days[number]["paths"]) => {
    const calls = sumCalls(paths)
    return { calls, rate: sumCalls(paths.filter((path) => path.outcome === "resolved")) / calls }
  }
  return intents.map((intent) => ({
    intent,
    ...resolution(selected.flatMap((day) => day.paths.filter((path) => path.intent === intent))),
    days: selected.map((day) => {
      const { calls, rate } = resolution(day.paths.filter((path) => path.intent === intent))
      return { date: day.date, calls, rate: calls >= minIntentDayCalls ? rate : null }
    }),
  }))
}

export function ResolutionTrend({ days, selection }: { days: Days; selection: CallSelection }) {
  const [view, setView] = useState<TrendView>("overall")
  const selected = days.flatMap((day) => day.paths.filter((path) => matchesSelection(path, selection)))
  const withOutcome = sumCalls(selected.filter((path) => path.outcome))
  const unlabeled = sumCalls(selected) - withOutcome
  const intents = intentResolution(days, selection)
  return <Card role="region" aria-label="Resolution over time" className="[--outcome-transferred:var(--chart-2)] dark:[--outcome-transferred:var(--chart-4)]">
    <CardHeader className="flex flex-row items-center justify-between gap-3">
      <CardTitle>Resolution over time</CardTitle>
      <ToggleGroup aria-label="Resolution view" variant="segmented" size="sm" value={[view]} onValueChange={(values) => values[0] && setView(values[0] as TrendView)}>
        <ToggleGroupItem value="overall">Overall</ToggleGroupItem>
        <ToggleGroupItem value="intent">By intent</ToggleGroupItem>
      </ToggleGroup>
    </CardHeader>
    <CardContent>
      {view === "overall"
        ? withOutcome ? <DailyBarChart label="Daily outcome share" data={outcomeShareDays(days, selection)} series={outcomeSeries} format={formatShare} percent /> : <Empty message="No calls in this range have an outcome." />
        : intents.length ? <IntentResolution rows={intents} /> : <Empty message="No calls in this range have both an intent and an outcome." />}
    </CardContent>
    {unlabeled > 0 && <CardFooter><p className="text-[11px] text-muted-foreground">Leaves out {unlabeled.toLocaleString()} {unlabeled === 1 ? "call" : "calls"} without an outcome.</p></CardFooter>}
  </Card>
}

function Empty({ message }: { message: string }) {
  return <p className="py-16 text-center text-sm text-muted-foreground">{message}</p>
}

function IntentResolution({ rows }: { rows: ReturnType<typeof intentResolution> }) {
  return <div className="flex min-w-0 flex-col gap-4">
    <div className="grid gap-x-8 gap-y-6 lg:grid-cols-2">
      {rows.map((row) => <section key={row.intent} aria-label={intentLabels[row.intent]} className="min-w-0">
        <div className="flex items-baseline justify-between gap-4 text-xs">
          <h3 className="font-medium">{intentLabels[row.intent]}</h3>
          <p className="shrink-0 tabular-nums text-muted-foreground"><span className="font-medium text-foreground">{formatShare(row.rate)}</span> resolved · {row.calls.toLocaleString()} {row.calls === 1 ? "call" : "calls"}</p>
        </div>
        {row.days.every((point) => point.rate === null) ? <p className="mt-2 flex h-[140px] items-center justify-center rounded-md bg-muted/40 px-4 text-center text-xs text-muted-foreground">Fewer than {minIntentDayCalls} calls on every day, too few to chart.</p> : <ChartContainer config={{ rate: { label: "Resolved", color: "var(--foreground)" } }} className="mt-2 aspect-auto h-[140px] w-full" role="img" aria-label={`${intentLabels[row.intent]} daily resolution rate`}>
          <LineChart accessibilityLayer data={row.days} margin={{ top: 8, right: 12, bottom: 0, left: 0 }}>
            <CartesianGrid vertical={false} stroke="var(--border)" strokeOpacity={0.65} />
            <XAxis dataKey="date" tickFormatter={formatDay} axisLine={false} tickLine={false} tickMargin={8} minTickGap={48} />
            <YAxis domain={[0, 1]} ticks={[0, 0.5, 1]} tickFormatter={formatShare} axisLine={false} tickLine={false} width="auto" />
            <ChartTooltip cursor={{ stroke: "var(--muted-foreground)", strokeWidth: 1 }} content={({ active, payload }) => {
              const point = payload?.[0]?.payload as ReturnType<typeof intentResolution>[number]["days"][number] | undefined
              return active && point ? <div className="rounded-lg border bg-popover p-3 text-xs text-popover-foreground shadow-md">
                <p className="mb-1 font-medium">{point.date} · Eastern</p>
                <p className="tabular-nums">{point.rate === null ? `${point.calls} ${point.calls === 1 ? "call" : "calls"}, too few to rate` : `${formatShare(point.rate)} resolved of ${point.calls} calls`}</p>
              </div> : null
            }} />
            <Line dataKey="rate" stroke="var(--color-rate)" strokeWidth={2} dot={{ r: 3, fill: "var(--color-rate)", strokeWidth: 0 }} connectNulls={false} isAnimationActive={false} />
          </LineChart>
        </ChartContainer>}
      </section>)}
    </div>
    <p className="text-[11px] text-muted-foreground">Share of each intent&apos;s calls resolved per day · Top {maxTrendIntents} intents by calls · Days with fewer than {minIntentDayCalls} calls for an intent are left blank · Eastern time.</p>
    <details>
      <summary className="cursor-pointer text-xs text-muted-foreground">Daily data</summary>
      <Table>
        <TableHeader><TableRow><TableHead>Date · Eastern</TableHead>{rows.map((row) => <TableHead key={row.intent}>{intentLabels[row.intent]}</TableHead>)}</TableRow></TableHeader>
        <TableBody>{rows[0]?.days.map((day, index) => <TableRow key={day.date}><TableCell>{day.date}</TableCell>{rows.map((row) => {
          const point = row.days[index]
          return <TableCell key={row.intent} className="tabular-nums">{point.rate === null ? "—" : formatShare(point.rate)} <span className="text-muted-foreground">({point.calls})</span></TableCell>
        })}</TableRow>)}</TableBody>
      </Table>
    </details>
  </div>
}
