import type { PhoneSummary, Task } from "./api/generated/types.gen.ts"

export function otherOpenTasks(
  summary: Pick<PhoneSummary, "openTasks" | "openTaskCount">,
  selected: readonly Pick<Task, "id" | "state">[],
) {
  const returned = new Set(summary.openTasks.map((task) => task.id))
  const selectedIDs = new Set(selected.map((task) => task.id))
  const tasks = summary.openTasks.filter((task) => !selectedIDs.has(task.id))
  const selectedOpen = selected.filter((task) => returned.has(task.id) || task.state === "OPEN").length
  return { tasks, count: Math.max(tasks.length, summary.openTaskCount - selectedOpen) }
}
