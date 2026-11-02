"use client"

import { useState } from "react"
import {
  AnalyticsLayout,
  type AnalyticsTab,
} from "@/components/analytics/analytics-layout"
import { AnalyticsFailure } from "@/components/analytics/analytics-failure"
import { BookingOverview } from "@/components/analytics/booking-overview"
import { StaffOverview } from "@/components/analytics/staff-overview"
import { Skeleton } from "@/components/ui/skeleton"
import { usePracticeReport } from "@/lib/clients/analytics"

export function PracticeAnalytics({
  practiceID,
  locationScopeID,
  metric,
}: {
  practiceID: string
  locationScopeID: string
  metric: AnalyticsTab
}) {
  const [period, setPeriod] = useState(7)
  const current = usePracticeReport({
    practiceID,
    locationID: locationScopeID,
    days: period as 7 | 30 | 90,
    kind: metric === "staff" ? "staff" : "bookings",
  })
  return (
    <AnalyticsLayout
      metric={metric}
      period={period}
      setPeriod={setPeriod}
      from={current.state === "ready" ? current.report.from : undefined}
      through={current.state === "ready" ? current.report.through : undefined}
    >
      {current.state === "loading" ? (
        <div
          aria-label="Loading analytics"
          aria-busy="true"
          className="flex flex-col gap-6"
        >
          <Skeleton className="h-72 w-full" />
        </div>
      ) : current.state === "ready" ? (
        current.kind === "staff" ? (
          <StaffOverview report={current.report} />
        ) : (
          <BookingOverview
            report={current.report}
            metric={metric === "staff" ? "bookings" : metric}
          />
        )
      ) : (
        <AnalyticsFailure
          title={
            current.state === "denied"
              ? "Analytics access unavailable"
              : current.state === "busy"
                ? "Analytics is busy"
                : "Analytics couldn’t load"
          }
          description={
            current.state === "denied"
              ? "Analytics requires Admin access to this Practice."
              : current.state === "busy"
                ? "Another report is loading. Try again in a moment."
                : "Your analytics are temporarily unavailable. Try again."
          }
          onRetry={current.retry}
        />
      )}
    </AnalyticsLayout>
  )
}
