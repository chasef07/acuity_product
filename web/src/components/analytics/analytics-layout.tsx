"use client"

import type { ReactNode } from "react"
import { WorkspacePageHeader } from "@/components/workspace/workspace-page-header"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { formatDay, type BookingMetric } from "@/lib/booking-analytics"
import styles from "./booking-overview.module.css"

export type AnalyticsTab = BookingMetric | "staff"

export function AnalyticsFrame({
  section,
  title,
  periodLabel,
  controls,
  tabs,
  children,
}: {
  section: string
  title: string
  periodLabel: ReactNode
  controls: ReactNode
  tabs: ReactNode
  children: ReactNode
}) {
  return (
    <div className={styles.dashboard}>
      <WorkspacePageHeader section={section} title={title} />
      <main className={styles.content}>
        <div className={styles.toolbar}>
          <div className={styles.periodLabel}>{periodLabel}</div>
          <div className={styles.controls}>{controls}</div>
        </div>
        <div className={styles.metricToolbar}>{tabs}</div>
        {children}
      </main>
    </div>
  )
}

function AnalyticsSwitch({
  metric,
  onChange,
}: {
  metric: AnalyticsTab
  onChange: (metric: AnalyticsTab) => void
}) {
  return (
    <ToggleGroup
      variant="segmented"
      spacing={1}
      value={[metric]}
      onValueChange={(values) => {
        const value = values[0]
        if (
          value === "bookings" ||
          value === "conversion" ||
          value === "duration" ||
          value === "staff"
        )
          onChange(value)
      }}
      aria-label="Analytics view"
    >
      <ToggleGroupItem value="bookings">Bookings</ToggleGroupItem>
      <ToggleGroupItem value="conversion">Conversion</ToggleGroupItem>
      <ToggleGroupItem value="duration">Duration</ToggleGroupItem>
      <ToggleGroupItem value="staff">Staff</ToggleGroupItem>
    </ToggleGroup>
  )
}

export function AnalyticsLayout({
  metric,
  setMetric,
  period,
  setPeriod,
  from,
  through,
  children,
}: {
  metric: AnalyticsTab
  setMetric: (value: AnalyticsTab) => void
  period: number
  setPeriod: (value: number) => void
  from?: string
  through?: string
  children: ReactNode
}) {
  return (
    <AnalyticsFrame
      section="Analytics"
      title={metric === "staff" ? "Staff" : "Confirmed bookings"}
      periodLabel={
        from && through ? (
          <>
            {formatDay(from)} – {formatDay(through)}
            <span>{through.slice(0, 4)}</span>
          </>
        ) : (
          `${period} days`
        )
      }
      controls={
        <ToggleGroup
          variant="segmented"
          spacing={1}
          value={[String(period)]}
          onValueChange={(values) => {
            const value = Number(values[0])
            if ([7, 30, 90].includes(value)) setPeriod(value)
          }}
          aria-label="Reporting period"
        >
          {[7, 30, 90].map((value) => (
            <ToggleGroupItem key={value} value={String(value)}>
              {value} days
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
      }
      tabs={<AnalyticsSwitch metric={metric} onChange={setMetric} />}
    >
      {children}
    </AnalyticsFrame>
  )
}
