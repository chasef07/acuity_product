export type OutcomeCounts = {
  bookings: number
  cancellations: number
  reschedules: number
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
  outcome: string,
): OutcomeCounts {
  switch (outcome) {
    case "BOOKING":
      return { ...counts, bookings: Math.max(0, counts.bookings - 1) }
    case "CANCELLATION":
      return {
        ...counts,
        cancellations: Math.max(0, counts.cancellations - 1),
      }
    case "RESCHEDULE":
      return { ...counts, reschedules: Math.max(0, counts.reschedules - 1) }
    default:
      return counts
  }
}
