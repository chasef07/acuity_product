import type {
  StaffTaskCategory,
  TaskFolderCounts,
} from "./api/generated/types.gen.ts"

export type TaskCategoryFilter = "all" | "texts" | "calls" | StaffTaskCategory

export function taskCountForCategory(
  counts: TaskFolderCounts,
  category: TaskCategoryFilter,
) {
  if (category === "texts") return counts.texts ?? 0
  if (category === "calls") return counts.callRecovery ?? 0
  return category === "all" ? counts.tasks : (counts.categories[category] ?? 0)
}
