"use client"

import { useEffect, useState } from "react"
import {
  AnalyticsLayout,
  type AnalyticsTab,
} from "@/components/analytics/analytics-layout"
import { BookingOverview } from "@/components/analytics/booking-overview"
import { StaffOverview } from "@/components/analytics/staff-overview"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { SidebarTrigger } from "@/components/ui/sidebar"
import { Skeleton } from "@/components/ui/skeleton"
import { portalClient } from "@/lib/api/client"
import {
  queryBookingAnalytics,
  queryStaffAnalytics,
} from "@/lib/api/generated/sdk.gen"
import type {
  BookingAnalytics,
  StaffAnalytics,
  Location,
} from "@/lib/api/generated/types.gen"
import { getAccessToken } from "@/lib/auth-client"

type ReportRequest = { key: string } & (
  | { state: "loading" | "unavailable" | "denied" | "busy" }
  | { state: "ready"; kind: "bookings"; report: BookingAnalytics }
  | { state: "ready"; kind: "staff"; report: StaffAnalytics }
)

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
  const [timeZone] = useState(
    () => Intl.DateTimeFormat().resolvedOptions().timeZone,
  )
  const [revision, setRevision] = useState(0)
  const kind = metric === "staff" ? "staff" : "bookings"
  const key = `${practiceID}:${office}:${period}:${timeZone}:${kind}:${revision}`
  const [request, setRequest] = useState<ReportRequest>({
    key: "",
    state: "loading",
  })
  const current: ReportRequest =
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
        const options = {
          client: portalClient(token),
          body: {
            practiceId: practiceID,
            locationId: office === "all" ? undefined : office,
            days: period as 7 | 30 | 90,
            timeZone,
          },
          signal: controller.signal,
        }
        let status: number | undefined
        if (kind === "staff") {
          const result = await queryStaffAnalytics(options)
          if (controller.signal.aborted) return
          if (result.data) {
            setRequest({ key, state: "ready", kind, report: result.data })
            return
          }
          status = result.response?.status
        } else {
          const result = await queryBookingAnalytics(options)
          if (controller.signal.aborted) return
          if (result.data) {
            setRequest({ key, state: "ready", kind, report: result.data })
            return
          }
          status = result.response?.status
        }
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
  }, [key, practiceID, office, period, timeZone, kind])
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
      headerLeading={<SidebarTrigger collapsedOnly />}
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
            <Button
              variant="outline"
              onClick={() => setRevision((value) => value + 1)}
            >
              Retry
            </Button>
          </AlertDescription>
        </Alert>
      )}
    </AnalyticsLayout>
  )
}
