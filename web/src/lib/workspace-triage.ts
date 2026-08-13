import type {
  StaffTaskCategory,
  TaskFolderCounts,
} from "./api/generated/types.gen.ts"

export type TaskCategoryFilter = "all" | StaffTaskCategory

export function filterTasksByCategory<
  T extends { category?: StaffTaskCategory },
>(tasks: T[], category: TaskCategoryFilter) {
  return category === "all"
    ? tasks
    : tasks.filter((task) => task.category === category)
}

export function recoveryGroupKey(locationID: string, phone: string) {
  return `${locationID}:${phone}`
}

export function taskCountForCategory(
  counts: TaskFolderCounts,
  category: TaskCategoryFilter,
) {
  return category === "all" ? counts.tasks : counts.categories[category]
}

export function taskFolderCursor(
  nextCursor: string,
  loadedCount: number,
  totalCount: number,
) {
  return loadedCount < totalCount ? nextCursor : ""
}

export function reconcileLoadedPage<T extends { id: string }>(
  current: T[],
  refreshed: T[],
  totalCount: number,
  currentCursor: string,
  refreshedCursor: string,
) {
  const refreshedIDs = new Set(refreshed.map((item) => item.id))
  const items = [
    ...refreshed,
    ...current.filter((item) => !refreshedIDs.has(item.id)),
  ].slice(0, totalCount)
  const keptExpandedWindow = items.length > refreshed.length

  return {
    items,
    cursor:
      items.length >= totalCount
        ? ""
        : keptExpandedWindow
          ? currentCursor
          : refreshedCursor,
  }
}
