"use client"

import { useEffect, useState } from "react"
import {
  Bar,
  Cell,
  CartesianGrid,
  BarChart,
  XAxis,
  YAxis,
} from "recharts"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { ChartContainer, ChartTooltip } from "@/components/ui/chart"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { portalClient } from "@/lib/api/client"
import { queryOperatorAiCosts } from "@/lib/api/generated/sdk.gen"
import type {
  OperatorAiAnalyticsRange,
  OperatorAiCostAnalytics,
  OperatorAiCostDay,
} from "@/lib/api/generated/types.gen"
import { getAccessToken } from "@/lib/auth-client"
import { formatDay, formatPercent } from "@/lib/booking-analytics"
import { useReducedMotion } from "@/lib/reduced-motion"
import { dailyTotalComparison } from "@/lib/analytics-trend"
import styles from "./booking-overview.module.css"

function dollars(value: number | null) {
  if (value === null) return "—"
  if (value > 0 && value < 0.0001) return "<$0.0001"
  return new Intl.NumberFormat("en-US", {
    style: "currency",
    currency: "USD",
    minimumFractionDigits: value > 0 && value < 1 ? 4 : 2,
    maximumFractionDigits: value > 0 && value < 1 ? 4 : 2,
  }).format(value)
}

function rateLabel(item: OperatorAiCostAnalytics["items"][number]) {
  const quantity =
    item.rateQuantity === 1_000_000
      ? "1M"
      : item.rateQuantity.toLocaleString("en-US")
  const rate = new Intl.NumberFormat("en-US", {
    style: "currency",
    currency: "USD",
    minimumFractionDigits: item.rateUsd < 0.01 ? 4 : 2,
    maximumFractionDigits: 4,
  }).format(item.rateUsd)
  return `${rate} / ${quantity === "1" ? "" : `${quantity} `}${item.rateUnit}`
}

function fullDate(value: string) {
  return new Intl.DateTimeFormat("en-US", {
    month: "long",
    day: "numeric",
    year: "numeric",
    timeZone: "UTC",
  }).format(new Date(`${value}T00:00:00Z`))
}

function CostTooltip({
  active,
  payload,
}: {
  active?: boolean
  payload?: ReadonlyArray<{ payload?: OperatorAiCostDay }>
}) {
  const day = payload?.[0]?.payload
  if (!active || !day) return null
  return (
    <div className={styles.tooltip}>
      <p className={styles.tooltipDate}>{formatDay(day.day)}</p>
      <div className={styles.tooltipRow}>
        <span>Estimated cost</span>
        <strong>{dollars(day.costUsd)}</strong>
      </div>
      <div className={styles.tooltipRow}>
        <span>Completed calls</span>
        <strong>{day.calls.toLocaleString()}</strong>
      </div>
      {day.pricedCalls < day.calls && (
        <p className={styles.tooltipSub}>
          Complete cost data for {day.pricedCalls.toLocaleString()} of{" "}
          {day.calls.toLocaleString()} calls.
          {day.unpricedUsage > 0 &&
            ` ${day.unpricedUsage.toLocaleString()} usage records could not be priced.`}
        </p>
      )}
    </div>
  )
}

type Request = { key: string } & (
  | { state: "loading" | "unavailable" | "denied" | "busy" }
  | { state: "ready"; report: OperatorAiCostAnalytics }
)

