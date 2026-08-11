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
