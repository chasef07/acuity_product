import type {
  StaffTaskCategory,
  TaskFolderCounts,
} from "./api/generated/types.gen.ts"

export type TaskCategoryFilter = "all" | StaffTaskCategory
export type AppointmentFolder =
  | "bookings"
  | "cancellations"
  | "reschedules"

export function appointmentFolderForTask(task: {
  category?: StaffTaskCategory
  title: string
  sourceMessage?: string
}): AppointmentFolder | undefined {
  if (task.category !== "appointments") return undefined
  const text = `${task.title} ${task.sourceMessage ?? ""}`.toLowerCase()
  if (/\b(cancel|cancellation)\b/.test(text)) return "cancellations"
  if (/\b(reschedule|rescheduling|move appointment|change appointment)\b/.test(text)) {
    return "reschedules"
  }
  if (/\b(book|booking|schedule|new appointment|appointment request)\b/.test(text)) {
    return "bookings"
  }
  return undefined
}

export function filterTasksByCategory<
  T extends { category?: StaffTaskCategory },
>(tasks: T[], category: TaskCategoryFilter) {
  return category === "all"
    ? tasks
    : tasks.filter((task) => task.category === category)
}

export function projectTaskUpdate<
  T extends { id: string; state: "OPEN" | "COMPLETED" },
>(tasks: T[], task: T) {
  if (task.state !== "OPEN") {
    return tasks.filter((current) => current.id !== task.id)
  }
  return tasks.some((current) => current.id === task.id)
    ? tasks.map((current) => (current.id === task.id ? task : current))
    : [task, ...tasks]
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

export function refreshTaskWindowTarget(loadedCount: number) {
  return loadedCount > 50 ? loadedCount + 50 : 50
}
