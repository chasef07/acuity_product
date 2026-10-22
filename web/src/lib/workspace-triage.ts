import type {
  StaffTaskCategory,
  TaskFolderCounts,
  TaskQueryRequest,
} from "./api/generated/types.gen.ts"

export type TaskCategoryFilter = "all" | StaffTaskCategory
export type TaskResponsibility = NonNullable<TaskQueryRequest["responsibility"]>

export function taskCountForCategory(
  counts: TaskFolderCounts,
  category: TaskCategoryFilter,
) {
  return category === "all" ? counts.tasks : (counts.categories[category] ?? 0)
}
