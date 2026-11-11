import assert from "node:assert/strict"
import test, { type TestContext } from "node:test"
import { act, type ComponentProps, type ReactNode } from "react"
import { renderToStaticMarkup } from "react-dom/server"
import { JSDOM } from "jsdom"

import type { CallingCall, Task } from "../../lib/api/generated/types.gen.ts"
import type { WrapUpRequest } from "../../lib/calling/call-wrap-up.ts"
import { CallWrapUpView } from "./call-wrap-up.tsx"

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
  openTask("second", { callerName: "Rosa Synthetic" }),
  openTask("third", { locationName: "South Office", category: "medication", callerName: "Omar Synthetic" }),
  openTask("fourth", { category: undefined, origin: "MISSED_CALL_RECOVERY" }),
  openTask("fifth", { category: undefined, origin: "INBOUND_MESSAGE_REVIEW" }),
]

function endedCall(overrides: Partial<CallingCall> = {}): CallingCall {
  return {
    id: "call-1",
    practiceId: "practice-1",
    locationId: "location-north",
    locationName: "North Office",
    direction: "INBOUND",
    entryPoint: "STANDALONE",
    state: "NEEDS_DISPOSITION",
    phone: "+15551234567",
    phoneSource: "",
    displayName: "",
    nameSource: "",
    transferReason: "",
    reasonSource: "",
    providerTermination: "",
    endRequested: false,
    callerId: "",
    retryAllowed: false,
    connectedAt: "2026-08-30T14:00:00Z",
    version: 3,
    ...overrides,
  }
}

type ViewProps = ComponentProps<typeof CallWrapUpView>

function ready(
  openTasks: Task[],
  rest: { names?: string[]; openTaskCount?: number } = {},
): ViewProps["summary"] {
  return {
    status: "ready",
    data: { names: [], openTasks, openTaskCount: openTasks.length, ...rest },
    refreshing: false,
  }
}

function view(overrides: Partial<ViewProps> = {}) {
  return (
    <CallWrapUpView
      call={endedCall()}
      summary={ready([])}
      saving={false}
      onRecover={() => {}}
      onRetry={() => {}}
      onSave={() => {}}
      onDismiss={() => {}}
      {...overrides}
    />
  )
}

function rows(markup: string) {
  return markup.split('data-testid="call-wrap-up-task"').slice(1)
}

function buttons(markup: string) {
  return [...markup.matchAll(/<button[^>]*>(?:<svg.*?<\/svg>)?([^<]*)<\/button>/g)].map((match) => match[1])
}

test("a patient with nothing open gets the follow-up choice and one button", () => {
  const markup = renderToStaticMarkup(view())
  assert.equal(rows(markup).length, 0)
  assert.match(markup, /No open Tasks for this patient\./)
  assert.match(markup, /Create a follow-up Task/)
  assert.equal(markup.match(/role="checkbox"/g)?.length, 1)
  assert.match(markup, /role="checkbox"[^>]*aria-checked="false"/)
  assert.deepEqual(buttons(markup), ["Finish"])
  assert.doesNotMatch(markup, /Tick what this call/)
})

test("one open Task is listed unticked, with its category and office and no reason yet", () => {
  const markup = renderToStaticMarkup(view({ summary: ready([fiveTasks[0]]) }))
  const [row] = rows(markup)
  assert.ok(row)
  assert.equal(rows(markup).length, 1)
  assert.match(markup, /Tick what this call took care of\. Anything unticked stays open\./)
  assert.match(row, /role="checkbox"[^>]*aria-checked="false"/)
  assert.match(row, /Synthetic first request/)
  assert.match(row, /Insurance &amp; prior authorizations · North Office/)
  assert.doesNotMatch(row, /<select/)
  assert.doesNotMatch(row, /This call/)
  assert.deepEqual(buttons(markup), ["Keep all open"], "with nothing ticked one button finishes the call")
  assert.doesNotMatch(markup, /<button[^>]* disabled=""/)
})

