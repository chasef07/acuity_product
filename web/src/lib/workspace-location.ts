import type { ManageAgentPage, WorkspaceView } from "./workspace-projection.ts"

export type WorkspacePageView = Exclude<WorkspaceView, "none" | "engagement">

export type WorkspaceLocation =
  | { view: "work" }
  | { view: "task"; taskID: string }
  | { view: Exclude<WorkspacePageView, "manage-agent"> }
  | { view: "manage-agent"; page?: ManageAgentPage }

const pageViews: WorkspacePageView[] = ["analytics", "operator-analytics", "manage-agent"]
const namedAgentPages: ManageAgentPage[] = ["transcripts", "knowledge", "insurance"]

export function parseWorkspaceLocation(search: string): WorkspaceLocation {
  const params = new URLSearchParams(search)
  const taskID = params.get("task")?.trim()
  if (taskID) return { view: "task", taskID }
  if (params.get("view") === "work") return { view: "work" }
  const view = pageViews.find((candidate) => candidate === params.get("view"))
  return pageLocation(view ?? "manage-agent", params.get("page"))
}

export function workspaceLocationSearch(location: WorkspaceLocation) {
  if (location.view === "task") {
    return `?${new URLSearchParams({ task: location.taskID })}`
  }
  if (location.view === "manage-agent" && !location.page) return ""
  const params = new URLSearchParams({ view: location.view })
  if (location.view === "manage-agent" && location.page) params.set("page", location.page)
  return `?${params}`
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
    return { view: "work" }
  }
  return pageLocation(selection.view, selection.agentPage)
}

function pageLocation(view: WorkspacePageView, page?: string | null): WorkspaceLocation {
  if (view !== "manage-agent") return { view }
  const named = namedAgentPages.find((candidate) => candidate === page)
  return named ? { view, page: named } : { view }
}
