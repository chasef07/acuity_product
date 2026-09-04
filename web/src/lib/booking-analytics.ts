import type {
  AccessDiscovery,
  BookingAnalytics,
  BookingMetrics,
  BookingDay,
} from "./api/generated/types.gen.ts"
export type { BookingAnalytics, BookingDay }
export type BookingSummary = BookingMetrics
export type PatientGroup = "new" | "existing" | "unknown"
export type BookingMetric = "bookings" | "conversion" | "duration"

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

export function bookingConversionExplanation(
  summary: Pick<BookingSummary, "converted" | "searched">,
): string {
  return summary.searched === 0
    ? "No calls with an availability check in this period."
    : `${summary.converted.toLocaleString("en-US")} of ${summary.searched.toLocaleString("en-US")} calls booked after checking availability.`
}

export function formatDuration(seconds: number | null): string {
  if (seconds === null) return "—"
  const rounded = Math.round(seconds)
  return `${Math.floor(rounded / 60)}m ${String(rounded % 60).padStart(2, "0")}s`
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