test("five open Tasks list every closable one unticked, with the Task the call was placed from first", () => {
  const call = endedCall({ direction: "OUTBOUND", entryPoint: "TASK", taskId: "task-third" })
  const markup = renderToStaticMarkup(
    view({ call, summary: ready(fiveTasks, { names: ["Rosa Synthetic", "Omar Synthetic"] }) }),
  )
  const listed = rows(markup)
  assert.deepEqual(
    listed.map((row) => /data-wrap-up-task-id="([^"]*)"/.exec(row)?.[1]),
    ["task-third", "task-first", "task-second", "task-fourth"],
    "a text review is closed by replying, so the wrap-up does not offer it",
  )
  assert.equal(listed.filter((row) => /aria-checked="false"/.test(row)).length, 4)
  assert.doesNotMatch(markup, /aria-checked="true"/)
  assert.match(listed[0], /This call/)
  assert.equal(listed.filter((row) => row.includes("This call")).length, 1)
  assert.match(listed[0], /Omar Synthetic · Clinical, medication &amp; pharmacy · South Office/)
  assert.match(listed[2], /Rosa Synthetic · /)
  assert.match(listed[3], /Missed-call follow-up · North Office/)
  assert.doesNotMatch(markup, /Create a follow-up Task/, "a Call placed from a Task keeps that Task open instead")
  assert.match(markup, /<h2[^>]*>\(555\) 123-4567<\/h2>/, "a shared phone is named by its number")
})

test("the header names the patient only when one name is known, and gives the call's length", () => {
  const named = renderToStaticMarkup(
    view({ summary: ready([], { names: ["Rosa Synthetic"] }), durationSeconds: 222 }),
  )
  assert.match(named, /Call ended · 3m 42s/)
  assert.match(named, /<h2[^>]*>Rosa Synthetic<\/h2>/)
  const unnamed = renderToStaticMarkup(view())
  assert.match(unnamed, /<h2[^>]*>\(555\) 123-4567<\/h2>/)
  assert.match(unnamed, />Call ended<\/p>/)
})

test("a capped summary says how many Tasks it shows", () => {
  const fifty = Array.from({ length: 50 }, (_, index) => openTask(`number-${index + 1}`))
  const capped = renderToStaticMarkup(view({ summary: ready(fifty, { openTaskCount: 73 }) }))
  assert.equal(rows(capped).length, 50)
  assert.match(capped, /Showing 50 of 73 open Tasks/)
  const withReviews = renderToStaticMarkup(
    view({
      summary: ready(
        [...fifty.slice(0, 48), openTask("review-a", { origin: "APPOINTMENT_REVIEW" }), fiveTasks[4]],
        { openTaskCount: 73 },
      ),
    }),
  )
  assert.equal(rows(withReviews).length, 48)
  assert.match(withReviews, /Showing 48 of 73 open Tasks/, "the count is the rows on screen")
  assert.doesNotMatch(renderToStaticMarkup(view({ summary: ready(fiveTasks) })), /Showing/)
})

test("while the summary loads the outcome can already be recorded with nothing closed", () => {
  const markup = renderToStaticMarkup(view({ summary: { status: "loading" } }))
  assert.match(markup, /aria-busy="true"/)
  assert.match(markup, /Loading open Tasks/)
  assert.equal(markup.match(/data-slot="skeleton"/g)?.length, 3)
  assert.deepEqual(buttons(markup), ["Keep all open"])
  assert.doesNotMatch(markup, /<button[^>]* disabled=""/)
})

test("a summary that failed to load says so and still records the outcome", () => {
  const markup = renderToStaticMarkup(
    view({ summary: { status: "failed", failure: { kind: "unavailable" } as never } }),
  )
  assert.match(markup, /role="alert"[^>]*>.*open Tasks could not be loaded\. You can still record the call\./)
  assert.deepEqual(buttons(markup), ["Retry", "Keep all open"])
  assert.doesNotMatch(markup, /<button[^>]* disabled=""/)
  assert.match(markup, /Create a follow-up Task/)
})

