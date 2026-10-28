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
  { value: "newPatientBookings", label: "New-patient booking calls" },
  { value: "existingPatientBookings", label: "Existing-patient booking calls" },
] as const

type Metric = typeof metrics[number]["value"]
type IntentMetric = "newPatientBookings" | "existingPatientBookings"

const intentSeries = [
  { key: "booked", label: "Booked", color: "var(--chart-4)" },
  { key: "notBooked", label: "Not booked", color: "var(--chart-2)" },
]

function intentCounts(day: OperatorAiAnalyticsDay, metric: IntentMetric) {
  return metric === "newPatientBookings"
    ? { calls: day.newPatientIntentCalls, booked: day.newPatientIntentBookings }
    : { calls: day.existingPatientIntentCalls, booked: day.existingPatientIntentBookings }
}

export function intentBookingDay(day: OperatorAiAnalyticsDay, metric: IntentMetric) {
  const { calls, booked } = intentCounts(day, metric)
  return { date: day.date, booked, notBooked: calls - booked }
}

export function intentBookingTotals(days: OperatorAiAnalyticsDay[], metric: IntentMetric) {
  return days.reduce((total, day) => {
    const { calls, booked } = intentCounts(day, metric)
    return { calls: total.calls + calls, booked: total.booked + booked }
  }, { calls: 0, booked: 0 })
}

function isIntentMetric(metric: Metric): metric is IntentMetric {
  return metric === "newPatientBookings" || metric === "existingPatientBookings"
}

export function overviewDayValue(day: OperatorAiAnalyticsDay, metric: Exclude<Metric, IntentMetric>): number | null {
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
  let description = `${selected.label}${metric === "average" || percent ? " · Days without a denominator have no bar." : ""}`
  if (isIntentMetric(metric)) {
    const { calls, booked } = intentBookingTotals(days, metric)
    description = calls
      ? `${selected.label} · ${booked.toLocaleString()} of ${calls.toLocaleString()} booked (${(booked / calls).toLocaleString(undefined, { style: "percent", maximumFractionDigits: 1 })})`
      : `${selected.label} · No calls with this intent`
  }
  return (
    <Card role="region" aria-label="Summary trend" className="mt-5">
      <CardHeader>
        <CardTitle>Over time</CardTitle>
        <CardDescription>{description}</CardDescription>
        <CardAction><OptionSelect label="Trend metric" value={metric} items={[...metrics]} onChange={setMetric} /></CardAction>
      </CardHeader>
      <CardContent>
        {isIntentMetric(metric)
          ? <DailyBarChart label={`${selected.label} over time`} data={days.map((day) => intentBookingDay(day, metric))} series={intentSeries} />
          : <DailyBarChart label={`${selected.label} over time`} data={days.map((day) => ({ date: day.date, value: overviewDayValue(day, metric) }))} series={[{ key: "value", label: selected.label, color: "var(--chart-1)" }]} format={format} percent={percent} />}
      </CardContent>
    </Card>
  )
}
