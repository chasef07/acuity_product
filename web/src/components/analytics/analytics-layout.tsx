"use client"

import type { ReactNode } from "react"
import { ChevronRightIcon } from "lucide-react"
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
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
  headerLeading,
  children,
}: {
  section: string
  title: string
  periodLabel: ReactNode
  controls: ReactNode
  tabs: ReactNode
  headerLeading?: ReactNode
  children: ReactNode
}) {
  return (
    <div className={styles.dashboard}>
      <header className={styles.header}>
        <div className={styles.breadcrumb}>
          {headerLeading}
          <span>{section}</span>
          <ChevronRightIcon size={13} />
          <h1>{title}</h1>
        </div>
      </header>
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
  office,
  setOffice,
  offices,
  from,
  through,
  headerLeading,
  children,
}: {
  metric: AnalyticsTab
  setMetric: (value: AnalyticsTab) => void
  period: number
  setPeriod: (value: number) => void
  office: string
  setOffice: (value: string) => void
  offices: Array<{ value: string; label: string }>
  from?: string
  through?: string
  headerLeading?: ReactNode
  children: ReactNode
}) {
  return (
    <AnalyticsFrame
      section="Analytics"
      title={metric === "staff" ? "Staff" : "Confirmed bookings"}
      headerLeading={headerLeading}
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
        <>
          <Select
            value={office}
            onValueChange={(value) => {
              if (value !== null) setOffice(value)
            }}
            items={offices}
          >
            <SelectTrigger aria-label="Office">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectGroup>
                {offices.map((item) => (
                  <SelectItem key={item.value} value={item.value}>
                    {item.label}
                  </SelectItem>
                ))}
              </SelectGroup>
            </SelectContent>
          </Select>
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
        </>
      }
      tabs={<AnalyticsSwitch metric={metric} onChange={setMetric} />}
    >
      {children}
    </AnalyticsFrame>
  )
}