test("a rejected save shows why, and saving locks the controls", () => {
  const markup = renderToStaticMarkup(
    view({
      summary: ready([fiveTasks[0]]),
      saving: true,
      failure: { kind: "conflict", message: "The Call outcome could not be saved. Try again.", recoverable: true },
    }),
  )
  assert.match(markup, /role="alert"[^>]*>.*Calling request failed.*Try the action again\./)
  assert.equal(markup.match(/<button[^>]* disabled=""/g)?.length, 1)
  assert.match(markup, /role="checkbox"[^>]*aria-disabled="true"/)
})

test("ticking a Task offers its reason, and each button sends the outcome with exactly what is ticked", async (t) => {
  const page = await harness(t)
  const sent: WrapUpRequest[] = []
  const [first, second] = fiveTasks
  await page.render(view({ summary: ready([first, second]), onSave: (request) => sent.push(request) }))
  const row = (task: Task) => page.host.querySelector<HTMLElement>(`[data-wrap-up-task-id="${task.id}"]`)!
  const button = (label: RegExp) =>
    [...page.host.querySelectorAll("button")].find((item) => label.test(item.textContent ?? ""))!
  const tick = (element: HTMLElement) =>
    act(async () => element.querySelector<HTMLElement>('[role="checkbox"]')!.click())

  assert.equal(button(/^Close/), undefined)
  await tick(row(second))
  assert.equal(row(second).querySelector('[role="checkbox"]')!.getAttribute("aria-checked"), "true")
  assert.equal(row(first).querySelector("select"), null)
  const reason = row(second).querySelector("select")!
  assert.equal(reason.getAttribute("aria-label"), "Reason for closing: Synthetic second request")
  assert.equal(reason.value, "DONE")
  assert.deepEqual([...reason.options].map((option) => option.textContent), ["Done", "Not needed"])
  assert.match(button(/^Close/).textContent!, /^Close 1 · keep 1 open$/)

  await act(async () => button(/^Close/).click())
  assert.deepEqual(sent.at(-1), {
    outcome: "RESOLVED",
    closures: [{ taskId: second.id, reason: "DONE", expectedVersion: 1 }],
  })

  await act(async () => {
    reason.value = "NOT_NEEDED"
    reason.dispatchEvent(new window.Event("change", { bubbles: true }))
  })
  await tick(row(first))
  assert.match(button(/^Close/).textContent!, /^Close 2 · keep 0 open$/)
  const followUp = [...page.host.querySelectorAll("label")].find((label) =>
    /Create a follow-up Task/.test(label.textContent ?? ""),
  )!
  await tick(followUp)
  await act(async () => button(/^Close/).click())
  assert.deepEqual(sent.at(-1), {
    outcome: "FOLLOW_UP_REQUIRED",
    closures: [
      { taskId: first.id, reason: "DONE", expectedVersion: 1 },
      { taskId: second.id, reason: "NOT_NEEDED", expectedVersion: 1 },
    ],
  })

  await act(async () => button(/^Keep all open$/).click())
  assert.deepEqual(sent.at(-1), { outcome: "FOLLOW_UP_REQUIRED", closures: [] }, "ticks are not sent by Keep all open")

  await tick(row(second))
  assert.equal(row(second).querySelector("select"), null)
  assert.match(button(/^Close/).textContent!, /^Close 1 · keep 1 open$/)
})

test("a Task that left the open list while ticked is neither counted nor sent", async (t) => {
  const page = await harness(t)
  const sent: WrapUpRequest[] = []
  const [first, second] = fiveTasks
  const onSave = (request: WrapUpRequest) => sent.push(request)
  await page.render(view({ summary: ready([first, second]), onSave }))
  for (const task of [first, second]) {
    await act(async () =>
      page.host.querySelector<HTMLElement>(`[data-wrap-up-task-id="${task.id}"] [role="checkbox"]`)!.click(),
    )
  }
  await page.render(view({ summary: ready([second]), onSave }))
  const close = [...page.host.querySelectorAll("button")].find((item) => /^Close/.test(item.textContent ?? ""))!
  assert.equal(close.textContent, "Close 1 · keep 0 open")
  await act(async () => close.click())
  assert.deepEqual(sent, [
    { outcome: "RESOLVED", closures: [{ taskId: second.id, reason: "DONE", expectedVersion: 1 }] },
  ])
})

