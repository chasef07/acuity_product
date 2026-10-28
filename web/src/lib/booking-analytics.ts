import type {
  AccessDiscovery,
  BookingAnalytics,
  BookingMetrics,
  BookingDay,
} from "./api/generated/types.gen.ts"
export type { BookingAnalytics, BookingDay }
export type BookingSummary = BookingMetrics
export type PatientGroup = "new" | "existing" | "unknown"
export type BookingMetric = "bookings" | "volume" | "duration"

export function canViewPracticeAnalytics(
  discovery: AccessDiscovery | undefined,
  practiceID: string,
): boolean {
  return Boolean(
    discovery &&
    (discovery.platformOperator ||
      discovery.practices.find((practice) => practice.id === practiceID)
        ?.membership?.role === "ADMIN"),
  )
}

export function formatPercent(value: number | null): string {
  return value === null ? "—" : `${value.toFixed(1)}%`
}

export function formatDay(day: string): string {
  return new Intl.DateTimeFormat("en-US", {
    month: "short",
    day: "numeric",
    timeZone: "UTC",
  }).format(new Date(`${day}T12:00:00Z`))
}
