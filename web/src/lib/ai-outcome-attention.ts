export type OutcomeCounts = {
  tasks: number
  bookings: number
  cancellations: number
  reschedules: number
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
  const refreshedIDs = new Set(refreshed.map((item) => item.id))
  return [...refreshed, ...loaded.filter((item) => !refreshedIDs.has(item.id))]
}

export function appendOutcomePage<T extends { id: string }>(
  loaded: T[],
  page: T[],
) {
  const loadedIDs = new Set(loaded.map((item) => item.id))
  return [...loaded, ...page.filter((item) => !loadedIDs.has(item.id))]
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
