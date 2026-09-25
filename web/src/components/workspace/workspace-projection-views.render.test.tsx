import assert from "node:assert/strict"
import test, { type TestContext } from "node:test"
import { act } from "react"
import { createRoot } from "react-dom/client"
import { renderToStaticMarkup } from "react-dom/server"
import { JSDOM } from "jsdom"

import { SidebarProvider } from "@/components/ui/sidebar"
import { AIInteractionContext } from "./ai-interaction-context.tsx"
import { EngagementWorkspaceView } from "./engagement-workspace.tsx"
import { WorkspaceRail } from "./workspace-rail.tsx"
import { clearAccessToken } from "../../lib/auth-client.ts"
import type {
  AiInteractionDetail,
  ConversationTimelineItem,
  Message,
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

test("opening AI appointment evidence never clears shared work", async () => {
  const dom = installDOM()
  const host = document.createElement("div")
  document.body.append(host)
  const root = createRoot(host)
  await act(async () => root.render(<AIInteractionContext detail={projectedAIInteraction()} loading={false} error="" />))
  assert.match(host.textContent ?? "", /Appointment booked/)
  assert.doesNotMatch(host.textContent ?? "", /Review status|Mark.*reviewed/)
  await act(async () => root.unmount())
  dom.window.close()
})

test("conversation clears its unavailable alert after a successful refresh", async (t) => {
  const conversation = conversationHarness(t)
  const task = projectedTask()
  conversation.items = [{
    type: "TASK",
    id: task.id,
    occurredAt: task.createdAt,
    taskActivity: "TASK_CREATED",
    task,
  }]
  await conversation.render(0)
  assert.equal(conversation.timelineRequests, 1)
  assert.match(conversation.host.textContent ?? "", /Projected follow-up/)

  conversation.timelineStatus = 503
  await conversation.render(1)
  assert.equal(conversation.timelineRequests, 2)
  assert.match(conversation.host.textContent ?? "", /Projected follow-up/)
  assert.match(
    conversation.host.querySelector("[role='alert']")?.textContent ?? "",
    /Conversation unavailable.*The conversation could not be loaded\./,
  )

  conversation.timelineStatus = 200
  await conversation.render(2)
  assert.equal(conversation.timelineRequests, 3)
  assert.equal(
    Boolean(conversation.host.querySelector("[role='alert']")),
    false,
    "A successful timeline response must clear the conversation unavailable alert",
  )
})

test("an unavailable conversation can be retried without showing an empty history", async (t) => {
  const conversation = conversationHarness(t)
  conversation.timelineStatus = 503
  await conversation.render(0)
  assert.doesNotMatch(conversation.host.textContent ?? "", /No activity yet/)
  const retry = Array.from(conversation.host.querySelectorAll("button")).find(
    (button) => button.textContent === "Try again",
  )
  assert.ok(retry, "A failed initial timeline load must offer a retry")

  await act(async () => retry.click())
  assert.equal(conversation.timelineRequests, 2)
  assert.match(
    conversation.host.querySelector("[role='alert']")?.textContent ?? "",
    /The conversation could not be loaded\./,
  )

  conversation.timelineStatus = 200
  await act(async () => retry.click())
  assert.equal(conversation.timelineRequests, 3)
  assert.equal(Boolean(conversation.host.querySelector("[role='alert']")), false)
  assert.match(conversation.host.textContent ?? "", /No activity yet/)
})

test("a missing access token leaves a retryable conversation error instead of a loading spinner", async (t) => {
  const conversation = conversationHarness(t)
  conversation.tokenStatus = 401
  await conversation.render(0)
  assert.equal(conversation.timelineRequests, 0)
  assert.equal(
    Boolean(conversation.host.querySelector('[aria-label="Loading conversation"]')),
    false,
  )
  const retry = Array.from(conversation.host.querySelectorAll("button")).find(
    (button) => button.textContent === "Try again",
  )
  assert.ok(retry)
  conversation.tokenStatus = 200
  await act(async () => retry.click())
  assert.equal(conversation.timelineRequests, 1)
  assert.equal(Boolean(conversation.host.querySelector("[role='alert']")), false)
})

test("linked call history stays brief and opens its existing detail panels", async (t) => {
  const conversation = conversationHarness(t)
  const task = projectedTask()
  const occurredAt = "2026-09-05T12:00:00Z"
  conversation.items = [{
    id: "history", type: "CALL_HISTORY", occurredAt,
    entries: [
      { id: "ai", type: "AI_INTERACTION", occurredAt, aiInteraction: {
        id: "ai", locationId: task.locationId, locationName: "Office", sourceCallId: "source", phone: task.phone,
        startedAt: occurredAt, status: "ESCALATED", appointmentOutcome: "BOOKING",
      } },
      { id: "call", type: "CALL", occurredAt, call: {
        id: "call", type: "CALL", direction: "INBOUND", startedAt: occurredAt,
        durationSeconds: 0, locationId: task.locationId, locationName: "Office",
        answeredByEmail: "", transferReason: "", sourceCallId: "source", outcome: "VOICEMAIL", current: false, originating: false,
      } },
      { id: "created", type: "TASK", occurredAt, taskActivity: "TASK_CREATED", task },
    ],
  }]
  await conversation.render(0)
  const timeline = conversation.host.querySelector('[aria-label="Conversation activity"]')!
  assert.equal(timeline.querySelectorAll("button[aria-expanded]").length, 0)
  const call = timeline.querySelector<HTMLButtonElement>('button[aria-label="View AI call: Appointment booked"]')!
  assert.ok(call)
  assert.match(call.textContent!, /Voicemail received/)
  assert.match(call.textContent!, /Projected follow-up/)
  for (const label of ["View AI call", "View voicemail", "View task"]) {
    const button = [...timeline.querySelectorAll<HTMLButtonElement>("button")].find((button) => button.getAttribute("aria-label")?.startsWith(label))
    assert.ok(button, label)
    await act(async () => button.click())
  }
  assert.deepEqual(conversation.opened, ["ai:ai", "call:call", `task:${task.id}`])

})

test("image download finishing after navigation does not allocate an object URL", async (t) => {
  const view = attachmentHarness(t)
  await view.render(0)
  await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)) })
  assert.equal(view.attachmentRequests, 1)
  await act(async () => view.root.unmount())
  await act(async () => view.release())
  assert.equal(view.createURL.mock.callCount(), 0)
})

