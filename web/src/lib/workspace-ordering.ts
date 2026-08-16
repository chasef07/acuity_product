export function oldestFirst<T>(
  rows: readonly T[],
  occurredAt: (row: T) => string,
) {
  return [...rows].sort((left, right) =>
    occurredAt(left).localeCompare(occurredAt(right)),
  )
}
