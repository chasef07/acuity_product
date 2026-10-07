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

type MergeableRow = {
  id: string
  groupMembers?: ReadonlyArray<{ id: string }>
}

export function newerFirst<T extends { id: string }>(
  orderedAt: (row: T) => string | undefined,
) {
  return (row: T, boundary: T) => {
    const difference =
      Date.parse(orderedAt(row) ?? "") - Date.parse(orderedAt(boundary) ?? "")
    return difference < 0 || (difference === 0 && row.id < boundary.id)
  }
}

export function mergeFirstPage<T extends MergeableRow>({
  loaded,
  loadedCursor,
  covered,
  page,
  pageCursor,
  moved = [],
  sortsAfter,
}: {
  loaded: readonly T[]
  loadedCursor: string
  covered: number
  page: readonly T[]
  pageCursor: string
  moved?: readonly MergeableRow[]
  sortsAfter: (row: T, boundary: T) => boolean
}) {
  const boundary = page.at(-1)
  if (!pageCursor || !boundary || loaded.length <= covered) {
    return { items: [...page], nextCursor: pageCursor }
  }
  const fresh = new Set([...page, ...moved].flatMap(rowIDs))
  return {
    items: [
      ...page,
      ...loaded.flatMap((row, index) => {
        if (fresh.has(row.id) || (index < covered && !sortsAfter(row, boundary))) return []
        const members = row.groupMembers?.filter((member) => !fresh.has(member.id))
        return members?.length === row.groupMembers?.length ? [row] : [{ ...row, groupMembers: members }]
      }),
    ],
    nextCursor: loadedCursor,
  }
}

function rowIDs(row: MergeableRow) {
  return [row.id, ...(row.groupMembers ?? []).map((member) => member.id)]
}

export function patchChangedRows<T extends MergeableRow>({
  loaded,
  nextCursor,
  replaced,
  rows,
  sortsAfter,
}: {
  loaded: readonly T[]
  nextCursor: string
  replaced: (row: T) => boolean
  rows: readonly T[]
  sortsAfter: (row: T, boundary: T) => boolean
}) {
  const boundary = loaded.at(-1)
  const items = loaded.filter((row) => !replaced(row))
  let inserted = 0
  for (const row of rows) {
    if (nextCursor && boundary && sortsAfter(row, boundary)) continue
    const index = items.findIndex((item) => sortsAfter(item, row))
    items.splice(index < 0 ? items.length : index, 0, row)
    inserted += 1
  }
  return { items, nextCursor, inserted }
}
