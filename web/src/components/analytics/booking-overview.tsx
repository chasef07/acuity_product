"use client"

import {
  Area,
  CartesianGrid,
  ComposedChart,
  Line,
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
  formatDuration,
  formatPercent,
  type BookingDay,
  type BookingAnalytics,
  type BookingMetric,
  bookingConversionExplanation,
  type BookingSummary,
  type PatientGroup,
} from "@/lib/booking-analytics"
import { useReducedMotion } from "@/lib/reduced-motion"
import { trendDot } from "./trend-dot"
import styles from "./booking-overview.module.css"

type Metric = BookingMetric
const chartConfig = {
  total: {
    label: "All patients",
    color: "var(--booking-total, var(--muted-foreground))",
  },
  new: { label: "New patients", color: "var(--booking-new)" },
  existing: { label: "Existing patients", color: "var(--booking-existing)" },
  unknown: { label: "Unclassified", color: "var(--muted-foreground)" },
} satisfies ChartConfig
const visibleGroups = ["new", "existing"] as const
const count = (value: number) => value.toLocaleString("en-US")

type Series = keyof typeof chartConfig

function GroupLabel({ group }: { group: Series }) {
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
                  : metric === "conversion"
                    ? formatPercent(summary.conversion)
                    : formatDuration(summary.p50)}
              </strong>
            </div>
            {metric === "conversion" && (
              <span className={styles.tooltipSub}>
                {summary.searched === 0
                  ? "No completed availability searches"
                  : `${count(summary.converted)} of ${count(summary.searched)} calls booked`}
              </span>
            )}
            {metric === "duration" && (
              <span className={styles.tooltipSub}>
                p50 · {summary.durationSamples} calls
              </span>
            )}
          </div>
        )
      })}
      {metric === "conversion" &&
        day.total.searchEvidenceCalls < day.total.calls && (
          <span className={styles.tooltipSub}>
            Availability history recorded for{" "}
            {count(day.total.searchEvidenceCalls)} of {count(day.total.calls)}{" "}
            calls.
          </span>
        )}
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
          ? "Daily confirmed bookings by patient status"
          : metric === "conversion"
            ? "Daily booking conversion by patient status"
            : "Daily p50 duration by patient status"
      }
    >
      <ComposedChart
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
          domain={metric === "conversion" ? [0, 100] : [0, "auto"]}
          tickFormatter={(value: number) =>
            metric === "conversion"
              ? `${value}%`
              : metric === "duration"
                ? formatDuration(value)
                : String(value)
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
          <Area
            key={cohort}
            type="monotone"
            dataKey={`${cohort}.${metric === "duration" ? "p50" : metric}`}
            fill={`var(--color-${cohort})`}
            fillOpacity={0.055}
            stroke="none"
            tooltipType="none"
            connectNulls={false}
            isAnimationActive={!reducedMotion}
          />
        ))}
        {series.map((cohort) => (
          <Line
            key={cohort}
            name={chartConfig[cohort].label}
            type="monotone"
            dataKey={`${cohort}.${metric === "duration" ? "p50" : metric}`}
            stroke={`var(--color-${cohort})`}
            strokeWidth={2}
            strokeDasharray={cohort === "total" ? "4 3" : undefined}
            dot={trendDot(
              daily.map((day) =>
                metric === "duration" ? day[cohort].p50 : day[cohort][metric],
              ),
              `var(--color-${cohort})`,
            )}
            activeDot={{ r: 4, strokeWidth: 2, stroke: "var(--background)" }}
            connectNulls={false}
            isAnimationActive={!reducedMotion}
          />
        ))}
      </ComposedChart>
    </ChartContainer>
  )
}

