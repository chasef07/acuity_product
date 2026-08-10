import type { StaffTaskCategory } from "./api/generated/types.gen.ts"

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
