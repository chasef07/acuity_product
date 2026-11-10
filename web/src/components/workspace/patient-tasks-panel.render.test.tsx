import assert from "node:assert/strict"
import test, { type TestContext } from "node:test"
import { act, type ComponentProps, type ReactNode } from "react"
import { renderToStaticMarkup } from "react-dom/server"
import { JSDOM } from "jsdom"

import type { CallingCall, StaffTaskCloseReason, Task } from "../../lib/api/generated/types.gen.ts"
import { DataProvider } from "../../lib/data/provider.tsx"
import { EngagementWorkspaceView } from "./engagement-workspace.tsx"
import { PatientTasksPanelView } from "./patient-tasks-panel.tsx"
import { TaskCallContext } from "./task-call-context.tsx"

const now = Date.parse("2026-08-30T15:00:00Z")

function openTask(name: string, overrides: Partial<Task> = {}): Task {
  return {
    id: `task-${name}`,
    practiceId: "practice-1",
    locationId: "location-north",
    locationName: "North Office",
    phone: "+15551234567",
    title: `Synthetic ${name} request`,
    category: "insurance",
    state: "OPEN",
    origin: "ABITA_AI",
    urgency: "normal",
    createdBy: { kind: "SERVICE", subject: "abita" },
    createdAt: "2026-08-30T12:00:00Z",
    version: 1,
    updatedAt: "2026-08-30T12:00:00Z",
    relatedInteractionCount: 0,
    interactions: [],
    ...overrides,
  }
}

const fiveTasks = [
  openTask("first"),
  openTask("second", { urgency: "high_priority", createdAt: "2026-08-30T14:30:00Z" }),
  openTask("third", { locationId: "location-south", locationName: "South Office", category: "medication" }),
  openTask("fourth", {
    locationId: "location-south",
    locationName: "South Office",
    category: undefined,
    origin: "MISSED_CALL_RECOVERY",
    createdAt: "2026-08-28T12:00:00Z",
  }),
  openTask("fifth", { category: undefined, origin: "INBOUND_MESSAGE_REVIEW" }),
]

const closedTask: Task = {
  ...openTask("closed"),
  state: "COMPLETED",
  closeReason: "NOT_NEEDED",
  completedBy: { kind: "HUMAN", subject: "user-1", email: "ines@abita.test" },
  completedAt: "2026-08-30T13:00:00Z",
  version: 2,
}

type ViewProps = ComponentProps<typeof PatientTasksPanelView>

function ready(openTasks: Task[], closedToday: Task[] = []): ViewProps["summary"] {
  return { status: "ready", data: { openTasks, closedToday }, refreshing: false }
}

function view(overrides: Partial<ViewProps> = {}) {
  return (
    <PatientTasksPanelView
      summary={ready([])}
      completion={{ pendingTaskID: "", errorTaskID: "", error: "" }}
      reopening=""
      onComplete={() => {}}
      onOpen={() => {}}
      onReopen={() => {}}
      onRetry={() => {}}
      {...overrides}
    />
  )
}

function rows(markup: string) {
  return markup.split('data-testid="patient-task-row"').slice(1)
}

test("a patient with nothing open reads so", () => {
  const markup = renderToStaticMarkup(view())
  assert.match(markup, /<h2[^>]*>Open Tasks<\/h2><span[^>]*>0<\/span>/)
  assert.match(markup, /Nothing open for this patient\./)
  assert.equal(rows(markup).length, 0)
  assert.doesNotMatch(markup, /Closed today/)
})

test("one open Task shows its tick, title, category, office, age and close menu", (t) => {
  t.mock.method(Date, "now", () => now)
  const [row] = rows(renderToStaticMarkup(view({ summary: ready([fiveTasks[0]]) })))
  assert.ok(row)
  assert.deepEqual(
    [...row.matchAll(/<button[^>]*aria-label="([^"]*)"/g)].map((match) => match[1]),
    ["Mark done: Synthetic first request", "Open Task: Synthetic first request", "Close as: Synthetic first request"],
  )
  assert.match(row, /Insurance &amp; prior authorizations · North Office/)
  assert.match(row, /<time[^>]*dateTime="2026-08-30T12:00:00Z"[^>]*>3h<\/time>/)
  assert.match(row, /aria-haspopup="menu"/)
})