test("a patient whose only open Tasks are reviews has nothing to close from the call", () => {
  const markup = renderToStaticMarkup(
    view({ summary: ready([fiveTasks[4], openTask("appointment", { origin: "APPOINTMENT_REVIEW" })]) }),
  )
  assert.equal(rows(markup).length, 0)
  assert.match(markup, /No Tasks to close from this call\./)
  assert.doesNotMatch(markup, /No open Tasks for this patient/)
  assert.deepEqual(buttons(markup), ["Finish"])
})

test("the Task the call was placed from is always listed while it is open", () => {
  const call = endedCall({ direction: "OUTBOUND", entryPoint: "TASK", taskId: "task-own" })
  const own = openTask("own", { category: undefined, origin: "INBOUND_MESSAGE_REVIEW" })
  const callTask = { status: "ready", data: own, refreshing: false } as const
  const listedIDs = (markup: string) =>
    rows(markup).map((row) => /data-wrap-up-task-id="([^"]*)"/.exec(row)?.[1])

  const review = renderToStaticMarkup(view({ call, callTask, summary: ready([fiveTasks[0], own, fiveTasks[4]]) }))
  assert.deepEqual(listedIDs(review), ["task-own", "task-first"], "its own review Task is listed, another review is not")
  assert.match(rows(review)[0], /This call/)

  const beyond = renderToStaticMarkup(
    view({ call, callTask, summary: ready([fiveTasks[0]], { openTaskCount: 60 }) }),
  )
  assert.deepEqual(listedIDs(beyond), ["task-own", "task-first"], "it is listed when the summary did not return it")
  assert.match(beyond, /Showing 2 of 60 open Tasks/)

  const failed = renderToStaticMarkup(
    view({ call, callTask, summary: { status: "failed", failure: { kind: "unavailable" } as never } }),
  )
  assert.deepEqual(listedIDs(failed), ["task-own"], "it is listed when the summary failed to load")
  assert.match(failed, /open Tasks could not be loaded/)
  assert.deepEqual(buttons(failed), ["Retry", "Keep all open"])

  const closed = renderToStaticMarkup(
    view({
      call,
      callTask: { status: "ready", data: { ...own, state: "COMPLETED" }, refreshing: false },
      summary: ready([fiveTasks[0], own]),
    }),
  )
  assert.deepEqual(listedIDs(closed), ["task-first"], "a Task that is already closed is not offered")

  const unread = renderToStaticMarkup(
    view({ call, callTask: { status: "loading" }, summary: ready([fiveTasks[0], own]) }),
  )
  assert.deepEqual(listedIDs(unread), ["task-own", "task-first"])
})