export function CostOverview({
  practiceID,
  locationID,
  range,
}: {
  practiceID: string
  locationID: string
  range: OperatorAiAnalyticsRange
}) {
  const [timeZone] = useState(
    () => Intl.DateTimeFormat().resolvedOptions().timeZone,
  )
  const [revision, setRevision] = useState(0)
  const key = `${practiceID}:${locationID}:${range}:${timeZone}:${revision}`
  const [request, setRequest] = useState<Request>({
    key: "",
    state: "loading",
  })
  const current: Request =
    request.key === key ? request : { key, state: "loading" }
  useEffect(() => {
    const controller = new AbortController()
    async function load() {
      try {
        const token = await getAccessToken()
        if (controller.signal.aborted) return
        if (!token) {
          setRequest({ key, state: "denied" })
          return
        }
        const result = await queryOperatorAiCosts({
          client: portalClient(token),
          body: {
            practiceId: practiceID,
            locationId: locationID || undefined,
            range,
            timeZone,
          },
          signal: controller.signal,
        })
        if (controller.signal.aborted) return
        if (result.data) {
          setRequest({ key, state: "ready", report: result.data })
          return
        }
        const status = result.response?.status
        setRequest({
          key,
          state:
            status === 401 || status === 403
              ? "denied"
              : status === 429
                ? "busy"
                : "unavailable",
        })
      } catch {
        if (!controller.signal.aborted)
          setRequest({ key, state: "unavailable" })
      }
    }
    void load()
    return () => controller.abort()
  }, [key, practiceID, locationID, range, timeZone])
  if (current.state === "loading")
    return (
      <Skeleton
        className="h-80 w-full"
        aria-label="Loading cost analytics"
        aria-busy="true"
      />
    )
  if (current.state !== "ready")
    return (
      <Alert>
        <AlertTitle>
          {current.state === "denied"
            ? "Cost access unavailable"
            : current.state === "busy"
              ? "Analytics is busy"
              : "Costs couldn’t load"}
        </AlertTitle>
        <AlertDescription>
          <p>
            {current.state === "denied"
              ? "AI costs require Platform Operator access."
              : "Try again to load the cost breakdown."}
          </p>
          <Button
            variant="outline"
            onClick={() => setRevision((value) => value + 1)}
          >
            Retry
          </Button>
        </AlertDescription>
      </Alert>
    )
  return <CostReport report={current.report} />
}