test("loaded attachment previews revoke their object URL on navigation", async (t) => {
  const view = attachmentHarness(t)
  await view.render(0)
  await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)) })
  await act(async () => view.release())
  assert.equal(view.host.querySelector("img")?.getAttribute("src"), "blob:synthetic-image")
  assert.equal(view.createURL.mock.callCount(), 1)
  assert.equal(view.revokeURL.mock.callCount(), 0)
  await act(async () => view.root.unmount())
  assert.deepEqual(view.revokeURL.mock.calls.map((call) => call.arguments), [["blob:synthetic-image"]])
})

function attachmentHarness(t: TestContext) {
  const view = conversationHarness(t)
  let release!: () => void
  view.attachmentGate = new Promise<void>((resolve) => { release = resolve })
  view.items = [{ type: "MESSAGE", id: message.id, occurredAt: timestamp, message }]
  t.after(release)
  return Object.assign(view, {
    release,
    createURL: t.mock.method(URL, "createObjectURL", () => "blob:synthetic-image"),
    revokeURL: t.mock.method(URL, "revokeObjectURL", () => {}),
  })
}

const timestamp = "2026-09-25T12:00:00Z"
const message: Message = {
  id: "message-1", direction: "INBOUND", body: "Synthetic attachment", sender: "+15551234567", destination: "+15557654321", delivery: "Delivered", version: 1,
  createdAt: timestamp, updatedAt: timestamp,
  thread: { id: "thread-1", practiceId: "practice-1", locationId: "location-1", locationName: "Test location", officePhone: "+15557654321", externalPhone: "+15551234567", outboundBlocked: false, createdAt: timestamp, updatedAt: timestamp },
  attachment: { id: "attachment-1", direction: "INBOUND", state: "Stored", fileName: "synthetic.png", contentType: "image/png", byteSize: 15, createdAt: timestamp, updatedAt: timestamp },
}