test("five open Tasks at two offices are all listed in the order given", (t) => {
  t.mock.method(Date, "now", () => now)
  const markup = renderToStaticMarkup(view({ summary: ready(fiveTasks), currentTaskID: "task-third" }))
  const listed = rows(markup)
  assert.match(markup, /<h2[^>]*>Open Tasks<\/h2><span[^>]*>5<\/span>/)
  assert.deepEqual(
    listed.map((row) => /aria-label="Open Task: ([^"]*)"/.exec(row)?.[1]),
    fiveTasks.map((task) => task.title),
  )
  assert.equal(listed.filter((row) => row.includes("North Office")).length, 3)
  assert.equal(listed.filter((row) => row.includes("South Office")).length, 2)
  assert.match(listed[1], /Urgent/)
  assert.match(listed[2], /aria-current="true"/)
  assert.match(listed[0], /aria-current="false"/)
  assert.match(listed[3], /Missed-call follow-up · South Office/)
  assert.match(listed[3], />2d<\/time>/)
  assert.match(listed[4], /Incoming text · North Office/)
  assert.doesNotMatch(listed[4], /Mark done|Close as/, "a text review offers no tick or close menu, as in the rail")
  assert.doesNotMatch(markup, /Nothing open/)
})

test("a Task closed today names its reason and who closed it, and offers Reopen", () => {
  const markup = renderToStaticMarkup(view({
    summary: ready([fiveTasks[0]], [closedTask, { ...closedTask, id: "task-auto", title: "Synthetic expired request", closeReason: "EXPIRED", completedBy: { kind: "SERVICE", subject: "task-expiry" } }]),
    reopenFailure: { taskID: "task-closed", message: "The Task could not be reopened." },
  }))
  const closed = markup.slice(markup.indexOf("Closed today"))
  assert.match(closed, /Synthetic closed request/)
  assert.match(closed, /Not needed, by ines@abita\.test/)
  assert.match(closed, /Expired, automatically/)
  assert.match(closed, /<button[^>]*aria-label="Reopen Task: Synthetic closed request"[^>]*>Reopen<\/button>/)
  assert.match(closed, /role="alert"[^>]*>The Task could not be reopened\./)
})

test("the panel stays out of the way when it would only repeat the Task already shown", () => {
  const current = fiveTasks[0]
  assert.equal(renderToStaticMarkup(view({ summary: ready([current]), currentTaskID: current.id })), "")
  assert.equal(renderToStaticMarkup(view({ summary: ready([], [{ ...closedTask, id: current.id }]), currentTaskID: current.id })), "")
  assert.equal(renderToStaticMarkup(view({ summary: { status: "loading" }, currentTaskID: current.id })), "")
  assert.match(renderToStaticMarkup(view({ summary: ready([current, fiveTasks[1]]), currentTaskID: current.id })), /Open Tasks/)
  assert.match(renderToStaticMarkup(view({ summary: ready([current]) })), /Open Tasks/)
  assert.match(renderToStaticMarkup(view({ summary: { status: "loading" } })), /Loading open Tasks/)
})

