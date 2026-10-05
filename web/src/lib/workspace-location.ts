import type { ManageAgentPage, WorkspaceView } from "./workspace-projection.ts"

export type WorkspacePageView = Exclude<WorkspaceView, "none" | "engagement">

type ManageAgentPageParam = Exclude<ManageAgentPage, "transcripts">

export type WorkspaceLocation =
  | { view: "none" }
  | { view: "task"; taskID: string }
  | { view: WorkspacePageView; page?: ManageAgentPageParam }

const pageViews: WorkspacePageView[] = ["analytics", "operator-analytics", "manage-agent"]
const agentPages: ManageAgentPageParam[] = ["knowledge", "insurance"]

export function parseWorkspaceLocation(search: string): WorkspaceLocation {
  const params = new URLSearchParams(search)
  const taskID = params.get("task")?.trim()
  if (taskID) return { view: "task", taskID }
  const view = pageViews.find((candidate) => candidate === params.get("view"))
  const page = agentPages.find((candidate) => candidate === params.get("page"))
  if (view === "manage-agent" && page) return { view, page }
  return view ? { view } : { view: "none" }
}

export function workspaceLocationSearch(location: WorkspaceLocation) {
  if (location.view === "task") {
    return `?${new URLSearchParams({ task: location.taskID })}`
  }
  if (location.view === "none") return ""
  if (location.view === "manage-agent" && location.page) {
    return `?${new URLSearchParams({ view: location.view, page: location.page })}`
  }
  return `?${new URLSearchParams({ view: location.view })}`
}

export function selectedWorkspaceLocation(selection: {
  view: WorkspaceView
  agentPage?: ManageAgentPage
  task?: { id: string }
}): WorkspaceLocation {
  if (selection.view === "engagement" && selection.task) {
    return { view: "task", taskID: selection.task.id }
  }
  if (selection.view === "none" || selection.view === "engagement") {
    return { view: "none" }
  }
  if (selection.view === "manage-agent" && selection.agentPage && selection.agentPage !== "transcripts") {
    return { view: selection.view, page: selection.agentPage }
  }
  return { view: selection.view }
}