function ConversionSummary({
  total,
  groups,
}: {
  total: BookingSummary
  groups: Record<PatientGroup, BookingSummary>
}) {
  const missingHistory = total.calls - total.searchEvidenceCalls
  return (
    <div className={styles.summary}>
      <div>
        <p className={styles.summaryLabel}>Overall booking conversion</p>
        <output
          className={styles.headline}
          aria-label="Overall booking conversion"
        >
          {formatPercent(total.conversion)}
        </output>
        <p className={styles.summaryCaption}>
          {bookingConversionExplanation(total)}
        </p>
      </div>
      <div className={styles.cohorts}>
        {visibleGroups.map((group) => {
          const summary = groups[group]
          return (
            <div key={group} className={styles.cohort}>
              <div className={styles.cohortHeading}>
                <GroupLabel group={group} />
                <strong>{formatPercent(summary.conversion)}</strong>
              </div>
              <p>
                {count(summary.converted)} of {count(summary.searched)} calls
                booked
              </p>
            </div>
          )
        })}
      </div>
      <p className={styles.summaryNote}>
        Repeated completed searches count once. Searches with no openings remain
        included. Failed searches, reschedules, and cancellations are excluded.
      </p>
      {missingHistory > 0 && (
        <p className={styles.summaryNote}>
          {count(missingHistory)}{" "}
          {missingHistory === 1 ? "call has" : "calls have"} no recorded
          availability history and {missingHistory === 1 ? "is" : "are"}{" "}
          excluded from this rate.
        </p>
      )}
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
  if (metric === "conversion")
    return <ConversionSummary total={total} groups={groups} />
  const label =
    metric === "bookings" ? "Confirmed bookings" : "Median booked-call duration"
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
            p50 · {count(total.durationSamples)} booked calls
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
          From call start to hang-up, for calls that booked.
        </p>
      )}
    </div>
  )
}

function ConversionBreakdown({ report }: { report: BookingAnalytics }) {
  const rows = [
    ...visibleGroups.map((group) => ({
      key: group,
      label: <GroupLabel group={group} />,
      summary: report.groups[group],
    })),
    { key: "total", label: <strong>Total</strong>, summary: report.total },
  ]
  return (
    <section className={styles.breakdown} aria-label="Breakdown">
      <div className={styles.sectionHeading}>
        <div>
          <h2>Conversion by patient status</h2>
          <p className={styles.summaryCaption}>
            New and existing rows cover all {count(report.total.searched)}{" "}
            completed availability searches.
          </p>
        </div>
      </div>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Patient status</TableHead>
            <TableHead className="text-right">Booked calls</TableHead>
            <TableHead className="text-right">
              Completed availability searches
            </TableHead>
            <TableHead className="text-right">Conversion</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map(({ key, label, summary }) => (
            <TableRow key={key}>
              <TableCell>{label}</TableCell>
              <TableCell className="text-right tabular-nums">
                {count(summary.converted)}
              </TableCell>
              <TableCell className="text-right tabular-nums">
                {count(summary.searched)}
              </TableCell>
              <TableCell className="text-right tabular-nums">
                {formatPercent(summary.conversion)}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </section>
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
    metric === "conversion"
      ? ["total", ...visibleGroups]
      : partialBreakdown
        ? ["total", ...visibleGroups]
        : visibleGroups
  return (
    <>
      <section
        className={`${styles.hero} ${metric === "conversion" ? styles.conversion : ""}`}
        aria-label="Booking performance"
      >
        <Summary total={report.total} groups={report.groups} metric={metric} />
        <div className={styles.chartSection}>
          <div className={styles.chartHeading}>
            <h2>
              {metric === "bookings"
                ? "Bookings"
                : metric === "conversion"
                  ? "Daily booking conversion"
                  : "Median booked-call duration"}
            </h2>
            {(metric === "conversion" || partialBreakdown) && (
              <GroupLabel group="total" />
            )}
          </div>
          {metric === "conversion" && (
            <p className={styles.chartCaption}>
              Booked calls ÷ completed availability searches, each day.
            </p>
          )}
          <BookingTrend daily={report.daily} metric={metric} series={series} />
        </div>
      </section>

      {metric === "conversion" ? (
        <ConversionBreakdown report={report} />
      ) : (
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
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Patient status</TableHead>
                <TableHead className="text-right">Bookings</TableHead>
                <TableHead className="text-right">Conversion</TableHead>
                <TableHead className="text-right">p50 duration</TableHead>
                <TableHead className="text-right">p90 duration</TableHead>
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
                    {formatPercent(report.groups[group].conversion)}
                  </TableCell>
                  <TableCell className="text-right tabular-nums">
                    {formatDuration(report.groups[group].p50)}
                  </TableCell>
                  <TableCell className="text-right tabular-nums">
                    {formatDuration(report.groups[group].p90)}
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
                  {formatPercent(report.total.conversion)}
                </TableCell>
                <TableCell className="text-right tabular-nums">
                  {formatDuration(report.total.p50)}
                </TableCell>
                <TableCell className="text-right tabular-nums">
                  {formatDuration(report.total.p90)}
                </TableCell>
              </TableRow>
            </TableBody>
          </Table>
        </section>
      )}
    </>
  )
}