test("a failed or stale read is said, with a way to retry", () => {
  const failure = { kind: "unavailable", retryable: true } as const
  const failed = renderToStaticMarkup(view({ summary: { status: "failed", failure }, currentTaskID: "task-first" }))
  assert.match(failed, /role="alert"[^>]*>This patient&#x27;s open Tasks could not be loaded\./)
  assert.match(failed, />Retry<\/button>/)
  const stale = renderToStaticMarkup(view({
    summary: { status: "ready", data: { openTasks: fiveTasks, closedToday: [] }, refreshing: false, refreshFailure: failure },
  }))
  assert.match(stale, /This list could not be refreshed\./)
  assert.equal(rows(stale).length, 5)
})

test("a completion in flight disables every row and a failed one is reported on its row", () => {
  const pending = rows(renderToStaticMarkup(view({
    summary: ready(fiveTasks.slice(0, 2)),
    completion: { pendingTaskID: "task-first", errorTaskID: "", error: "" },
  })))
  assert.match(pending[0], /aria-label="Mark done: Synthetic first request"[^>]*aria-busy="true"/)
  assert.equal(pending.join("").match(/ disabled=""/g)?.length, 4)
  const failed = rows(renderToStaticMarkup(view({
    summary: ready(fiveTasks.slice(0, 2)),
    completion: { pendingTaskID: "", errorTaskID: "task-second", error: "This Task changed elsewhere." },
  })))
  assert.doesNotMatch(failed[0], /role="alert"/)
  assert.match(failed[1], /role="alert"[^>]*>This Task changed elsewhere\./)
})

test("the tick, the title and Reopen each report the Task they belong to", async (t) => {
  const page = await harness(t)
  const completed: [string, StaffTaskCloseReason][] = []
  const opened: string[] = []
  const reopened: string[] = []
  await page.render(view({
    summary: ready(fiveTasks.slice(0, 2), [closedTask]),
    onComplete: (task, reason) => completed.push([task.id, reason]),
    onOpen: (task) => opened.push(task.id),
    onReopen: (task) => reopened.push(task.id),
  }))
  const button = (label: string) => page.host.querySelector<HTMLButtonElement>(`button[aria-label="${label}"]`)!
  await act(async () => button("Mark done: Synthetic second request").click())
  await act(async () => button("Open Task: Synthetic second request").click())
  await act(async () => button("Reopen Task: Synthetic closed request").click())
  assert.deepEqual(completed, [["task-second", "DONE"]])
  assert.deepEqual(opened, ["task-second"])
  assert.deepEqual(reopened, ["task-closed"])
})

test("focus stays in the panel when a ticked Task leaves it", async (t) => {
  const page = await harness(t)
  const [first, second, third] = fiveTasks
  const render = (openTasks: Task[], pendingTaskID = "") => page.render(view({
    summary: ready(openTasks),
    completion: { pendingTaskID, errorTaskID: "", error: "" },
  }))
  const tick = (task: Task) => page.host.querySelector<HTMLButtonElement>(`button[aria-label="Mark done: ${task.title}"]`)!
  const focused = () => document.activeElement?.getAttribute("aria-label")
  const complete = async (closed: Task, before: Task[], after: Task[]) => {
    await act(async () => {
      tick(closed).focus()
      tick(closed).click()
    })
    await render(before, closed.id)
    await render(after)
  }
  await render([first, second, third])
  await complete(second, [first, second, third], [first, third])
  assert.equal(focused(), `Open Task: ${third.title}`, "the next Task takes focus")
  await complete(third, [first, third], [first])
  assert.equal(focused(), `Open Task: ${first.title}`, "the previous Task takes focus after the last one")
  await complete(first, [first], [])
  assert.equal(focused(), "Open Tasks", "the panel takes focus when no Task is left")
})

const call: CallingCall = {
  id: "call-1",
  practiceId: "practice-1",
  locationId: "location-north",
  locationName: "North Office",
  direction: "INBOUND",
  entryPoint: "STANDALONE",
  state: "CONNECTED",
  phone: "+15551234567",
  phoneSource: "Telnyx",
  displayName: "Dolores Reyes",
  nameSource: "Abita",
  transferReason: "",
  reasonSource: "",
  providerTermination: "",
  endRequested: false,
  callerId: "+15550000000",
  retryAllowed: false,
  version: 1,
  recoveryTask: { id: "task-fourth", title: "Synthetic fourth request", state: "OPEN", relatedInteractionCount: 1 },
}

function context(overrides: Partial<ComponentProps<typeof TaskCallContext>>) {
  const asked: [string, string | undefined][] = []
  const markup = renderToStaticMarkup(
    <DataProvider>
      <TaskCallContext
        task={undefined}
        activeCall={undefined}
        view="none"
        taskCallError=""
        onTaskUpdated={() => {}}
        onOpenTask={() => {}}
        onReturnToCall={() => {}}
        patientTasks={(phone, currentTaskID) => {
          asked.push([phone, currentTaskID])
          return <p>patient tasks for {phone}</p>
        }}
        {...overrides}
      />
    </DataProvider>,
  )
  return { markup, asked }
}

test("a call shows the caller's Tasks under its details in place of the one recovery Task", () => {
  const { markup, asked } = context({ activeCall: call, view: "call" })
  assert.deepEqual(asked, [[call.phone, undefined]])
  assert.ok(markup.indexOf("Dolores Reyes") < markup.indexOf("patient tasks for"))
  assert.doesNotMatch(markup, /Synthetic fourth request/)
  assert.match(markup, />Open Task<\/button>/)
  const returning = context({ activeCall: { ...call, recoveryTask: undefined }, view: "call", task: fiveTasks[0] })
  assert.match(returning.markup, /Back to Task<\/button>/)
})

test("a selected Task shows the patient's Tasks above its detail, for one Task and for a group", () => {
  const task = fiveTasks[0]
  const single = context({ task, view: "task" })
  assert.deepEqual(single.asked, [[task.phone, task.id]])
  assert.ok(single.markup.indexOf("patient tasks for") < single.markup.indexOf('aria-label="Focused Task"'))
  const group = { ...task, groupMembers: [task, fiveTasks[1]] }
  const grouped = context({ task, group, taskRows: [group], onSelectTask: () => {}, view: "task" })
  assert.deepEqual(grouped.asked, [[task.phone, task.id]])
  assert.ok(grouped.markup.indexOf("patient tasks for") < grouped.markup.indexOf('aria-label="Related Tasks"'))
  assert.deepEqual(context({}).asked, [])
})

function header(caller?: { displayName: string; names: string[] }) {
  return renderToStaticMarkup(
    <DataProvider>
      <EngagementWorkspaceView
        practiceName="Synthetic Practice"
        engagement={{ phone: "+15551234567", locations: [{ id: "location-north", name: "North Office" }] }}
        practiceID="practice-1"
        onTaskCreated={() => {}}
        onTaskOpen={() => {}}
        onCallOpen={() => {}}
        onAIInteractionOpen={() => {}}
        calling={{ callingOccupied: false, outboundPending: false, ownsSoftphone: false, startOutbound: async () => undefined }}
        caller={caller}
      />
    </DataProvider>,
  ).split("</header>")[0]
}

test("the conversation header names the caller before the number and notes other names on it", () => {
  const unnamed = header({ displayName: "", names: [] })
  assert.match(unnamed, /<h1[^>]*>\(555\) 123-4567<\/h1>/)
  assert.doesNotMatch(unnamed, /caller-name|also used by/)
  assert.equal(header(), unnamed)
  const named = header({ displayName: "Dolores Reyes", names: ["Dolores Reyes"] })
  assert.ok(named.indexOf("Dolores Reyes") < named.indexOf("(555) 123-4567"))
  assert.match(named, /<h1[^>]*>\(555\) 123-4567<\/h1>/, "the number stays the heading")
  assert.doesNotMatch(named, /also used by/)
  const shared = header({ displayName: "Dolores Reyes", names: ["Dolores Reyes", "Marco Reyes", "Ana Reyes"] })
  assert.match(shared, />Dolores Reyes<\/span>/)
  assert.match(shared, /also used by Marco Reyes, Ana Reyes</)
})

async function harness(t: TestContext) {
  const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost/workspace" })
  Object.defineProperties(globalThis, {
    window: { configurable: true, value: dom.window },
    document: { configurable: true, value: dom.window.document },
    navigator: { configurable: true, value: dom.window.navigator },
    HTMLElement: { configurable: true, value: dom.window.HTMLElement },
    Element: { configurable: true, value: dom.window.Element },
    Node: { configurable: true, value: dom.window.Node },
    getComputedStyle: { configurable: true, value: dom.window.getComputedStyle.bind(dom.window) },
    IS_REACT_ACT_ENVIRONMENT: { configurable: true, value: true },
  })
  const host = document.createElement("div")
  document.body.append(host)
  const { createRoot } = await import("react-dom/client")
  const root = createRoot(host)
  t.after(async () => {
    await act(async () => root.unmount())
    dom.window.close()
  })
  return { host, render: (node: ReactNode) => act(async () => root.render(node)) }
}
