import type {
  StaffTaskCategory,
  TaskFolderCounts,
} from "./api/generated/types.gen.ts"
import { newestFirst } from "./workspace-ordering.ts"

export type TaskCategoryFilter = "all" | StaffTaskCategory

export function filterTaskQueue<
  T extends { origin: string },
>(tasks: T[]) {
  return tasks.filter(
    (task) =>
      task.origin !== "MISSED_CALL_RECOVERY" &&
      task.origin !== "VOICEMAIL_RECOVERY",
  )
}

export function sortRecoveryQueue<T extends { updatedAt: string }>(tasks: T[]) {
  return newestFirst(tasks, (task) => task.updatedAt)
}

export function filterTasksByCategory<
  T extends { category?: StaffTaskCategory },
>(tasks: T[], category: TaskCategoryFilter) {
  return category === "all"
    ? tasks
    : tasks.filter((task) => task.category === category)
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
