"use client"

import {
  Bar,
  CartesianGrid,
  BarChart,
  XAxis,
  YAxis,
} from "recharts"

import {
  ChartContainer,
  ChartTooltip,
  type ChartConfig,
} from "@/components/ui/chart"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import {
  formatDay,
  type BookingDay,
  type BookingAnalytics,
  type BookingMetric,
  type BookingSummary,
  type PatientGroup,
} from "@/lib/booking-analytics"
import { formatDuration } from "@/lib/vocabulary"
import { useReducedMotion } from "@/lib/reduced-motion"
import { dailyTotalComparison } from "@/lib/analytics-trend"
import styles from "./booking-overview.module.css"

type Metric = BookingMetric
const chartConfig = {
  total: {
    label: "All patients",
    color: "var(--muted-foreground)",
  },
  new: { label: "New patients", color: "var(--booking-new)" },
  existing: { label: "Existing patients", color: "var(--booking-existing)" },
  unknown: { label: "Unclassified", color: "var(--muted-foreground)" },
} satisfies ChartConfig
const visibleGroups = ["new", "existing"] as const
const count = (value: number) => value.toLocaleString("en-US")
const minutes = (value: number) =>
  value.toLocaleString("en-US", { maximumFractionDigits: 1 })

type Series = keyof typeof chartConfig

function chartValue(summary: BookingSummary, metric: Metric) {
  return metric === "duration"
    ? summary.p50
    : metric === "volume"
      ? summary.calls
      : summary.bookings
}

function GroupLabel({ group }: { group: keyof typeof chartConfig }) {
  return (
    <span className={styles.groupLabel}>
      <span
        className={styles.dot}
        style={{ background: chartConfig[group].color }}
      />
      {chartConfig[group].label}
    </span>
  )
}

function DayTooltip({
  active,
  payload,
  metric,
  series,
}: {
  active?: boolean
  payload?: ReadonlyArray<{ payload?: BookingDay }>
  metric: Metric
  series: readonly Series[]
}) {
  const day = payload?.[0]?.payload
  if (!active || !day) return null
  return (
    <div className={styles.tooltip}>
      <p className={styles.tooltipDate}>{formatDay(day.day)}</p>
      {series.map((cohort) => {
        const summary = day[cohort]
        return (
          <div className={styles.tooltipGroup} key={cohort}>
            <div className={styles.tooltipRow}>
              <GroupLabel group={cohort} />
              <strong>
                {metric === "bookings"
                  ? count(summary.bookings)
                  : metric === "volume"
                    ? `${count(summary.calls)} ${summary.calls === 1 ? "call" : "calls"}`
                    : summary.p50 === null
                      ? "No duration samples"
                      : formatDuration(summary.p50)}
              </strong>
            </div>
            {metric === "volume" && summary.calls > 0 && (
              <span className={styles.tooltipSub}>
                {minutes(summary.minutes)} min
              </span>
            )}
            {metric === "duration" && summary.durationSamples > 0 && (
              <span className={styles.tooltipSub}>
                p50 · {count(summary.durationSamples)} calls
              </span>
            )}
          </div>
        )
      })}
    </div>
  )
}

function BookingTrend({
  daily,
  metric,
  series,
}: {
  daily: BookingDay[]
  metric: Metric
  series: readonly Series[]
}) {
  const reducedMotion = useReducedMotion()
  return (
    <ChartContainer
      config={chartConfig}
      className="h-[300px] w-full aspect-auto"
      aria-label={
        metric === "bookings"
          ? "Daily confirmed bookings"
          : metric === "volume"
            ? "Daily calls by patient status"
            : "Daily p50 duration by patient status"
      }
    >
      <BarChart
        accessibilityLayer
        data={daily}
        margin={{ top: 20, right: 16, bottom: 8, left: 8 }}
      >
        <CartesianGrid
          vertical={false}
          stroke="var(--border)"
          strokeOpacity={0.65}
        />
        <XAxis
          height={40}
          dataKey="day"
          tickFormatter={formatDay}
          axisLine={false}
          tickLine={false}
          tickMargin={14}
          minTickGap={48}
        />
        <YAxis
          domain={[0, "auto"]}
          tickFormatter={(value: number) =>
            metric === "duration" ? formatDuration(value) : String(value)
          }
          axisLine={false}
          tickLine={false}
          width="auto"
          tickMargin={10}
          allowDecimals={false}
        />
        <ChartTooltip
          content={<DayTooltip metric={metric} series={series} />}
          cursor={{ stroke: "var(--muted-foreground)", strokeWidth: 1 }}
        />
        {series.map((cohort) => (
          <Bar
            key={cohort}
            name={chartConfig[cohort].label}
            dataKey={(day: BookingDay) => chartValue(day[cohort], metric)}
            fill={`var(--color-${cohort})`}
            stackId={metric === "duration" ? undefined : metric}
            maxBarSize={36}
            isAnimationActive={!reducedMotion}
          />
        ))}
      </BarChart>
    </ChartContainer>
  )
}

function VolumeSummary({
  total,
  groups,
}: {
  total: BookingSummary
  groups: Record<PatientGroup, BookingSummary>
}) {
  return (
    <div className={styles.summary}>
      <div>
        <p className={styles.summaryLabel}>Total calls</p>
        <output className={styles.headline} aria-label="Total calls">
          {count(total.calls)}
        </output>
      </div>
      <div>
        <p className={styles.summaryLabel}>Total minutes</p>
        <output className={styles.headline} aria-label="Total minutes">
          {minutes(total.minutes)}
        </output>
      </div>
      <div className={styles.cohorts}>
        {visibleGroups.map((group) => {
          const summary = groups[group]
          return (
            <div key={group} className={styles.cohort}>
              <div className={styles.cohortHeading}>
                <GroupLabel group={group} />
                <strong>{count(summary.calls)}</strong>
              </div>
              <p>{minutes(summary.minutes)} min</p>
            </div>
          )
        })}
      </div>
    </div>
  )
}

