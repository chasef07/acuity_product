"use client"

import { useEffect, useState } from "react"
import {
  Area,
  CartesianGrid,
  ComposedChart,
  Line,
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
    maximumFractionDigits: item.rateUsd < 0.01 ? 4 : 2,
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

function CostDot({
  cx,
  cy,
  payload,
}: {
  cx?: number
  cy?: number
  payload?: OperatorAiCostDay
}) {
  if (cx === undefined || cy === undefined || !payload?.calls) return null
  const partial = payload.pricedCalls < payload.calls
  return (
    <circle
      cx={cx}
      cy={cy}
      r={3}
      fill={partial ? "var(--background)" : "var(--color-cost)"}
      stroke="var(--color-cost)"
      strokeWidth={partial ? 2 : 0}
      aria-hidden="true"
    />
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
              ? "Cache usage unavailable."
              : `${formatPercent(report.cacheHitRate)} of recorded input tokens served from cache.`}
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
            <ComposedChart
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
              <Area
                type="monotone"
                dataKey="costUsd"
                fill="var(--color-cost)"
                fillOpacity={0.055}
                stroke="none"
                tooltipType="none"
                isAnimationActive={!reducedMotion}
              />
              <Line
                type="monotone"
                dataKey="costUsd"
                stroke="var(--color-cost)"
                strokeWidth={2}
                dot={<CostDot />}
                activeDot={{
                  r: 4,
                  strokeWidth: 2,
                  stroke: "var(--background)",
                }}
                isAnimationActive={!reducedMotion}
              />
            </ComposedChart>
          </ChartContainer>
        </div>
      </section>
      <section className={styles.breakdown} aria-label="Cost breakdown">
        <div className={styles.sectionHeading}>
          <div>
            <h2>Cost breakdown</h2>
            <p className={styles.summaryCaption}>
              Recorded usage at rates effective{" "}
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
            {report.items.map((item) => (
              <TableRow key={item.id}>
                <TableCell>
                  {item.label}
                  {item.calls < report.totalCalls && (
                    <p className={styles.summaryCaption}>
                      Usage recorded for {item.calls} of {report.totalCalls}{" "}
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
          AssemblyAI uses recorded audio time. LiveKit media and Telnyx
          estimates use call duration. Cached input is billed separately from
          uncached input.
        </p>
      </section>
    </>
  )
}
