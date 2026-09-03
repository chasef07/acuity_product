"use client"

import { useMemo, useState } from "react"
import { ArrowDownIcon, ArrowUpIcon, ArrowUpDownIcon } from "lucide-react"
import { Button } from "@/components/ui/button"
import {
  CartesianGrid,
  Line,
  LineChart,
  ReferenceLine,
  XAxis,
  YAxis,
} from "recharts"
import { ChartContainer, ChartTooltip } from "@/components/ui/chart"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import type {
  StaffAnalytics,
  StaffPhoneMetrics,
  StaffTaskDay,
} from "@/lib/api/generated/types.gen"
import { formatDay, formatPercent } from "@/lib/booking-analytics"
import styles from "./booking-overview.module.css"
import { useReducedMotion } from "@/lib/reduced-motion"

function hours(seconds: number | null) {
  if (seconds === null) return "—"
  const minutes = Math.round(seconds / 60)
  return minutes >= 60
    ? `${Math.floor(minutes / 60)}h ${String(minutes % 60).padStart(2, "0")}m`
    : `${minutes}m`
}
const count = (value: number) => value.toLocaleString("en-US")

function TaskTooltip({
  active,
  payload,
}: {
  active?: boolean
  payload?: ReadonlyArray<{ payload?: StaffTaskDay }>
}) {
  const day = payload?.[0]?.payload
  if (!active || !day) return null
  return (
    <div className={styles.tooltip}>
      <p className={styles.tooltipDate}>{formatDay(day.day)}</p>
      <div className={styles.tooltipRow}>
        <span>Median completion time</span>
        <strong>{hours(day.p50Seconds)}</strong>
      </div>
      <div className={styles.tooltipRow}>
        <span>Tasks completed</span>
        <strong>{count(day.completed)}</strong>
      </div>
    </div>
  )
}

function PhoneCells({ metrics }: { metrics: StaffPhoneMetrics }) {
  return (
    <>
      <TableCell className="text-right tabular-nums">
        {count(metrics.inboundCalls)}
      </TableCell>
      <TableCell className="text-right tabular-nums">
        {count(metrics.outboundCalls)}
      </TableCell>
      <TableCell className="text-right tabular-nums">
        {metrics.missingInboundDurationCalls
          ? "—"
          : hours(metrics.inboundSeconds)}
      </TableCell>
      <TableCell className="text-right tabular-nums">
        {metrics.missingOutboundDurationCalls
          ? "—"
          : hours(metrics.outboundSeconds)}
      </TableCell>
      <TableCell className="text-right tabular-nums">
        {count(metrics.tasksCompleted)}
      </TableCell>
    </>
  )
}

type SortKey =
  | "email"
  | "inboundCalls"
  | "outboundCalls"
  | "inboundSeconds"
  | "outboundSeconds"
  | "tasksCompleted"
const columns: Array<{ key: SortKey; label: string }> = [
  { key: "email", label: "Staff" },
  { key: "inboundCalls", label: "Inbound calls" },
  { key: "outboundCalls", label: "Outbound calls" },
  { key: "inboundSeconds", label: "Inbound time" },
  { key: "outboundSeconds", label: "Outbound time" },
  { key: "tasksCompleted", label: "Tasks completed" },
]