function Summary({
  total,
  groups,
  metric,
}: {
  total: BookingSummary
  groups: Record<PatientGroup, BookingSummary>
  metric: Metric
}) {
  if (metric === "volume")
    return <VolumeSummary total={total} groups={groups} />
  const label =
    metric === "bookings" ? "Confirmed bookings" : "Median call duration"
  return (
    <div className={styles.summary}>
      <div>
        <p className={styles.summaryLabel}>{label}</p>
        <output className={styles.headline} aria-label={label}>
          {metric === "bookings"
            ? count(total.bookings)
            : formatDuration(total.p50)}
        </output>
        {metric === "duration" && (
          <p className={styles.summaryCaption}>
            p50 · {count(total.durationSamples)} calls
          </p>
        )}
      </div>
      <div className={styles.cohorts}>
        {visibleGroups.map((group) => {
          const summary = groups[group]
          return (
            <div key={group} className={styles.cohort}>
              <div className={styles.cohortHeading}>
                <GroupLabel group={group} />
                <strong>
                  {metric === "bookings"
                    ? count(summary.bookings)
                    : formatDuration(summary.p50)}
                </strong>
              </div>
              <p>
                {metric === "bookings"
                  ? `${total.bookings ? ((summary.bookings / total.bookings) * 100).toFixed(1) : "0"}% of bookings`
                  : `p50 · ${count(summary.durationSamples)} calls`}
              </p>
            </div>
          )
        })}
      </div>
      {metric === "duration" && (
        <p className={styles.summaryNote}>
          From call start to hang-up, for booking attempts.
        </p>
      )}
    </div>
  )
}

function VolumeBreakdown({ report }: { report: BookingAnalytics }) {
  const rows = [
    ...visibleGroups.map((group) => ({
      key: group,
      label: <GroupLabel group={group} />,
      summary: report.groups[group],
    })),
    { key: "total", label: <strong>Total</strong>, summary: report.total },
  ]
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Patient status</TableHead>
          <TableHead className="text-right">Calls</TableHead>
          <TableHead className="text-right">Minutes</TableHead>
          <TableHead className="text-right">Avg. / call</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {rows.map(({ key, label, summary }) => (
          <TableRow key={key}>
            <TableCell>{label}</TableCell>
            <TableCell className="text-right tabular-nums">
              {count(summary.calls)}
            </TableCell>
            <TableCell className="text-right tabular-nums">
              {minutes(summary.minutes)}
            </TableCell>
            <TableCell className="text-right tabular-nums">
              {formatDuration(
                summary.calls ? (summary.minutes * 60) / summary.calls : null,
              )}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}

function BookingBreakdown({ report }: { report: BookingAnalytics }) {
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Patient status</TableHead>
          <TableHead className="text-right">Bookings</TableHead>
          <TableHead className="text-right">p50 duration</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {visibleGroups.map((group) => (
          <TableRow key={group}>
            <TableCell>
              <GroupLabel group={group} />
            </TableCell>
            <TableCell className="text-right tabular-nums">
              {count(report.groups[group].bookings)}
            </TableCell>
            <TableCell className="text-right tabular-nums">
              {formatDuration(report.groups[group].p50)}
            </TableCell>
          </TableRow>
        ))}
        <TableRow>
          <TableCell>
            <strong>Total</strong>
          </TableCell>
          <TableCell className="text-right tabular-nums">
            {count(report.total.bookings)}
          </TableCell>
          <TableCell className="text-right tabular-nums">
            {formatDuration(report.total.p50)}
          </TableCell>
        </TableRow>
      </TableBody>
    </Table>
  )
}

export function BookingOverview({
  report,
  metric,
}: {
  report: BookingAnalytics
  metric: Metric
}) {
  const partialBreakdown = report.groups.unknown.calls > 0
  const series: readonly Series[] =
    metric === "duration"
      ? partialBreakdown ? ["total", ...visibleGroups] : visibleGroups
      : partialBreakdown ? [...visibleGroups, "unknown"] : visibleGroups
  return (
    <>
      <section className={styles.hero} aria-label="Booking performance">
        <Summary total={report.total} groups={report.groups} metric={metric} />
        <div className={styles.chartSection}>
          <div className={styles.chartHeading}>
            <h2>
              {metric === "bookings"
                ? "Bookings"
                : metric === "volume"
                  ? "Calls"
                  : "Median call duration"}
            </h2>
            <div className={styles.chartLegend}>
              {series.map((group) => <GroupLabel key={group} group={group} />)}
            </div>
          </div>
          <BookingTrend daily={report.daily} metric={metric} series={series} />
          <p className={styles.chartCaption}>
            {metric === "duration"
              ? "Days without measurements are blank. · "
              : `${dailyTotalComparison(report.daily.map((day) => chartValue(day.total, metric)))} · `}
            Today is excluded.
          </p>
        </div>
      </section>

      <section className={styles.breakdown} aria-label="Breakdown">
        <div className={styles.sectionHeading}>
          <div>
            <h2>Breakdown</h2>
            {partialBreakdown && (
              <p className={styles.summaryCaption}>
                New/existing breakdown covers{" "}
                {count(report.total.calls - report.groups.unknown.calls)} of{" "}
                {count(report.total.calls)} calls. Totals include all calls.
              </p>
            )}
          </div>
        </div>
        {metric === "volume" ? (
          <VolumeBreakdown report={report} />
        ) : (
          <BookingBreakdown report={report} />
        )}
      </section>
    </>
  )
}
