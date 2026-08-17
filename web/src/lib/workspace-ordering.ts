export function oldestFirst<T>(
  rows: readonly T[],
  occurredAt: (row: T) => string,
) {
  return [...rows].sort((left, right) =>
    Date.parse(occurredAt(left)) - Date.parse(occurredAt(right)),
  )
}

export function newestFirst<T>(
  rows: readonly T[],
  occurredAt: (row: T) => string,
) {
  return [...rows].sort((left, right) =>
    Date.parse(occurredAt(right)) - Date.parse(occurredAt(left)),
  )
}

export function appendUniqueByID<T extends { id: string }>(
  loaded: readonly T[],
  incoming: readonly T[],
) {
  const seen = new Set(loaded.map((item) => item.id))
  const items = [...loaded]
  for (const item of incoming) {
    if (seen.has(item.id)) continue
    seen.add(item.id)
    items.push(item)
  }
  return items
}
