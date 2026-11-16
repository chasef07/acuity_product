import {
  type WorkspaceProjectionState,
  type WorkspaceRailSection,
} from "@/lib/workspace-projection"
import { taskCountForCategory } from "@/lib/workspace-triage"

export function workspaceFolders(projection: WorkspaceProjectionState): Array<{
  section: Exclude<WorkspaceRailSection, "completed">
  title: string
  count: number
}> {
  const practice = currentPractice(projection)
  const taskCounts = projection.tasks.counts
  const appointmentLocation = practice.locations.find(
    (location) =>
      location.name.trim().toLowerCase() === "spring hill" &&
      (projection.scope.locationScopeID === location.id ||
        practice.locations.length === 1),
  )
  return [
    {
      section: "tasks",
      title: "Tasks",
      count: taskCountForCategory(taskCounts, projection.rail.taskCategory),
    },
    {
      section: "calls",
      title: "Missed Calls",
      count: taskCounts.callRecovery ?? 0,
    },
    ...(appointmentLocation || (taskCounts.appointmentReviews ?? 0) > 0
      ? [
          {
            section: "appointments" as const,
            title: "Appointments",
            count: taskCounts.appointmentReviews ?? 0,
          },
        ]
      : []),
    { section: "texts", title: "Texts", count: taskCounts.texts ?? 0 },
  ]
}

export function currentPractice(projection: WorkspaceProjectionState) {
  const discovery = projection.discovery!
  return (
    discovery.practices.find(
      (item) => item.id === projection.scope.practiceID,
    ) ?? discovery.practices[0]!
  )
}
