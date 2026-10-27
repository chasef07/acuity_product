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
import { type PortalQuery, useRead } from "../data/queries"
import { accessDenied, type PortalResult } from "./portal-request"

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
  return analyticsReport(
    useRead({
      name: "practice-report",
      identity: { practiceID, locationID, days, timeZone, kind },
      read: async (identity, transport): Promise<PortalResult<PracticeReport>> => {
        const body = {
          practiceId: identity.practiceID,
          locationId: identity.locationID || undefined,
          days: identity.days,
          timeZone: identity.timeZone,
        }
        if (identity.kind === "staff") {
          const result = await queryStaffAnalytics({ ...transport, body })
          return { ...result, data: result.data && { kind: "staff", report: result.data } }
        }
        const result = await queryBookingAnalytics({ ...transport, body })
        return { ...result, data: result.data && { kind: "bookings", report: result.data } }
      },
    }),
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
    useRead({
      name: "ai-cost-report",
      identity: { practiceID, locationID, range, timeZone },
      read: async (identity, transport) => {
        const result = await queryOperatorAiCosts({
          ...transport,
          body: {
            practiceId: identity.practiceID,
            locationId: identity.locationID || undefined,
            range: identity.range,
            timeZone: identity.timeZone,
          },
        })
        return { ...result, data: result.data && { report: result.data } }
      },
    }),
  )
}

function analyticsReport<T extends object>(
  query: PortalQuery<T> & { retry: () => void },
): AnalyticsReport<T> {
  const { retry } = query
  if (query.status === "loading") return { state: "loading", retry }
  if (query.status === "ready") return { state: "ready", ...query.data, retry }
  const { failure } = query
  return {
    state: accessDenied(failure)
      ? "denied"
      : failure.kind === "busy"
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
