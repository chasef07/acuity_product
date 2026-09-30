import { useState } from "react"

import {
  queryBookingAnalytics,
  queryOperatorAiCosts,
  queryStaffAnalytics,
} from "../api/generated/sdk.gen"
import type {
  BookingAnalytics,
  OperatorAiAnalyticsRange,
  OperatorAiCostAnalytics,
  StaffAnalytics,
} from "../api/generated/types.gen"
import { type PortalQuery, usePortalQuery } from "./portal-request"

export type AnalyticsReport<T extends object> = (
  | { state: "loading" | "denied" | "busy" | "unavailable" }
  | ({ state: "ready" } & T)
) & { retry: () => void }

export type PracticeReport =
  | { kind: "bookings"; report: BookingAnalytics }
  | { kind: "staff"; report: StaffAnalytics }

export function usePracticeReport({
  practiceID,
  locationID,
  days,
  kind,
}: {
  practiceID: string
  locationID: string
  days: 7 | 30 | 90
  kind: PracticeReport["kind"]
}): AnalyticsReport<PracticeReport> {
  const timeZone = useBrowserTimeZone()
  const body = {
    practiceId: practiceID,
    locationId: locationID || undefined,
    days,
    timeZone,
  }
  return analyticsReport(
    usePortalQuery<PracticeReport>(
      `${practiceID}:${locationID}:${days}:${timeZone}:${kind}`,
      async (transport) => {
        if (kind === "staff") {
          const result = await queryStaffAnalytics({ ...transport, body })
          return { ...result, data: result.data && { kind, report: result.data } }
        }
        const result = await queryBookingAnalytics({ ...transport, body })
        return { ...result, data: result.data && { kind, report: result.data } }
      },
    ),
  )
}

export function useAiCostReport({
  practiceID,
  locationID,
  range,
}: {
  practiceID: string
  locationID: string
  range: OperatorAiAnalyticsRange
}): AnalyticsReport<{ report: OperatorAiCostAnalytics }> {
  const timeZone = useBrowserTimeZone()
  return analyticsReport(
    usePortalQuery<{ report: OperatorAiCostAnalytics }>(
      `${practiceID}:${locationID}:${range}:${timeZone}`,
      async (transport) => {
        const result = await queryOperatorAiCosts({
          ...transport,
          body: {
            practiceId: practiceID,
            locationId: locationID || undefined,
            range,
            timeZone,
          },
        })
        return { ...result, data: result.data && { report: result.data } }
      },
    ),
  )
}

function analyticsReport<T extends object>(
  query: PortalQuery<T> & { retry: () => void },
): AnalyticsReport<T> {
  const { retry } = query
  if (query.status === "loading") return { state: "loading", retry }
  if (query.status === "ready") return { state: "ready", ...query.data, retry }
  const { kind } = query.failure
  return {
    state:
      kind === "signedOut" || kind === "unauthenticated" || kind === "unauthorized"
        ? "denied"
        : kind === "busy"
          ? "busy"
          : "unavailable",
    retry,
  }
}

function useBrowserTimeZone() {
  const [timeZone] = useState(
    () => Intl.DateTimeFormat().resolvedOptions().timeZone,
  )
  return timeZone
}
