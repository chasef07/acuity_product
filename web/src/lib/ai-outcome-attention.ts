import type { AiAppointmentAction } from "./api/generated/types.gen"

export type OutcomeCounts = {
  tasks: number
  bookings: number
  cancellations: number
  reschedules: number
}

export const appointmentOutcomeFolders = [
  "bookings",
  "cancellations",
  "reschedules",
] as const

export type AppointmentOutcomeFolder =
  (typeof appointmentOutcomeFolders)[number]

export type AppointmentOutcomeCursors = Record<AppointmentOutcomeFolder, string>

export function emptyAppointmentOutcomeCursors(): AppointmentOutcomeCursors {
  return { bookings: "", cancellations: "", reschedules: "" }
}

export function appointmentActionForFolder(
  folder: AppointmentOutcomeFolder,
): AiAppointmentAction {
  switch (folder) {
    case "bookings":
      return "BOOKED"
    case "cancellations":
      return "CANCELLED"
    case "reschedules":
      return "RESCHEDULED"
  }
}

export function appointmentFolderForAction(action?: string) {
  switch (action) {
    case "BOOKED":
      return "bookings" as const
    case "CANCELLED":
      return "cancellations" as const
    case "RESCHEDULED":
      return "reschedules" as const
    default:
      return undefined
  }
}

export function categorizeAIOutcomes<
  T extends { appointmentAction?: string },
>(outcomes: T[]) {
  const categorized = {
    bookings: [] as T[],
    cancellations: [] as T[],
    reschedules: [] as T[],
  }
  for (const outcome of outcomes) {
    const folder = appointmentFolderForAction(outcome.appointmentAction)
    if (folder) categorized[folder].push(outcome)
  }
  return categorized
}

export function mergeOutcomePages<T extends { id: string }>(
  loaded: T[],
  refreshed: T[],
) {
  return appendOutcomePage(appendOutcomePage([], refreshed), loaded)
}

export function appendOutcomePage<T extends { id: string }>(
  loaded: T[],
  page: T[],
) {
  const seen = new Set(loaded.map((item) => item.id))
  const items = [...loaded]
  for (const item of page) {
    if (seen.has(item.id)) continue
    seen.add(item.id)
    items.push(item)
  }
  return items
}

export function decrementOutcomeCount(
  counts: OutcomeCounts,
  action?: string,
): OutcomeCounts {
  const folder = appointmentFolderForAction(action)
  if (!folder) {
    return { ...counts, tasks: Math.max(0, counts.tasks - 1) }
  }
  return { ...counts, [folder]: Math.max(0, counts[folder] - 1) }
}
