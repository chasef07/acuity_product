"use client"

import { useState } from "react"
import type { OperatorAiAnalyticsDay } from "@/lib/api/generated/types.gen"
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { DailyBarChart } from "@/components/analytics/daily-bar-chart"
import { OptionSelect } from "./manage-agent-page"

const metrics = [
  { value: "calls", label: "Total calls" },
  { value: "booked", label: "Booked appointments" },
  { value: "cancelled", label: "Cancelled appointments" },
  { value: "rescheduled", label: "Rescheduled appointments" },
  { value: "minutes", label: "Total minutes" },
  { value: "average", label: "Avg. time / call" },
  { value: "transfers", label: "Transfer rate" },
  { value: "failures", label: "Tool failure rate" },
] as const

type Metric = typeof metrics[number]["value"]

export function overviewDayValue(day: OperatorAiAnalyticsDay, metric: Metric): number | null {
  switch (metric) {
    case "calls": return day.totalCalls
    case "booked": return day.bookingCount ?? null
    case "cancelled": return day.cancellationCount ?? null
    case "rescheduled": return day.rescheduleCount ?? null
    case "minutes": return day.callMinutes
    case "average": return day.totalCalls ? day.callMinutes / day.totalCalls : null
    case "transfers": return day.totalCalls ? day.transferCount / day.totalCalls : null
    case "failures": return day.toolCallCount && day.toolErrorCount !== undefined ? day.toolErrorCount / day.toolCallCount : null
  }
}

export function OverviewTrend({ days }: { days: OperatorAiAnalyticsDay[] }) {
  const [metric, setMetric] = useState<Metric>("calls")
  const selected = metrics.find((item) => item.value === metric)!
  const percent = metric === "transfers" || metric === "failures"
  const format = (value: number) => percent
    ? value.toLocaleString(undefined, { style: "percent", maximumFractionDigits: 1 })
    : `${value.toLocaleString(undefined, { maximumFractionDigits: 1 })}${metric === "average" ? " min" : ""}`
  return (
    <Card role="region" aria-label="Overview trend" className="mt-5">
      <CardHeader>
        <CardTitle>Over time</CardTitle>
        <CardDescription>{selected.label}{metric === "average" || percent ? " · Days without a denominator have no bar." : ""}</CardDescription>
        <CardAction><OptionSelect label="Trend metric" value={metric} items={[...metrics]} onChange={setMetric} /></CardAction>
      </CardHeader>
      <CardContent>
        <DailyBarChart label={`${selected.label} over time`} data={days.map((day) => ({ date: day.date, value: overviewDayValue(day, metric) }))} series={[{ key: "value", label: selected.label, color: "var(--chart-1)" }]} format={format} percent={percent} />
      </CardContent>
    </Card>
  )
}
