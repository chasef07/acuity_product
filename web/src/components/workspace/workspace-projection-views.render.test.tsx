import assert from "node:assert/strict"
import test from "node:test"
import { act } from "react"
import { createRoot } from "react-dom/client"
import { renderToStaticMarkup } from "react-dom/server"
import { JSDOM } from "jsdom"

import { SidebarProvider } from "@/components/ui/sidebar"
import { AIInteractionContext } from "./ai-interaction-context.tsx"
import { EngagementWorkspaceView } from "./engagement-workspace.tsx"
import { WorkspaceRail } from "./workspace-rail.tsx"
import type {
  AiInteractionDetail,
  Task,
} from "../../lib/api/generated/types.gen.ts"
import type {
  WorkspaceProjectionIntent,
  WorkspaceProjectionState,
} from "../../lib/workspace-projection.ts"

test("Task rail and canvas render one supplied projection and the rail emits selection intent", async () => {
  const task = projectedTask()
  const projection = projectedWorkspace(task)
  const railMarkup = renderToStaticMarkup(
    <SidebarProvider>
      <WorkspaceRail
        projection={projection}
        workspaceControl={<span>Workspace control</span>}
        availabilityControl={<span>Available</span>}
        onIntent={() => {}}
      />
    </SidebarProvider>,
  )
  const canvasMarkup = renderToStaticMarkup(
    <EngagementWorkspaceView
      engagement={projection.selection.engagement!}
      practiceID={projection.scope.practiceID}
      canMutate
      revision={projection.detailRevision}
      selectedTaskID={projection.selection.task?.id}
      onTaskCreated={() => {}}
      onTaskOpen={() => {}}
      onCallOpen={() => {}}
      onAIInteractionOpen={() => {}}
      calling={{
        callingOccupied: false,
        callingEnabled: false,
        outboundPending: false,
        ownsSoftphone: false,
        startOutbound: async () => undefined,
      }}
    />,
  )

  assert.match(railMarkup, /Projected follow-up/)
  assert.match(canvasMarkup, /\(555\) 123-4567/)
  assert.match(canvasMarkup, /aria-label="Message"/)

  const dom = installDOM()
  const host = document.createElement("div")
  document.body.append(host)
  const root = createRoot(host)
  const intents: WorkspaceProjectionIntent[] = []
  try {
    await act(async () => {
      root.render(
        <SidebarProvider>
          <WorkspaceRail
            projection={projection}
            workspaceControl={<span>Workspace control</span>}
            availabilityControl={<span>Available</span>}
            onIntent={(intent) => intents.push(intent)}
          />
        </SidebarProvider>,
      )
    })
  } catch (error) {
    if (error instanceof AggregateError) {
      throw new Error(error.errors.map(String).join("\n"))
    }
    throw error
  }
  const taskButton = Array.from(
    host.querySelectorAll<HTMLButtonElement>("[data-testid='task-row'] button"),
  ).find((button) => button.textContent?.includes(task.title))
  assert.ok(taskButton)
  await act(async () => taskButton.click())
  assert.deepEqual(intents, [{ type: "select-task", task }])
  await act(async () => root.unmount())
  dom.window.close()
})

test("failed AI outcome review exposes a retry that can recover", async () => {
  const dom = installDOM()
  const host = document.createElement("div")
  document.body.append(host)
  const root = createRoot(host)
  let resolveFirstReview!: (reviewed: boolean) => void
  const firstReview = new Promise<boolean>((resolve) => {
    resolveFirstReview = resolve
  })
  let attempts = 0
  const onReview = () => {
    attempts += 1
    return attempts === 1 ? firstReview : Promise.resolve(true)
  }

  await act(async () => {
    root.render(
      <AIInteractionContext
        interactionID="interaction-1"
        detail={projectedAIInteraction()}
        loading={false}
        error=""
        onReview={onReview}
      />,
    )
  })
  assert.equal(attempts, 1)

  await act(async () => {
    resolveFirstReview(false)
    await firstReview
  })
  const failure = host.querySelector<HTMLElement>("[role='alert']")
  assert.match(failure?.textContent ?? "", /Review status not saved/)
  const retry = Array.from(failure?.querySelectorAll("button") ?? []).find(
    (button) => button.textContent === "Try again",
  )
  assert.ok(retry)

  await act(async () => {
    retry.click()
    await Promise.resolve()
  })
  assert.equal(attempts, 2)
  assert.equal(host.querySelector("[role='alert']"), null)

  await act(async () => root.unmount())
  dom.window.close()
})