function conversationHarness(t: TestContext) {
  const dom = installDOM()
  const host = document.createElement("div")
  document.body.append(host)
  const root = createRoot(host)
  const previousAPIURL = process.env.NEXT_PUBLIC_PORTAL_API_URL
  process.env.NEXT_PUBLIC_PORTAL_API_URL = "http://portal.test"
  clearAccessToken()
  const conversation = {
    host,
    root,
    render,
    attachmentRequests: 0,
    attachmentGate: undefined as Promise<void> | undefined,
    timelineStatus: 200,
    timelineRequests: 0,
    tokenStatus: 200,
    textTask: undefined as Task | undefined,
    timelineGate: undefined as Promise<void> | undefined,
    completions: [] as number[],
    items: [] as ConversationTimelineItem[],
    opened: [] as string[],
  }
  t.mock.method(globalThis, "fetch", async (input: RequestInfo | URL) => {
    const url =
      typeof input === "string"
        ? input
        : input instanceof URL
          ? input.href
          : input.url
    if (url === "/api/auth/token") {
      return Response.json(
        { token: "synthetic-token" },
        { status: conversation.tokenStatus },
      )
    }
    if (url.includes("/attachments/")) {
      conversation.attachmentRequests++
      await conversation.attachmentGate
      return new Response(new Blob(["synthetic image"], { type: "image/png" }), { headers: { "Content-Type": "image/png" } })
    }
    if (url.includes("/v1/tasks/") && url.endsWith("/complete")) {
      const body = await (input as Request).json()
      conversation.completions.push(body.expectedVersion)
      return Response.json({ ...conversation.textTask, state: "COMPLETED" })
    }
    assert.match(url, /\/v1\/engagements\/%2B15551234567\/timeline\?/)
    conversation.timelineRequests += 1
    await conversation.timelineGate
    return conversation.timelineStatus === 200
      ? Response.json({ items: conversation.items, nextCursor: "" })
      : Response.json(
          { code: "UNAVAILABLE" },
          { status: conversation.timelineStatus },
        )
  })
  const projection = projectedWorkspace(projectedTask())
  async function render(revision: number) {
    await act(async () => {
      root.render(
        <EngagementWorkspaceView
          engagement={projection.selection.engagement!}
          practiceID={projection.scope.practiceID}
          canMutate={false}
          textTask={conversation.textTask}
          onTextTaskUpdated={() => {}}
          revision={revision}
          onTaskCreated={() => {}}
          onTaskOpen={(task) => conversation.opened.push(`task:${task.id}`)}
          onCallOpen={(id) => conversation.opened.push(`call:${id}`)}
          onAIInteractionOpen={(id) => conversation.opened.push(`ai:${id}`)}
          calling={{
            callingOccupied: false,
            callingEnabled: false,
            outboundPending: false,
            ownsSoftphone: false,
            startOutbound: async () => undefined,
          }}
        />,
      )
    })
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
  }
  t.after(async () => {
    await act(async () => root.unmount())
    clearAccessToken()
    if (previousAPIURL === undefined) delete process.env.NEXT_PUBLIC_PORTAL_API_URL
    else process.env.NEXT_PUBLIC_PORTAL_API_URL = previousAPIURL
    dom.window.close()
  })
  return conversation
}

test("text completion requires a successful conversation reload for the current Task version", async (t) => {
  const conversation = conversationHarness(t)
  const task = { ...projectedTask(), origin: "INBOUND_MESSAGE_REVIEW" as const }
  const done = () => Array.from(conversation.host.querySelectorAll<HTMLButtonElement>("button")).find((button) => button.textContent === "Mark done")!
  conversation.textTask = task
  let release!: () => void
  conversation.timelineGate = new Promise<void>((resolve) => { release = resolve })
  await conversation.render(0)
  assert.equal(done().disabled, true, "initial loading has no reviewed evidence")
  conversation.textTask = { ...task, version: 2 }
  await conversation.render(0)
  assert.equal(done().disabled, true, "a Task change during initial loading stays unreviewed")
  await act(async () => { release(); await conversation.timelineGate })
  assert.equal(done().disabled, false)

  conversation.timelineGate = new Promise<void>((resolve) => { release = resolve })
  conversation.textTask = { ...task, version: 3 }
  await conversation.render(1)
  assert.equal(done().disabled, true, "new Task version is not proof the new messages loaded")
  await act(async () => done().click())
  assert.deepEqual(conversation.completions, [])
  conversation.timelineStatus = 503
  await act(async () => { release(); await conversation.timelineGate })
  assert.equal(done().disabled, true, "failed refresh must leave unseen work pending")

  conversation.timelineStatus = 200
  conversation.timelineGate = undefined
  await conversation.render(2)
  assert.equal(done().disabled, false)
  await act(async () => done().click())
  assert.deepEqual(conversation.completions, [3], "completion submits the version whose conversation loaded")
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
    completedTasks: { items: [], nextCursor: "", loading: false, error: "" },
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