function CostReport({ report }: { report: OperatorAiCostAnalytics }) {
  const reducedMotion = useReducedMotion()
  const partial = report.pricedCalls < report.totalCalls
  const items = report.items.filter((item) => item.calls > 0 || item.id === "gpt_live" || item.id.startsWith("luna_"))
  const summaries = [
    { label: "Average cost per call", value: dollars(report.costPerCallUsd) },
    {
      label: "Average cost per minute",
      value: dollars(report.costPerMinuteUsd),
    },
    { label: "Cache savings", value: dollars(report.cacheSavingsUsd) },
  ]
  return (
    <>
      <section className={styles.hero} aria-label="AI cost overview">
        <div className={styles.summary}>
          <div>
            <p className={styles.summaryLabel}>
              {partial ? "Recorded estimated cost" : "Estimated cost"}
            </p>
            <output className={styles.headline} aria-label="Estimated cost">
              {dollars(report.totalCostUsd)}
            </output>
            <p className={styles.summaryCaption}>
              Before credits · {report.totalCalls.toLocaleString()} completed
              calls
            </p>
          </div>
          <div className={styles.cohorts}>
            {summaries.map((item) => (
              <div key={item.label} className={styles.cohort}>
                <div className={styles.cohortHeading}>
                  <span>{item.label}</span>
                  <strong>{item.value}</strong>
                </div>
              </div>
            ))}
          </div>
          <p className={styles.summaryNote}>
            {report.cacheHitRate === null
              ? "Cache hit rate unavailable."
              : `Cache hit rate · ${formatPercent(report.cacheHitRate)}`}
            {partial && (
              <>
                {" "}
                Averages use the {report.pricedCalls.toLocaleString()} of{" "}
                {report.totalCalls.toLocaleString()} calls with complete cost
                data. Missing usage is excluded.
              </>
            )}
            {report.unpricedUsage > 0 && (
              <>
                {" "}
                {report.unpricedUsage.toLocaleString()} usage records could not
                be priced.
              </>
            )}
          </p>
        </div>
        <div className={styles.chartSection}>
          <div className={styles.chartHeading}>
            <h2>Daily cost</h2>
            <span className={styles.summaryCaption}>
              USD · {report.timeZone}
            </span>
          </div>
          <ChartContainer
            config={{
              cost: { label: "Estimated cost", color: "var(--foreground)" },
            }}
            className="h-[300px] w-full aspect-auto"
            aria-label="Daily estimated AI cost in US dollars"
          >
            <BarChart
              accessibilityLayer
              data={report.daily}
              margin={{ top: 20, right: 16, bottom: 8, left: 8 }}
            >
              <CartesianGrid
                vertical={false}
                stroke="var(--border)"
                strokeOpacity={0.65}
              />
              <XAxis
                dataKey="day"
                height={40}
                tickFormatter={formatDay}
                axisLine={false}
                tickLine={false}
                tickMargin={14}
                minTickGap={48}
              />
              <YAxis
                domain={[0, "auto"]}
                tickFormatter={dollars}
                axisLine={false}
                tickLine={false}
                width="auto"
                tickMargin={10}
              />
              <ChartTooltip
                content={<CostTooltip />}
                cursor={{ stroke: "var(--muted-foreground)", strokeWidth: 1 }}
              />
              <Bar
                dataKey="costUsd"
                fill="var(--color-cost)"
                maxBarSize={36}
                isAnimationActive={!reducedMotion}
              >
                {report.daily.map((day) => (
                  <Cell key={day.day} fillOpacity={day.pricedCalls < day.calls ? 0.35 : 1} />
                ))}
              </Bar>
            </BarChart>
          </ChartContainer>
          <p className={styles.chartCaption}>
            {dailyTotalComparison(report.daily.slice(1, -1).map((day) => day.pricedCalls < day.calls ? null : day.costUsd))}
            {" · "}First and last days may be partial. Faded bars have incomplete usage.
          </p>
        </div>
      </section>
      <section className={styles.breakdown} aria-label="Cost breakdown">
        <div className={styles.sectionHeading}>
          <div>
            <h2>Cost breakdown</h2>
            <p className={styles.summaryCaption}>
              Recorded usage at rates verified{" "}
              {fullDate(report.rateEffectiveDate)}. Percentages show each item’s
              share of the recorded estimated cost.
            </p>
          </div>
        </div>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Item</TableHead>
              <TableHead className="text-right">Usage</TableHead>
              <TableHead>Rate</TableHead>
              <TableHead className="text-right">Cost</TableHead>
              <TableHead className="text-right">Share of cost</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.map((item) => (
              <TableRow key={item.id}>
                <TableCell>
                  {item.label}
                  {item.calls < report.totalCalls && (
                    <p className={styles.summaryCaption}>
                      Usage priced for {item.calls} of {report.totalCalls}{" "}
                      calls
                    </p>
                  )}
                </TableCell>
                <TableCell className="text-right tabular-nums">
                  {item.costUsd === null
                    ? "—"
                    : `${item.quantity.toLocaleString("en-US", { maximumFractionDigits: item.unit === "minutes" ? 2 : 0 })} ${item.unit}`}
                </TableCell>
                <TableCell className="text-muted-foreground">
                  {rateLabel(item)}
                </TableCell>
                <TableCell className="text-right tabular-nums">
                  {dollars(item.costUsd)}
                </TableCell>
                <TableCell className="text-right tabular-nums">
                  {item.sharePercent === null
                    ? "—"
                    : `${item.sharePercent.toFixed(1)}%`}
                </TableCell>
              </TableRow>
            ))}
            <TableRow>
              <TableCell colSpan={3}>
                <strong>{partial ? "Recorded total" : "Total"}</strong>
              </TableCell>
              <TableCell className="text-right tabular-nums">
                <strong>{dollars(report.totalCostUsd)}</strong>
              </TableCell>
              <TableCell className="text-right tabular-nums">
                <strong>{report.totalCostUsd > 0 ? "100.0%" : "—"}</strong>
              </TableCell>
            </TableRow>
          </TableBody>
        </Table>
        <p className={styles.summaryCaption}>
          GPT Live uses recorded session seconds at $0.05 per minute, without
          rounding up. Luna uses recorded tokens at Standard short-context rates;
          cache reads and writes are separate from uncached input. Call reports
          above 272,000 input tokens cannot establish the per-request context
          tier, so Luna usage for those calls is unpriced. Long-context rates per
          1M tokens are $0.20 input, $0.02 cached input, $0.25 cache writes, and
          $0.75 output. Missing usage is unknown, not free. LiveKit media and
          Telnyx estimates use call duration. Historical models retain their
          original rates. Estimates exclude tool charges, regional uplifts,
          taxes, and credits.
        </p>
      </section>
    </>
  )
}
