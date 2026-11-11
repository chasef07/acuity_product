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
  assert.deepEqual(buttons(markup), ["Keep all open", "Tick a Task to close it"])
  assert.match(markup, /<button[^>]* disabled=""[^>]*>Tick a Task to close it<\/button>/)
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
  assert.match(capped, /Showing 50 of 73/)
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
  assert.equal(markup.match(/<button[^>]* disabled=""/g)?.length, 2)
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

  assert.equal(button(/Tick a Task/).disabled, true)
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
    closures: [{ taskId: second.id, reason: "DONE" }],
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
      { taskId: first.id, reason: "DONE" },
      { taskId: second.id, reason: "NOT_NEEDED" },
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
  assert.deepEqual(sent, [{ outcome: "RESOLVED", closures: [{ taskId: second.id, reason: "DONE" }] }])
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
