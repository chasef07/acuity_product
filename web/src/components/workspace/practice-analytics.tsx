"use client"

import { useState } from "react"
import {
  AnalyticsLayout,
  type AnalyticsTab,
} from "@/components/analytics/analytics-layout"
import { BookingOverview } from "@/components/analytics/booking-overview"
import { StaffOverview } from "@/components/analytics/staff-overview"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import type { Location } from "@/lib/api/generated/types.gen"
import { usePracticeReport } from "@/lib/clients/analytics"

export function PracticeAnalytics({
  practiceID,
  locationScopeID,
  locations,
}: {
  practiceID: string
  locationScopeID: string
  locations: Location[]
}) {
  const [metric, setMetric] = useState<AnalyticsTab>("bookings")
  const [period, setPeriod] = useState(30)
  const [office, setOffice] = useState(locationScopeID || "all")
  const current = usePracticeReport({
    practiceID,
    locationID: office === "all" ? "" : office,
    days: period as 7 | 30 | 90,
    kind: metric === "staff" ? "staff" : "bookings",
  })
  return (
    <AnalyticsLayout
      metric={metric}
      setMetric={setMetric}
      period={period}
      setPeriod={setPeriod}
      office={office}
      setOffice={setOffice}
      offices={[
        { value: "all", label: "All offices" },
        ...locations.map((location) => ({
          value: location.id,
          label: location.name,
        })),
      ]}
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
        <Alert>
          <AlertTitle>
            {current.state === "denied"
              ? "Analytics access unavailable"
              : current.state === "busy"
                ? "Analytics is busy"
                : "Analytics couldn’t load"}
          </AlertTitle>
          <AlertDescription>
            <p>
              {current.state === "denied"
                ? "Analytics requires Admin access to this Practice."
                : current.state === "busy"
                  ? "Another report is loading. Try again in a moment."
                  : "Your analytics are temporarily unavailable. Try again."}
            </p>
            <Button variant="outline" onClick={current.retry}>
              Retry
            </Button>
          </AlertDescription>
        </Alert>
      )}
    </AnalyticsLayout>
  )
}
