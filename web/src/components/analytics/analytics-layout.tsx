"use client"

import type { ReactNode } from "react"
import { OptionSelect } from "@/components/workspace/manage-agent-page"
import { practiceAnalyticsPages, type PracticeAnalyticsPage } from "@/components/workspace/practice-analytics-navigation"
import { formatDay } from "@/lib/booking-analytics"
import styles from "./booking-overview.module.css"

export type AnalyticsTab = PracticeAnalyticsPage

const periods = [7, 30, 90].map((days) => ({ value: String(days), label: `Last ${days} days` }))

export function AnalyticsLayout({
  metric,
  period,
  setPeriod,
  from,
  through,
  children,
}: {
  metric: AnalyticsTab
  period: number
  setPeriod: (value: number) => void
  from?: string
  through?: string
  children: ReactNode
}) {
  return (
    <div className={styles.dashboard}>
      <h1 className="sr-only">{practiceAnalyticsPages.find((item) => item.value === metric)!.label}</h1>
      <main className={styles.content}>
        <div className={styles.toolbar}>
          <div className={styles.periodLabel}>
            {from && through ? <>{formatDay(from)} – {formatDay(through)}<span>{through.slice(0, 4)}</span></> : `${period} days`}
          </div>
          <div className={styles.controls}>
            <OptionSelect label="Date range" value={String(period)} items={periods} onChange={(value) => setPeriod(Number(value))} />
          </div>
        </div>
        {children}
      </main>
    </div>
  )
}
