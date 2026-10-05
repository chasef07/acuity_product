import type { WorkspaceView } from "./workspace-projection.ts"

export type WorkspacePageView = Exclude<WorkspaceView, "none" | "engagement">

export type WorkspaceLocation =
  | { view: "none" }
  | { view: "task"; taskID: string }
  | { view: WorkspacePageView }

const pageViews: WorkspacePageView[] = ["analytics", "operator-analytics", "manage-agent"]

export function parseWorkspaceLocation(search: string): WorkspaceLocation {
  const params = new URLSearchParams(search)
  const taskID = params.get("task")?.trim()
  if (taskID) return { view: "task", taskID }
  const view = pageViews.find((candidate) => candidate === params.get("view"))
  return view ? { view } : { view: "none" }
}

export function workspaceLocationSearch(location: WorkspaceLocation) {
  if (location.view === "task") {
    return `?${new URLSearchParams({ task: location.taskID })}`
  }
  if (location.view === "none") return ""
  return `?${new URLSearchParams({ view: location.view })}`
}

export function selectedWorkspaceLocation(selection: {
  view: WorkspaceView
  task?: { id: string }
}): WorkspaceLocation {
  if (selection.view === "engagement" && selection.task) {
    return { view: "task", taskID: selection.task.id }
  }
  if (selection.view === "none" || selection.view === "engagement") {
    return { view: "none" }
  }
  return { view: selection.view }
}