test("a call that was resolved automatically says so, locks the follow-up and can still close Tasks", async (t) => {
  const page = await harness(t)
  const sent: WrapUpRequest[] = []
  const dismissed: string[] = []
  const [first, second] = fiveTasks
  const props = {
    summary: ready([first, second]),
    onSave: (request: WrapUpRequest) => sent.push(request),
    onDismiss: () => dismissed.push("dismissed"),
  }
  await page.render(view(props))
  const followUp = () =>
    [...page.host.querySelectorAll("label")]
      .find((label) => /Create a follow-up Task/.test(label.textContent ?? ""))!
      .querySelector<HTMLElement>('[role="checkbox"]')!
  const button = (label: RegExp) =>
    [...page.host.querySelectorAll("button")].find((item) => label.test(item.textContent ?? ""))
  await act(async () => followUp().click())
  assert.equal(followUp().getAttribute("aria-checked"), "true")
  assert.doesNotMatch(page.host.textContent ?? "", /resolved automatically/)

  await page.render(view({ ...props, settled: { automatic: true } }))
  const notice = page.host.querySelector('[role="status"]')
  assert.equal(
    notice?.textContent,
    "This call was marked resolved automatically. You can still close Tasks.",
  )
  assert.equal(followUp().getAttribute("aria-checked"), "false")
  assert.equal(followUp().getAttribute("aria-disabled"), "true")
  assert.equal(
    page.host.querySelector('[data-testid="call-wrap-up-follow-up-reason"]')?.textContent,
    "The follow-up Task was not created. A follow-up can no longer be created from this call.",
  )
  assert.equal(rows(page.host.innerHTML).length, 2, "the Tasks are still offered")

  await act(async () => button(/^Close$/)!.click())
  assert.deepEqual(dismissed, ["dismissed"], "with nothing ticked the button only closes the dialog")
  assert.deepEqual(sent, [])

  await act(async () =>
    page.host.querySelector<HTMLElement>(`[data-wrap-up-task-id="${second.id}"] [role="checkbox"]`)!.click(),
  )
  await act(async () => button(/^Close 1 · keep 1 open$/)!.click())
  assert.deepEqual(sent, [
    { outcome: "RESOLVED", closures: [{ taskId: second.id, reason: "DONE", expectedVersion: 1 }] },
  ])
  await act(async () => button(/^Keep all open$/)!.click())
  assert.deepEqual(dismissed, ["dismissed", "dismissed"])
  assert.equal(sent.length, 1)

  const untouched = renderToStaticMarkup(view({ settled: { automatic: true } }))
  assert.match(
    untouched,
    /data-testid="call-wrap-up-follow-up-reason"[^>]*>A follow-up can no longer be created from this call\.</,
  )
})

test("a save that left Tasks open keeps them listed and says why", async (t) => {
  const page = await harness(t)
  const sent: WrapUpRequest[] = []
  const [first, second, third] = fiveTasks
  const props = { onSave: (request: WrapUpRequest) => sent.push(request) }
  await page.render(view({ ...props, summary: ready([first, second, third]) }))
  const row = (task: Task) => page.host.querySelector<HTMLElement>(`[data-wrap-up-task-id="${task.id}"]`)!
  const checkbox = (task: Task) => row(task).querySelector<HTMLElement>('[role="checkbox"]')!
  for (const task of [first, second, third]) await act(async () => checkbox(task).click())

  await page.render(
    view({
      ...props,
      summary: ready([{ ...second, version: 2 }, third]),
      settled: {
        automatic: false,
        shortfall: { changedTaskIds: [second.id], unclosedTaskIds: [third.id], followUpNotCreated: true },
      },
    }),
  )
  const alert = page.host.querySelector('[role="alert"]')
  assert.match(alert?.textContent ?? "", /1 Task changed since you opened this and stayed open\./)
  assert.match(alert?.textContent ?? "", /1 Task could not be closed\. Try again\./)
  assert.equal(page.host.querySelector('[role="status"]'), null, "a save by the staff member is not an automatic resolve")
  assert.match(row(second).textContent ?? "", /Changed since you opened this/)
  assert.equal(checkbox(second).getAttribute("aria-checked"), "false", "a changed Task must be ticked again")
  assert.match(row(third).textContent ?? "", /Not closed/)
  assert.equal(checkbox(third).getAttribute("aria-checked"), "true")
  assert.equal(
    page.host.querySelector('[data-testid="call-wrap-up-follow-up-reason"]')?.textContent,
    "The follow-up Task was not created. A follow-up can no longer be created from this call.",
  )

  await act(async () => checkbox(second).click())
  const close = [...page.host.querySelectorAll("button")].find((item) => /^Close 2/.test(item.textContent ?? ""))!
  await act(async () => close.click())
  assert.deepEqual(sent, [
    {
      outcome: "RESOLVED",
      closures: [
        { taskId: second.id, reason: "DONE", expectedVersion: 2 },
        { taskId: third.id, reason: "DONE", expectedVersion: 1 },
      ],
    },
  ])
})

test("an incoming call is shown inside a wrap-up that outlived its call", () => {
  const markup = renderToStaticMarkup(
    view({ settled: { automatic: true }, incomingCall: <p>Synthetic incoming call</p> }),
  )
  assert.match(markup, /Synthetic incoming call/)
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