export function StaffOverview({ report }: { report: StaffAnalytics }) {
  const reducedMotion = useReducedMotion()
  const [sorting, setSorting] = useState<{ key: SortKey; descending: boolean }>(
    { key: "email", descending: false },
  )
  const accounts = useMemo(
    () =>
      [...report.accounts].sort((a, b) => {
        const direction = sorting.descending ? -1 : 1
        if (sorting.key === "email")
          return (
            direction * a.email.localeCompare(b.email) ||
            a.id.localeCompare(b.id)
          )
        if (
          sorting.key === "inboundSeconds" ||
          sorting.key === "outboundSeconds"
        ) {
          const missingKey =
            sorting.key === "inboundSeconds"
              ? "missingInboundDurationCalls"
              : "missingOutboundDurationCalls"
          const missingA = a[missingKey] > 0,
            missingB = b[missingKey] > 0
          if (missingA !== missingB) return missingA ? 1 : -1
        }
        return (
          direction * (a[sorting.key] - b[sorting.key]) ||
          a.email.localeCompare(b.email) ||
          a.id.localeCompare(b.id)
        )
      }),
    [report.accounts, sorting],
  )
  const tasks = report.tasks
  const upper = Math.max(
    48 * 3600,
    ...report.daily.map((day) => day.p50Seconds ?? 0),
  )
  return (
    <>
      <section className={styles.hero} aria-label="Staff performance">
        <div className={styles.summary}>
          <div>
            <p className={styles.summaryLabel}>Time to task completion</p>
            <output
              className={styles.headline}
              aria-label="Median task completion time"
            >
              {hours(tasks.p50Seconds)}
            </output>
            <p className={styles.summaryCaption}>
              Median · {count(tasks.completed)} tasks completed
            </p>
          </div>
          <div className={styles.cohorts}>
            <div className={styles.cohort}>
              <div className={styles.cohortHeading}>
                <span>Completed within 48 hours</span>
                <strong>{formatPercent(tasks.within48HoursPercent)}</strong>
              </div>
            </div>
          </div>
        </div>
        <div className={styles.chartSection}>
          <div className={styles.chartHeading}>
            <h2>Time to task completion</h2>
          </div>
          <ChartContainer
            config={{
              p50Seconds: {
                label: "Median completion time",
                color: "var(--foreground)",
              },
            }}
            className="h-[300px] w-full aspect-auto"
            aria-label="Median task completion time by day"
          >
            <LineChart
              data={report.daily}
              accessibilityLayer
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
                domain={[0, Math.ceil(upper / (24 * 3600)) * 24 * 3600]}
                tickFormatter={(value: number) =>
                  `${Math.round(value / 3600)}h`
                }
                axisLine={false}
                tickLine={false}
                width="auto"
                tickMargin={10}
              />
              <ReferenceLine
                y={48 * 3600}
                stroke="var(--muted-foreground)"
                strokeDasharray="4 4"
                label={{
                  value: "48-hour target",
                  position: "insideTopRight",
                  fill: "var(--muted-foreground)",
                  fontSize: 11,
                }}
              />
              <ChartTooltip
                content={<TaskTooltip />}
                cursor={{ stroke: "var(--muted-foreground)", strokeWidth: 1 }}
              />
              <Line
                type="monotone"
                dataKey="p50Seconds"
                stroke="var(--foreground)"
                strokeWidth={2}
                dot={{ r: 2, strokeWidth: 0, fill: "var(--foreground)" }}
                activeDot={{ r: 4 }}
                connectNulls={false}
                isAnimationActive={!reducedMotion}
              />
            </LineChart>
          </ChartContainer>
        </div>
      </section>
      <section className={styles.breakdown} aria-label="Staff accounts">
        <div className={styles.sectionHeading}>
          <h2>Staff accounts</h2>
        </div>
        <Table>
          <TableHeader>
            <TableRow>
              {columns.map((column) => {
                const active = sorting.key === column.key
                const Icon = active
                  ? sorting.descending
                    ? ArrowDownIcon
                    : ArrowUpIcon
                  : ArrowUpDownIcon
                return (
                  <TableHead
                    key={column.key}
                    className={
                      column.key === "email" ? undefined : "text-right"
                    }
                    aria-sort={
                      active
                        ? sorting.descending
                          ? "descending"
                          : "ascending"
                        : undefined
                    }
                  >
                    <Button
                      variant="ghost"
                      size="sm"
                      className={column.key === "email" ? "-ml-3" : "-mr-3"}
                      aria-label={`Sort by ${column.label.toLowerCase()}`}
                      onClick={() =>
                        setSorting({
                          key: column.key,
                          descending: active
                            ? !sorting.descending
                            : column.key !== "email",
                        })
                      }
                    >
                      {column.label}
                      <Icon data-icon="inline-end" />
                    </Button>
                  </TableHead>
                )
              })}
            </TableRow>
          </TableHeader>
          <TableBody>
            {accounts.map((account) => (
              <TableRow key={account.id}>
                <TableCell>
                  <div>{account.email}</div>
                  <div className="text-xs text-muted-foreground">
                    {account.role === "ADMIN"
                      ? "Admin"
                      : account.role === "STAFF"
                        ? "Staff"
                        : "Historical activity"}
                    {account.status === "PENDING"
                      ? " · Not signed in yet"
                      : account.status === "INACTIVE"
                        ? " · Inactive"
                        : ""}
                  </div>
                </TableCell>
                <PhoneCells metrics={account} />
              </TableRow>
            ))}
            <TableRow>
              <TableCell>
                <strong>Total</strong>
              </TableCell>
              <PhoneCells metrics={report.total} />
            </TableRow>
          </TableBody>
        </Table>
      </section>
    </>
  )
}
