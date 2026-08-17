import type { AiAppointmentAction } from "./api/generated/types.gen"
import { appendUniqueByID } from "./workspace-ordering.ts"

export type OutcomeCounts = {
  tasks: number
  bookings: number
  cancellations: number
  reschedules: number
}

type AppointmentOutcomeFolderDefinition = {
  action: AiAppointmentAction
  title: string
}

export const appointmentOutcomeFolders = {
  bookings: { action: "BOOKED", title: "Bookings" },
  cancellations: { action: "CANCELLED", title: "Cancellations" },
  reschedules: { action: "RESCHEDULED", title: "Reschedules" },
} as const satisfies Record<string, AppointmentOutcomeFolderDefinition>

export type AppointmentOutcomeFolder =
  keyof typeof appointmentOutcomeFolders

export const appointmentOutcomeFolderKeys = Object.keys(
  appointmentOutcomeFolders,
) as AppointmentOutcomeFolder[]

export type AppointmentOutcomeCursors = Record<AppointmentOutcomeFolder, string>

export function emptyAppointmentOutcomeCursors(): AppointmentOutcomeCursors {
  return Object.fromEntries(
    appointmentOutcomeFolderKeys.map((folder) => [folder, ""]),
  ) as AppointmentOutcomeCursors
}

export function appointmentActionForFolder(
  folder: AppointmentOutcomeFolder,
): AiAppointmentAction {
  return appointmentOutcomeFolders[folder].action
}

export function appointmentFolderForAction(
  action?: string,
): AppointmentOutcomeFolder | undefined {
  for (const folder of appointmentOutcomeFolderKeys) {
    if (appointmentOutcomeFolders[folder].action === action) return folder
  }
  return undefined
}

export function categorizeAIOutcomes<
  T extends { appointmentAction?: string },
>(outcomes: T[]) {
  const categorized = Object.fromEntries(
    appointmentOutcomeFolderKeys.map((folder) => [folder, [] as T[]]),
  ) as Record<AppointmentOutcomeFolder, T[]>
  for (const outcome of outcomes) {
    const folder = appointmentFolderForAction(outcome.appointmentAction)
    if (folder) categorized[folder].push(outcome)
  }
  return categorized
}

export function applyOutcomePages<T extends { id: string }>(
  loaded: readonly T[],
  pages: ReadonlyArray<{
    folder: AppointmentOutcomeFolder
    items: readonly T[]
    nextCursor: string
  }>,
  append: boolean,
) {
  return {
    items: appendUniqueByID(
      append ? loaded : [],
      pages.flatMap((page) => page.items),
    ),
    nextCursors: Object.fromEntries(
      pages.map((page) => [page.folder, page.nextCursor]),
    ) as Partial<AppointmentOutcomeCursors>,
  }
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