function installDOM() {
  const dom = new JSDOM("<!doctype html><html><body></body></html>", {
    url: "http://localhost/workspace",
  })
  Object.defineProperties(globalThis, {
    window: { configurable: true, value: dom.window },
    document: { configurable: true, value: dom.window.document },
    navigator: { configurable: true, value: dom.window.navigator },
    HTMLElement: { configurable: true, value: dom.window.HTMLElement },
    Element: { configurable: true, value: dom.window.Element },
    Node: { configurable: true, value: dom.window.Node },
    MouseEvent: { configurable: true, value: dom.window.MouseEvent },
    getComputedStyle: {
      configurable: true,
      value: dom.window.getComputedStyle.bind(dom.window),
    },
    IS_REACT_ACT_ENVIRONMENT: { configurable: true, value: true },
  })
  Object.defineProperty(dom.window, "matchMedia", {
    configurable: true,
    value: () => ({
      addEventListener() {},
      matches: false,
      removeEventListener() {},
    }),
  })
  Object.defineProperties(dom.window, {
    requestAnimationFrame: {
      configurable: true,
      value: (callback: FrameRequestCallback) =>
        globalThis.setTimeout(() => callback(Date.now()), 0),
    },
    cancelAnimationFrame: {
      configurable: true,
      value: (id: number) => globalThis.clearTimeout(id),
    },
  })
  Object.defineProperty(dom.window.HTMLElement.prototype, "scrollTo", {
    configurable: true,
    value() {},
  })
  return dom
}

function projectedWorkspace(task: Task): WorkspaceProjectionState {
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
          callingEnabled: false,
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
    tasks: {
      items: [task],
      nextCursor: "",
      loading: false,
      error: "",
      counts: {
        tasks: 1,
        missedCalls: 0,
        categories: {
          billing: 0,
          appointments: 0,
          documentation: 0,
          optical: 0,
          medication: 0,
          referrals: 0,
          other: 1,
        },
      },
    },
    recoveryTasks: { items: [], nextCursor: "", loading: false, error: "" },
    messages: { items: [], nextCursor: "", loading: false, error: "" },
    aiOutcomes: {
      items: [],
      nextCursor: "",
      loading: false,
      error: "",
      counts: { tasks: 0, bookings: 0, cancellations: 0, reschedules: 0 },
      nextCursors: { bookings: "", cancellations: "", reschedules: "" },
    },
    selection: {
      task,
      taskError: "",
      engagement: {
        phone: task.phone,
        locations: [{ id: "location-1", name: "Downtown" }],
        latestActivity: task.updatedAt,
        openTaskCount: 1,
        unread: false,
      },
      aiInteractionID: "",
      aiInteractionLoading: false,
      aiInteractionError: "",
      view: "engagement",
      contextView: "task",
      contextPanelOpen: true,
    },
    detailRevision: 0,
    completion: { pendingTaskID: "", errorTaskID: "", error: "" },
    rail: {
      expanded: ["tasks"],
      expandedAppointments: [],
      taskCategory: "all",
      scrollTop: 0,
    },
  }
}

function projectedTask(): Task {
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
    unread: false,
    createdBy: { kind: "HUMAN", subject: "user-1" },
    createdAt: "2026-08-30T12:00:00Z",
    version: 1,
    updatedAt: "2026-08-30T12:00:00Z",
    relatedInteractionCount: 0,
    interactions: [],
  }
}

function projectedAIInteraction(): AiInteractionDetail {
  return {
    id: "interaction-1",
    practiceId: "practice-1",
    locationId: "location-1",
    locationName: "Downtown",
    sourceCallId: "call-1",
    phone: "+15551234567",
    officePhone: "+15557654321",
    startedAt: "2026-08-30T12:00:00Z",
    endedAt: "2026-08-30T12:05:00Z",
    status: "COMPLETED",
    appointmentOutcome: "BOOKING",
    appointment: {},
    createdAt: "2026-08-30T12:05:00Z",
    updatedAt: "2026-08-30T12:05:00Z",
  }
}
