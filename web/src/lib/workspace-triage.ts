import type {
  StaffTaskCategory,
  TaskFolderCounts,
} from "./api/generated/types.gen.ts"

export type TaskCategoryFilter = "all" | StaffTaskCategory

export function taskCountForCategory(
  counts: TaskFolderCounts,
  category: TaskCategoryFilter,
) {
  return category === "all" ? counts.tasks : (counts.categories[category] ?? 0)
}
