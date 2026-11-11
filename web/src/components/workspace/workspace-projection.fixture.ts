import type { PatientWorkRow, Task } from "../../lib/api/generated/types.gen.ts"
import type { WorkspaceProjectionState } from "../../lib/workspace-projection.ts"

export function projectedWorkspace(task: Task): WorkspaceProjectionState {
  return {
    loadState: "ready",
    connection: "connected",
    discovery: {
      actor: {
        type: "HUMAN",
        subject: "user-1",
        email: "staff@abita.test",
      },
      platformOperator: false,
      practices: [
        {
          id: "practice-1",
          name: "Example Eye Group",
          version: 1,
          membership: {
            id: "membership-1",
            role: "STAFF",
            locationScope: "ALL",
          },
          locations: [{ id: "location-1", name: "Downtown" }],
        },
      ],
    },
    workspace: {
      schemaVersion: "2026-07-24",
      version: 1,
      state: "EMPTY",
      actor: {
        type: "HUMAN",
        subject: "user-1",
        email: "staff@abita.test",
      },
      practice: { id: "practice-1", name: "Example Eye Group", version: 1 },
      location: { id: "location-1", name: "Downtown" },
      platformOperator: false,
      navigation: [],
    },
    scope: {
      practiceID: "practice-1",
      locationID: "location-1",
      locationScopeID: "",
    },
    search: { input: "", applied: "", error: "" },
    patients: {
      items: [projectedPatient(task)],
      nextCursor: "",
      loading: false,
      error: "",
      counts: { all: 1, followUp: 0, callBack: 0, texts: 0, appointments: 0, unowned: 0 },
      totals: { openTasks: 1, patients: 1, patientsOutsideScope: 0 },
    },
    completedTasks: { items: [], nextCursor: "", loading: false, error: "" },
    selection: {
      task,
      taskError: "",
      engagement: {
        phone: task.phone,
        locations: [{ id: "location-1", name: "Downtown" }],
      },
      aiInteractionID: "",
      aiInteractionLoading: false,
      aiInteractionError: "",
      view: "engagement",
      agentPage: "transcripts",
      contextView: "task",
      contextPanelOpen: true,
    },
    detailRevision: 0,
    completion: { pending: [], errors: {} },
    rail: {
      scope: "mine",
      filter: "all",
      completedExpanded: false,
      scrollTop: 0,
    },
  }
}

export function projectedPatient(
  top: Task,
  overrides: Partial<PatientWorkRow> = {},
): PatientWorkRow {
  return {
    phone: top.phone,
    displayName: "",
    openCount: 1,
    highPriority: top.urgency === "high_priority",
    hasUnowned: false,
    activityAt: top.createdAt,
    top,
    ...overrides,
  }
}

export function projectedTask(): Task {
  return {
    id: "task-1",
    practiceId: "practice-1",
    locationId: "location-1",
    locationName: "Downtown",
    phone: "+15551234567",
    title: "Projected follow-up",
    state: "OPEN",
    origin: "STAFF_MESSAGE_FOLLOW_UP",
    urgency: "normal",
    createdBy: { kind: "HUMAN", subject: "user-1" },
    createdAt: "2026-08-30T12:00:00Z",
    version: 1,
    updatedAt: "2026-08-30T12:00:00Z",
    relatedInteractionCount: 0,
    interactions: [],
  }
}
