import assert from "node:assert/strict"
import test, { type TestContext } from "node:test"
import { act, type ReactNode } from "react"
import { JSDOM } from "jsdom"

import type { StaffTaskCloseReason, Task } from "../../lib/api/generated/types.gen.ts"
import { clearAccessToken } from "../../lib/auth-client.ts"

const task: Task = {
  id: "task-1",
  practiceId: "practice-1",
  locationId: "location-1",
  locationName: "Downtown",
  phone: "+15551234567",
  title: "Call back about refill",
  state: "OPEN",
  origin: "ABITA_AI",
  urgency: "normal",
  createdBy: { kind: "SERVICE", subject: "abita" },
  createdAt: "2026-08-30T12:00:00Z",
  version: 1,
  updatedAt: "2026-08-30T12:00:00Z",
  relatedInteractionCount: 0,
  interactions: [],
}

test("the Task panel menu sends the chosen reason and is offered only for Tasks staff close by hand", async (t) => {
  const page = await harness(t)
  const { DataProvider } = await import("../../lib/data/provider.tsx")
  const { TaskCallContext } = await import("./task-call-context.tsx")
  const sent: unknown[] = []
  t.mock.method(globalThis, "fetch", async (input: RequestInfo | URL) => {
    if (input === "/api/auth/token") return Response.json({ token: "synthetic-token" })
    const request = input as Request
    assert.match(request.url, /\/v1\/tasks\/task-1\/complete$/)
    sent.push(await request.json())
    return Response.json({ ...task, state: "COMPLETED", version: 2 })
  })
  const render = (shown: Task) => page.render(
    <DataProvider>
      <TaskCallContext
        key={sent.length}
        task={shown}
        activeCall={undefined}
        callControlsShowing={false}
        view="task"
        taskCallError=""
        onTaskUpdated={() => {}}
        onReturnToCall={() => {}}
        onOpenTask={() => {}}
        patientTasks={() => null}
      />
    </DataProvider>,
  )
  const menu = () => page.host.querySelector<HTMLButtonElement>("button[aria-label='Close as']")
  for (const [label, reason] of [["Not needed", "NOT_NEEDED"], ["Couldn't reach", "UNREACHABLE"]] as const) {
    await render(task)
    await page.choose(menu()!, label)
    await page.waitFor(() => JSON.stringify(sent.at(-1)) === JSON.stringify({ expectedVersion: task.version, reason }))
  }
  assert.equal(sent.length, 2)
  for (const origin of ["APPOINTMENT_REVIEW", "INBOUND_MESSAGE_REVIEW"] as const) {
    await render({ ...task, origin })
    assert.equal(menu(), null, `${origin} offers no close menu`)
  }
})

test("the patient panel menu sends the chosen reason for its own Task", async (t) => {
  const page = await harness(t)
  const { PatientTasksPanelView } = await import("./patient-tasks-panel.tsx")
  const other: Task = { ...task, id: "task-2", title: "Send records" }
  const closed: [string, StaffTaskCloseReason][] = []
  await page.render(
    <PatientTasksPanelView
      summary={{ status: "ready", data: { names: [], openTasks: [task, other], openTaskCount: 2, closedToday: [] }, refreshing: false }}
      completion={{ pendingTaskID: "", errorTaskID: "", error: "" }}
      reopening=""
      onComplete={(closing, reason) => closed.push([closing.id, reason])}
      onOpen={() => {}}
      onReopen={() => {}}
      onRetry={() => {}}
    />,
  )
  const menu = (shown: Task) =>
    page.host.querySelector<HTMLButtonElement>(`button[aria-label="Close as: ${shown.title}"]`)!
  await page.choose(menu(other), "Not needed")
  await page.choose(menu(task), "Couldn't reach")
  assert.deepEqual(closed, [["task-2", "NOT_NEEDED"], ["task-1", "UNREACHABLE"]])
})

test("a completed text row keeps the patient's preview and names why it closed in the hover details", async (t) => {
  const page = await harness(t)
  const { SidebarProvider } = await import("../ui/sidebar.tsx")
  const { CompletedTaskRow } = await import("./workspace-rail-patient-row.tsx")
  await page.render(
    <SidebarProvider>
      <CompletedTaskRow
        task={{
          ...task,
          origin: "INBOUND_MESSAGE_REVIEW",
          state: "COMPLETED",
          preview: "Can I move my appointment?",
          completedBy: { kind: "HUMAN", subject: "user-1", email: "ines@abita.test" },
          completedAt: "2026-08-30T13:00:00Z",
          closeReason: "NOT_NEEDED",
          version: 2,
        }}
        active={false}
        onSelect={() => {}}
      />
    </SidebarProvider>,
  )
  const row = page.host.querySelector<HTMLButtonElement>("[data-testid='completed-task-row'] button")!
  assert.match(row.textContent ?? "", /Can I move my appointment\?/)
  assert.doesNotMatch(row.textContent ?? "", /Not needed/)
  await act(async () => row.focus())
  await page.waitFor(() => document.querySelector("[data-testid='rail-hover-details']") !== null)
  assert.match(
    document.querySelector("[data-testid='rail-hover-details']")!.textContent ?? "",
    /Not needed, by ines@abita\.test/,
  )
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
    MouseEvent: { configurable: true, value: dom.window.MouseEvent },
    getComputedStyle: { configurable: true, value: dom.window.getComputedStyle.bind(dom.window) },
    IS_REACT_ACT_ENVIRONMENT: { configurable: true, value: true },
    requestAnimationFrame: { configurable: true, value: () => 0 },
    cancelAnimationFrame: { configurable: true, value: () => {} },
  })
  Object.defineProperty(dom.window, "matchMedia", { configurable: true, value: () => ({ matches: false, addEventListener() {}, removeEventListener() {} }) })
  Object.defineProperties(dom.window, {
    requestAnimationFrame: { configurable: true, value: () => 0 },
    cancelAnimationFrame: { configurable: true, value: () => {} },
  })
  const previousAPIURL = process.env.NEXT_PUBLIC_PORTAL_API_URL
  process.env.NEXT_PUBLIC_PORTAL_API_URL = "http://portal.test"
  clearAccessToken()
  const host = document.createElement("div")
  document.body.append(host)
  const { createRoot } = await import("react-dom/client")
  const root = createRoot(host)
  t.after(async () => {
    await act(async () => root.unmount())
    clearAccessToken()
    if (previousAPIURL === undefined) delete process.env.NEXT_PUBLIC_PORTAL_API_URL
    else process.env.NEXT_PUBLIC_PORTAL_API_URL = previousAPIURL
    dom.window.close()
  })
  const waitFor = async (ready: () => boolean) => {
    for (let attempt = 0; attempt < 50 && !ready(); attempt++) {
      await act(async () => new Promise((resolve) => setTimeout(resolve, 0)))
    }
    assert.ok(ready(), "condition was not reached")
  }
  const item = (label: string) =>
    [...document.querySelectorAll<HTMLElement>("[role='menuitem']")].find((candidate) => candidate.textContent === label)
  return {
    host,
    waitFor,
    render: (node: ReactNode) => act(async () => root.render(node)),
    choose: async (trigger: HTMLElement, label: string) => {
      await act(async () => {
        trigger.dispatchEvent(new window.MouseEvent("mousedown", { button: 0, bubbles: true }))
        trigger.click()
      })
      await waitFor(() => item(label) !== undefined)
      await act(async () => item(label)!.click())
    },
  }
}
