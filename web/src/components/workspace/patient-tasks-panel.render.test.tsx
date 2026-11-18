import assert from "node:assert/strict"
import test, { type TestContext } from "node:test"
import { act, type ComponentProps, type ReactNode } from "react"
import { renderToStaticMarkup } from "react-dom/server"
import { JSDOM } from "jsdom"

import type { CallingCall, StaffTaskCloseReason, Task } from "../../lib/api/generated/types.gen.ts"
import { clearAccessToken } from "../../lib/auth-client.ts"
import { DataProvider } from "../../lib/data/provider.tsx"
import { useLiveRefresh } from "../../lib/data/queries.ts"
import { PatientActionsView, PatientIdentityView } from "./patient-bar.tsx"
import { PatientTasksPanelView } from "./patient-tasks-panel.tsx"
import { type PatientTasksPlacement, TaskCallContext } from "./task-call-context.tsx"

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

function ready(
  openTasks: Task[],
  closedToday: Task[] = [],
  rest: { names?: string[]; openTaskCount?: number } = {},
): ViewProps["summary"] {
  return {
    status: "ready",
    data: { names: [], openTasks, openTaskCount: openTasks.length, closedToday, ...rest },
    refreshing: false,
  }
}

function view(overrides: Partial<ViewProps> = {}) {
  return (
    <PatientTasksPanelView
      summary={ready([])}
      completion={{ pending: [], errors: {} }}
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

function titleAboveScroller(markup: string, title: string, scrolled: string) {
  const page = new JSDOM(markup).window.document
  const withText = (text: string) => [...page.querySelectorAll("h2, h3, p, button, summary, li")].find((element) => element.textContent?.includes(text))
  const heading = withText(title)
  const content = withText(scrolled)
  assert.ok(heading, `${title} is shown`)
  assert.equal(heading.tagName, "H2")
  assert.ok(content, `${scrolled} is shown`)
  const scroller = content.closest('[class~="overflow-y-auto"]')
  assert.ok(scroller, `${scrolled} is in a scrolling region`)
  assert.ok(!scroller.contains(heading), `${title} stays put while ${scrolled} scrolls`)
  const header = heading.closest("header")
  assert.ok(header, `${title} has a title row`)
  assert.ok(header.classList.contains("shrink-0"), "the title row keeps its height")
  assert.ok(
    header.compareDocumentPosition(scroller) & page.defaultView!.Node.DOCUMENT_POSITION_FOLLOWING,
    "the scrolling region starts beneath the title row",
  )
  return header
}

test("a patient with nothing open reads so", () => {
  const markup = renderToStaticMarkup(view())
  assert.match(markup, /<h2[^>]*>Open Tasks<\/h2><span[^>]*>0<\/span>/)
  assert.match(markup, /Nothing open for this patient\./)
  assert.equal(rows(markup).length, 0)
  assert.doesNotMatch(markup, /Done today/)
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
  const markup = renderToStaticMarkup(view({ summary: ready(fiveTasks) }))
  const listed = rows(markup)
  assert.match(markup, /<h2[^>]*>Open Tasks<\/h2><span[^>]*>5<\/span>/)
  assert.deepEqual(
    listed.map((row) => /<span class="truncate">([^<]*)<\/span>/.exec(row)?.[1]),
    fiveTasks.map((task) => task.title),
  )
  assert.equal(listed.filter((row) => row.includes("North Office")).length, 3)
  assert.equal(listed.filter((row) => row.includes("South Office")).length, 2)
  assert.match(listed[1], /Urgent/)
  assert.match(listed[3], /Missed-call follow-up · South Office/)
  assert.match(listed[3], />2d<\/time>/)
  assert.match(listed[4], /Incoming text · North Office/)
  assert.doesNotMatch(listed[4], /Mark done|Close as/, "a text review offers no tick or close menu, as in the rail")
  assert.doesNotMatch(listed[4], /<button/, "a text review title is text, because selecting it would close the panel")
  assert.match(listed[4], /Synthetic fifth request/)
  assert.doesNotMatch(markup, /Nothing open/)
})

test("a Task done today is checked and struck through, names its reason and who closed it, and offers Reopen", () => {
  const markup = renderToStaticMarkup(view({
    summary: ready([fiveTasks[0]], [closedTask, { ...closedTask, id: "task-auto", title: "Synthetic expired request", closeReason: "EXPIRED", completedBy: { kind: "SERVICE", subject: "task-expiry" } }]),
    reopenFailure: { taskID: "task-closed", message: "The Task could not be reopened." },
  }))
  const closed = markup.slice(markup.indexOf("Done today"))
  assert.match(closed, /<p class="[^"]*\bline-through\b[^"]*">Synthetic closed request</)
  assert.match(closed, /Not needed, by ines@abita\.test/)
  assert.match(closed, /Expired, automatically/)
  assert.match(closed, /<button[^>]*aria-label="Reopen Task: Synthetic closed request"[^>]*>Reopen<\/button>/)
  assert.match(closed, /role="alert"[^>]*>The Task could not be reopened\./)
})

test("loading holds its heading and one row of space", () => {
  const current = fiveTasks[0]
  const loading = renderToStaticMarkup(view({ summary: { status: "loading" } }))
  assert.match(loading, /aria-busy="true"/)
  assert.ok(loading.includes(">Open Tasks</h2>"))
  assert.match(loading, /Loading open Tasks/)
  assert.equal(loading.match(/data-slot="skeleton"/g)?.length, 3)
  const one = renderToStaticMarkup(view({ summary: ready([current]) }))
  assert.equal(rows(one).length, 1, "the one Task takes the row the skeleton held")
})

test("a selected Task opens in place in the list, and a Task not in the list opens above it", (t) => {
  t.mock.method(Date, "now", () => now)
  const [first, second] = fiveTasks
  const detail = <p>detail of the selected Task</p>
  const inPlace = renderToStaticMarkup(view({ summary: ready(fiveTasks), expanded: { task: second, content: detail } }))
  assert.match(inPlace, /<h2[^>]*>Open Tasks<\/h2><span[^>]*>5<\/span>/)
  assert.equal(rows(inPlace).length, 4, "the selected Task's row gives way to its detail")
  const order = [first.title, "detail of the selected Task", fiveTasks[2].title].map((text) => inPlace.indexOf(text))
  assert.deepEqual([...order].sort((x, y) => x - y), order, "the detail sits where the row was")
  assert.match(inPlace, new RegExp(`data-patient-task-id="${second.id}"[^>]*data-expanded="true"`))

  const completed = { ...first, state: "COMPLETED" as const }
  const above = renderToStaticMarkup(view({ summary: ready(fiveTasks.slice(1)), expanded: { task: completed, content: detail } }))
  assert.equal(rows(above).length, 4)
  assert.ok(above.indexOf("detail of the selected Task") < above.indexOf(second.title), "a Task that left the list stays on top")

  const loading = renderToStaticMarkup(view({ summary: { status: "loading" }, expanded: { task: first, content: detail } }))
  assert.ok(loading.includes("detail of the selected Task"), "the selected Task does not wait for the list")
})

function manyTasks(count: number) {
  return Array.from({ length: count }, (_, index) => openTask(`number ${index + 1}`))
}

test("a long list shows eight rows until asked for all, and says when the server held some back", async (t) => {
  const page = await harness(t)
  const toggle = () => [...page.host.querySelectorAll("button")].find((button) => /^Show /.test(button.textContent ?? ""))
  const shown = () => page.host.querySelectorAll('[data-testid="patient-task-row"]').length

  await page.render(view({ summary: ready(manyTasks(8)) }))
  assert.equal(shown(), 8)
  assert.equal(toggle(), undefined, "eight Tasks need no toggle")

  await page.render(view({ summary: ready(manyTasks(12)) }))
  assert.equal(shown(), 8)
  assert.equal(toggle()?.textContent, "Show all 12")
  assert.doesNotMatch(page.host.textContent ?? "", /Showing/)
  await act(async () => toggle()!.click())
  assert.equal(shown(), 12)
  assert.equal(toggle()?.textContent, "Show fewer")
  assert.doesNotMatch(page.host.textContent ?? "", /Showing/, "nothing is held back when every Task was returned")
  await act(async () => toggle()!.click())
  assert.equal(shown(), 8)

  await page.render(view({ summary: ready(manyTasks(50), [], { openTaskCount: 63 }) }))
  assert.match(page.host.innerHTML, /<h2[^>]*>Open Tasks<\/h2><span[^>]*>63<\/span>/)
  assert.equal(shown(), 8)
  assert.equal(toggle()?.textContent, "Show 50 of 63")
  await act(async () => toggle()!.click())
  assert.equal(shown(), 50)
  assert.equal(toggle()?.textContent, "Show fewer")
  assert.match(page.host.textContent ?? "", /Showing 50 of 63/)
  assert.doesNotMatch(page.host.textContent ?? "", /\ball\b/i, "a capped list never claims to be all of them")
  await act(async () => toggle()!.click())
})

test("a selected Task past the first rows stays in its place while the rest are folded away", () => {
  const twelve = manyTasks(12)
  const tenth = twelve[9]
  const markup = renderToStaticMarkup(view({ summary: ready(twelve), expanded: { task: tenth, content: <p>detail of the tenth</p> } }))
  assert.ok(markup.includes("detail of the tenth"))
  assert.equal(rows(markup).length, 8, "the first eight rows are still shown")
  assert.ok(markup.indexOf(twelve[7].title) < markup.indexOf("detail of the tenth"))
  assert.ok(!markup.includes(twelve[8].title), "the folded rows stay folded")
})

test("a Task opened above the list after closing is not repeated under Done today", () => {
  const [first, second] = fiveTasks
  const completed = { ...closedTask, id: first.id }
  const markup = renderToStaticMarkup(view({ summary: ready([second], [completed]), expanded: { task: completed, content: <p>detail of the closed Task</p> } }))
  assert.ok(markup.includes("detail of the closed Task"))
  assert.doesNotMatch(markup, /Done today/)
})

test("on a call the titles are text, and the call's own Task comes first with its marker", (t) => {
  t.mock.method(Date, "now", () => now)
  const markup = renderToStaticMarkup(view({ summary: ready(fiveTasks), callTaskID: "task-fourth", onOpen: undefined }))
  const listed = rows(markup)
  assert.deepEqual(
    listed.map((row) => /<span class="truncate">([^<]*)<\/span>/.exec(row)?.[1]),
    [fiveTasks[3], fiveTasks[0], fiveTasks[1], fiveTasks[2], fiveTasks[4]].map((task) => task.title),
  )
  assert.match(listed[0], />This call</)
  assert.equal(listed.filter((row) => row.includes("This call")).length, 1)
  assert.doesNotMatch(markup, /aria-label="Open Task:/, "nothing in the list navigates away from the call")
  assert.match(listed[0], /aria-label="Mark done: Synthetic fourth request"/)
  assert.match(listed[0], /aria-label="Close as: Synthetic fourth request"/)
  const elsewhere = renderToStaticMarkup(view({ summary: ready(fiveTasks) }))
  assert.doesNotMatch(elsewhere, /This call/)
})

test("a shared phone names the caller on each Task that has one", () => {
  const dolores = openTask("first", { callerName: "Dolores Reyes" })
  const marco = openTask("second", { callerName: "Marco Reyes" })
  const unnamed = openTask("third")
  const shared = rows(renderToStaticMarkup(view({
    summary: ready([dolores, marco, unnamed], [], { names: ["Marco Reyes", "Dolores Reyes"] }),
  })))
  assert.match(shared[0], /Dolores Reyes · Insurance/)
  assert.match(shared[1], /Marco Reyes · Insurance/)
  assert.doesNotMatch(shared[2], /Reyes/)
  const single = rows(renderToStaticMarkup(view({
    summary: ready([dolores], [], { names: ["Dolores Reyes"] }),
  })))
  assert.doesNotMatch(single[0], /Dolores Reyes/, "one name is already in the conversation header")
})

test("rows scroll beneath a fixed heading", () => {
  const markup = renderToStaticMarkup(view({ summary: ready(fiveTasks) }))
  titleAboveScroller(markup, "Open Tasks", "Synthetic first request")
  const [, headerClass] = /<header class="([^"]*)"/.exec(markup) ?? []
  const [, bodyClass] = /data-testid="patient-tasks-body" class="([^"]*)"/.exec(markup) ?? []
  assert.match(headerClass ?? "", /\bshrink-0\b/)
  assert.match(bodyClass ?? "", /\boverflow-y-auto\b/)
  assert.ok(markup.indexOf("</header>") < markup.indexOf('data-testid="patient-task-row"'))
  assert.ok(
    markup.indexOf('data-testid="patient-tasks-body"') < markup.indexOf('data-testid="patient-task-row"'),
    "every row is inside the scrolling body",
  )
})

test("a row is built to truncate inside its column and keep its age and menu, as far as classes can say", (t) => {
  t.mock.method(Date, "now", () => now)
  const long = openTask("long", { title: "Synthetic request with a title far longer than the panel is wide", category: "optical" })
  const markup = renderToStaticMarkup(view({ summary: ready([long], [closedTask]) }))
  const classOf = (pattern: RegExp) => (pattern.exec(markup)?.[1] ?? "").split(" ")
  const has = (classes: string[], ...names: string[]) => names.every((name) => classes.includes(name))
  assert.ok(has(classOf(/<section[^>]*class="([^"]*)"/), "min-w-0"), "the section cannot be widened by its rows")
  assert.ok(has(classOf(/data-testid="patient-tasks-body" class="([^"]*)"/), "min-w-0", "overflow-x-hidden"))
  const [row] = rows(markup)
  const rowClass = (pattern: RegExp) => (pattern.exec(row)?.[1] ?? "").split(" ")
  assert.ok(has(rowClass(/<div class="([^"]*)"/), "flex", "min-w-0"), "the row is a flex line that may shrink")
  assert.match(row, /<div class="min-w-0 flex-1"><div class="flex min-w-0 items-baseline gap-2">/, "the text column takes the space left over")
  assert.ok(has(rowClass(/aria-label="Open Task: [^"]*"[^>]*class="([^"]*)"/), "min-w-0", "flex-1"))
  assert.match(row, /<span class="truncate">Synthetic request with a title far longer/)
  assert.ok(has(rowClass(/<time[^>]*class="([^"]*)"/), "shrink-0"), "the age keeps its width")
  assert.match(row, /<p class="truncate [^"]*">Optical, frames &amp; prescriptions · North Office<\/p>/)
  assert.ok(has(rowClass(/aria-label="Close as: [^"]*"[^>]*class="([^"]*)"/), "shrink-0"), "the menu keeps its width")
  const closed = markup.slice(markup.indexOf("Done today"))
  assert.match(closed, /<div class="min-w-0 flex-1"><p class="truncate /)
})

test("a failed or stale read is said, with a way to retry", () => {
  const failure = { kind: "unavailable", retryable: true } as const
  const failed = renderToStaticMarkup(view({ summary: { status: "failed", failure } }))
  assert.match(failed, /role="alert"[^>]*>This patient&#x27;s open Tasks could not be loaded\./)
  assert.match(failed, />Retry<\/button>/)
  const stale = renderToStaticMarkup(view({
    summary: {
      status: "ready",
      data: { names: [], openTasks: fiveTasks, openTaskCount: 5, closedToday: [] },
      refreshing: false,
      refreshFailure: failure,
    },
  }))
  assert.match(stale, /This list could not be refreshed\./)
  assert.equal(rows(stale).length, 5)
})

test("each tick saves on its own: only a saving row is disabled and each failure is reported on its row", () => {
  const three = fiveTasks.slice(0, 3)
  const disabled = (markup: string) =>
    [...new JSDOM(markup).window.document.querySelectorAll('[data-testid="patient-task-row"]')].map((row) =>
      [...row.querySelectorAll("button:disabled")].map((button) => button.getAttribute("aria-label")?.split(":")[0]))
  const saving = renderToStaticMarkup(view({
    summary: ready(three),
    completion: { pending: ["task-first", "task-third"], errors: {} },
  }))
  const pending = rows(saving)
  assert.match(pending[0], /aria-label="Mark done: Synthetic first request"[^>]*aria-busy="true"/)
  assert.match(pending[2], /aria-label="Mark done: Synthetic third request"[^>]*aria-busy="true"/)
  assert.deepEqual(disabled(saving), [["Mark done", "Close as"], [], ["Mark done", "Close as"]])
  assert.doesNotMatch(pending[1], /aria-busy/)
  const reported = renderToStaticMarkup(view({
    summary: ready(three),
    completion: {
      pending: ["task-first"],
      errors: { "task-second": "This Task changed elsewhere.", "task-third": "Task completion is temporarily unavailable." },
    },
  }))
  const failed = rows(reported)
  assert.doesNotMatch(failed[0], /role="alert"/)
  assert.match(failed[1], /role="alert"[^>]*>This Task changed elsewhere\./)
  assert.match(failed[2], /role="alert"[^>]*>Task completion is temporarily unavailable\./)
  assert.deepEqual(disabled(reported), [["Mark done", "Close as"], [], []])
})

test("a text review and an appointment check each offer Open for their own Task when no call is live", async (t) => {
  const page = await harness(t)
  const text = fiveTasks[4]
  const check = openTask("check", { category: "appointments", origin: "APPOINTMENT_REVIEW" })
  const opened: string[] = []
  const render = (overrides: Partial<ViewProps>) => page.render(view({
    summary: ready([fiveTasks[0], text, check]),
    onOpen: (task) => opened.push(task.id),
    ...overrides,
  }))
  const controls = () => [...page.host.querySelectorAll('[data-testid="patient-task-row"]')].map((row) =>
    [...row.querySelectorAll("button")].map((button) => button.getAttribute("aria-label")))
  const reviewControls = (task: Task) => controls()[[fiveTasks[0], text, check].indexOf(task)]

  await render({ opensReviews: true })
  assert.deepEqual(reviewControls(text), ["Open Task: Synthetic fifth request"], "a text review has no tick or close menu, only Open")
  assert.deepEqual(reviewControls(check), ["Open Task: Synthetic check request"])
  for (const review of [text, check]) {
    const open = page.host.querySelector<HTMLButtonElement>(`button[aria-label="Open Task: ${review.title}"]`)!
    assert.equal(open.textContent, "Open")
    assert.equal(open.closest("li")?.querySelector('[data-testid="patient-task-title"]')?.textContent, review.title, "the title stays text")
    await act(async () => open.click())
  }
  assert.deepEqual(opened, [text.id, check.id])

  await render({ opensReviews: true, expanded: { task: text, content: <p>the open text review</p> } })
  assert.equal(page.host.querySelector(`button[aria-label="Open Task: ${text.title}"]`), null, "the review that is already open shows in place, with nothing to open")
  assert.ok(page.host.querySelector(`button[aria-label="Open Task: ${check.title}"]`))

  await render({ opensReviews: false })
  assert.deepEqual([reviewControls(text), reviewControls(check)], [[], []], "during a live call a review is text")
  assert.equal(controls()[0].includes("Open Task: Synthetic first request"), true, "other titles follow their own rule")

  await render({ opensReviews: true, onOpen: undefined })
  assert.deepEqual(controls().flat().filter((label) => label?.startsWith("Open Task")), [], "beside a call nothing opens")
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
    completion: { pending: pendingTaskID ? [pendingTaskID] : [], errors: {} },
  }))
  const tick = (task: Task) => page.host.querySelector<HTMLButtonElement>(`button[aria-label="Mark done: ${task.title}"]`)!
  const focused = () => document.activeElement?.getAttribute("data-patient-task-id") ?? document.activeElement?.getAttribute("aria-label")
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
  assert.equal(focused(), third.id, "the next Task takes focus")
  await complete(third, [first, third], [first])
  assert.equal(focused(), first.id, "the previous Task takes focus after the last one")
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
  const asked: [string, PatientTasksPlacement][] = []
  const markup = renderToStaticMarkup(
    <DataProvider>
      <TaskCallContext
        task={undefined}
        activeCall={undefined}
        callControlsShowing={false}
        view="none"
        onTaskUpdated={() => {}}
        onOpenTask={() => {}}
        onReturnToCall={() => {}}
        patientTasks={(phone, placement) => {
          asked.push([phone, placement])
          return <p>patient tasks for {phone}</p>
        }}
        {...overrides}
      />
    </DataProvider>,
  )
  return { markup, asked }
}

test("a call shows the caller's Tasks under its header, above the recording and details", () => {
  const recorded = {
    ...call,
    transferReason: "Needs a staff response",
    recording: { audioState: "PROCESSING", durationSeconds: 0 } as const,
  }
  const { markup, asked } = context({ activeCall: recorded, view: "call" })
  assert.deepEqual(asked, [[call.phone, { beside: "call", callTaskID: "task-fourth" }]], "the recovery Task is the call's own")
  const at = (text: string) => {
    const index = markup.indexOf(text)
    assert.notEqual(index, -1, `${text} is shown`)
    return index
  }
  assert.ok(at("Dolores Reyes") < at("patient tasks for"))
  assert.ok(at("Needs a staff response") < at("patient tasks for"))
  assert.ok(at(">Open Task</button>") < at("patient tasks for"))
  assert.ok(at("patient tasks for") < at("Call recording"), "the list sits above the recording")
  assert.ok(at("patient tasks for") < at(">Details<"), "and above the call details")
  assert.equal(markup.match(/overflow-y-auto/g)?.length, 1, "the caller, list and details scroll as one")
  const header = titleAboveScroller(markup, "Connected", "patient tasks for")
  assert.ok(header.classList.contains("min-h-11"), "the title row ends below the close button")
  assert.ok(header.classList.contains("pr-12"), "and beside it")
  assert.match(markup, /^<section class="[^"]*\bmin-w-0\b/)
  assert.doesNotMatch(markup, /Synthetic fourth request/)
  assert.match(markup, />Open Task<\/button>/)
  const returning = context({ activeCall: { ...call, recoveryTask: undefined }, view: "call", task: fiveTasks[0] })
  assert.match(returning.markup, /Back to Task<\/button>/)
  const placed = context({ activeCall: { ...call, recoveryTask: undefined, entryPoint: "TASK", taskId: "task-second" }, view: "call" })
  assert.deepEqual(placed.asked, [[call.phone, { beside: "call", callTaskID: "task-second" }]], "an outbound call belongs to the Task it was placed from")
  const standalone = context({ activeCall: { ...call, recoveryTask: undefined }, view: "call" })
  assert.deepEqual(standalone.asked, [[call.phone, { beside: "call", callTaskID: undefined }]])
})

test("a live call, whose floating controls name the caller, keeps only its state and Task action above the list", () => {
  const transferred = { ...call, transferReason: "Needs a staff response" }
  const live = context({ activeCall: transferred, callControlsShowing: true, view: "call" })
  const header = titleAboveScroller(live.markup, "Connected", "patient tasks for")
  assert.ok(header.classList.contains("h-11"), "one row, ending below the close button")
  assert.ok(header.classList.contains("pr-12"), "and beside it")
  assert.deepEqual([...header.children].map((child) => [child.tagName, child.textContent]), [["H2", "Connected"], ["BUTTON", "Open Task"]])
  assert.doesNotMatch(live.markup, /Dolores Reyes|\(555\) 123-4567|North Office|Needs a staff response/)
  assert.equal(live.markup.match(/<button/g)?.length, 1, "the Task action is offered once")
  assert.ok(live.markup.indexOf("patient tasks for") < live.markup.indexOf(">Details<"), "the call details stay below the list")
  assert.deepEqual(live.asked, [[call.phone, { beside: "call", callTaskID: "task-fourth" }]])

  const returning = context({ activeCall: { ...transferred, recoveryTask: undefined }, callControlsShowing: true, view: "call", task: fiveTasks[0] })
  assert.match(returning.markup, /<header[^>]*>.*>Back to Task<\/button><\/header>/)
  const alone = context({ activeCall: { ...transferred, recoveryTask: undefined }, callControlsShowing: true, view: "call" })
  assert.doesNotMatch(alone.markup, /<button/)
  assert.match(alone.markup, /<h2[^>]*>Connected<\/h2><\/header>/)

  const past = context({ activeCall: transferred, callControlsShowing: false, view: "call" })
  for (const shown of ["Connected", "Dolores Reyes", "(555) 123-4567 · North Office", "Needs a staff response", ">Open Task</button>"]) {
    assert.ok(past.markup.includes(shown), `a call without floating controls still shows ${shown}`)
  }
  assert.match(past.markup, /<h2 class="text-base font-medium leading-snug">Connected<\/h2>/)
  assert.match(past.markup, /<button[^>]*class="[^"]*\bw-full\b[^"]*"[^>]*>Open Task<\/button>/)
})

test("a selected Task opens in place inside the patient's Task list, and a text review leaves the list closed", () => {
  const task = fiveTasks[0]
  const single = context({ task, view: "task" })
  assert.equal(single.asked.length, 1)
  const [phone, placement] = single.asked[0]
  assert.equal(phone, task.phone)
  assert.ok(placement.beside === "task" && placement.expanded, "the list is asked to open the Task")
  assert.equal(placement.expanded.task, task)
  const detail = renderToStaticMarkup(<DataProvider>{placement.expanded.content}</DataProvider>)
  assert.match(detail, /aria-label="Focused Task"/)
  assert.ok(detail.indexOf('aria-label="Complete"') < detail.indexOf(task.title), "the completion circle leads the title")
  assert.ok(detail.indexOf(task.title) < detail.indexOf(">Details<"))
  assert.match(single.markup, /^<div class="[^"]*\bmin-w-0\b/, "the column cannot be widened by its content")

  const text = fiveTasks[4]
  assert.deepEqual(context({ task: text, view: "task" }).asked, [[text.phone, { beside: "task" }]], "a text is reviewed in the conversation")
  assert.deepEqual(context({}).asked, [])
})

test("an opened Task is light: a circle to complete it, a small title, urgency in words, and one group control", () => {
  const urgent = { ...fiveTasks[1], sourceMessage: "Synthetic message from the caller." }
  const [, placement] = context({ task: urgent, view: "task" }).asked[0]
  assert.ok(placement.beside === "task" && placement.expanded)
  const page = new JSDOM(renderToStaticMarkup(<DataProvider>{placement.expanded.content}</DataProvider>)).window.document
  const focused = page.querySelector('[aria-label="Focused Task"]')!
  assert.equal(focused.querySelector('[data-slot="badge"]'), null, "urgency is not a boxed badge")
  assert.match(focused.textContent!, /High priority/)
  const title = focused.querySelector("h2")!
  assert.ok(title.classList.contains("text-sm"), "the title is list-sized")
  const group = focused.querySelector('[aria-label="Task group"]')!
  const move = [...group.querySelectorAll("button")]
  assert.equal(move.length, 1, "the group is one control")
  assert.match(move[0]!.getAttribute("aria-label")!, /^Move task: Insurance/, "the name says what it moves and where it is now")
  assert.match(move[0]!.textContent!, /Insurance/)
  const complete = focused.querySelector('button[aria-label="Complete"]')!
  assert.ok(complete.classList.contains("rounded-full"), "completing is a circle, not a bar")
  assert.equal(complete.textContent, "", "with no label beside it")
  assert.equal([...focused.querySelectorAll("button")].filter((button) => /Complete/.test(button.textContent ?? "")).length, 0)
})

type HeaderProps = Partial<ComponentProps<typeof PatientIdentityView>> & Partial<ComponentProps<typeof PatientActionsView>>

const engagementNorth = { phone: "+15551234567", locations: [{ id: "location-north", name: "North Office" }] }

function headerView(props: HeaderProps = {}) {
  const shared = { engagement: engagementNorth, practiceID: "practice-1", route: "location-north", ...props }
  return (
    <DataProvider>
      <header>
        <PatientIdentityView onRoute={() => {}} {...shared} />
        <PatientActionsView
          patientTasksOpen={false}
          onPatientTasks={() => {}}
          calling={{ callingOccupied: false, outboundPending: false, ownsSoftphone: true, startOutbound: async () => undefined }}
          {...shared}
        />
      </header>
    </DataProvider>
  )
}

function header(props: HeaderProps = {}) {
  return renderToStaticMarkup(headerView(props)).split("</header>")[0]
}

function caller(names: string[], openTaskCount = 0) {
  return { names, openTaskCount }
}

test("the top bar names the patient by the one known name, else the number, with the number, office and Call", () => {
  const unnamed = header({ caller: caller([]) })
  assert.match(unnamed, /<h1[^>]*>\(555\) 123-4567<\/h1>/)
  assert.equal(header(), unnamed)
  assert.match(unnamed, />· North Office</)
  assert.match(unnamed, /aria-label="Copy phone number"/)
  assert.match(unnamed, /<button[^>]*title="Call \(555\) 123-4567"[^>]*>.*Call<\/button>/)
  assert.doesNotMatch(unnamed, /Used by/)

  const named = header({ caller: caller(["Dolores Reyes"]) })
  assert.match(named, /<h1[^>]*>Dolores Reyes<\/h1>/)
  assert.ok(named.indexOf("Dolores Reyes") < named.indexOf("(555) 123-4567"), "the number follows the name")

  const shared = header({ caller: caller(["Dolores Reyes", "Marco Reyes"]) })
  assert.match(shared, /<h1[^>]*>\(555\) 123-4567<\/h1>/)
  assert.match(shared, /data-testid="shared-phone-names"[^>]*>Used by Dolores Reyes, Marco Reyes</)
  assert.match(header({ caller: caller(["Dolores Reyes"]), callName: "Marco Reyes" }), />Used by Dolores Reyes, Marco Reyes</)
  assert.match(header({ caller: caller(["Dolores Reyes"]), callName: "dolores reyes" }), /<h1[^>]*>Dolores Reyes<\/h1>/)
})

test("a patient seen at several offices picks the office in the top bar, and Call waits for one", () => {
  const offices = { phone: "+15551234567", locations: [{ id: "location-north", name: "North Office" }, { id: "location-south", name: "South Office" }] }
  const unchosen = header({ engagement: offices, route: "" })
  assert.match(unchosen, /aria-label="Sender office"/)
  assert.match(unchosen, /<button[^>]*disabled=""[^>]*title="Call|<button[^>]*title="Call[^"]*"[^>]*disabled=""/)
  const chosen = header({ engagement: offices, route: "location-south" })
  assert.doesNotMatch(chosen, /<button[^>]*title="Call[^"]*"[^>]*disabled=""|<button[^>]*disabled=""[^>]*title="Call/)
  const action = header({ callAction: <button type="button">Call this Task</button> })
  assert.match(action, />Call this Task</)
  assert.doesNotMatch(action, /title="Call \(555\)/, "a Task's own call replaces the plain Call")
})

test("the chosen office reaches Call, and a Task call failure shows under the actions", async (t) => {
  const page = await harness(t)
  const offices = { phone: "+15551234567", locations: [{ id: "location-north", name: "North Office" }, { id: "location-south", name: "South Office" }] }
  const chosen: string[] = []
  const started: string[] = []
  const calling = { callingOccupied: false, outboundPending: false, ownsSoftphone: true, startOutbound: async (locationID: string) => { started.push(locationID); return undefined } }
  const render = (route: string) => page.render(headerView({ engagement: offices, route, onRoute: (id: string) => chosen.push(id), calling }))
  await render("")
  const office = page.host.querySelector<HTMLSelectElement>('select[aria-label="Sender office"]')!
  await act(async () => {
    office.value = "location-south"
    office.dispatchEvent(new window.Event("change", { bubbles: true }))
  })
  assert.deepEqual(chosen, ["location-south"])
  await render("location-south")
  const call = [...page.host.querySelectorAll("button")].find((button) => button.textContent?.trim() === "Call")!
  await act(async () => call.click())
  assert.deepEqual(started, ["location-south"])

  const failed = header({ callAction: <button type="button">Call this Task</button>, callError: "This Task cannot be called right now." })
  assert.match(failed, /role="alert"[^>]*>This Task cannot be called right now\.</)
})

test("the header reopens the patient panel, naming the open Tasks when there are some", async (t) => {
  const button = (markup: string) => /<button[^>]*aria-pressed="(true|false)"[^>]*>(?:<svg.*?<\/svg>)?([^<]*open Tasks?)<\/button>/.exec(markup)
  const toggle = (markup: string) => /<button[^>]*aria-pressed="(true|false)"[^>]*>(?:<svg.*?<\/svg>)?([^<]*)<\/button>/.exec(markup)?.[2]
  assert.equal(toggle(header({ caller: caller([], 0) })), "Patient", "the patient panel can always be reopened")
  assert.equal(toggle(header()), "Patient")
  assert.equal(button(header({ caller: caller([], 1) }))?.[2], "1 open Task")
  assert.equal(button(header({ caller: caller([], 5) }))?.[2], "5 open Tasks")
  assert.equal(button(header({ caller: caller([], 5) }))?.[1], "false")
  assert.equal(button(header({ caller: caller([], 5), patientTasksOpen: true }))?.[1], "true")

  const page = await harness(t)
  let activated = 0
  await page.render(headerView({ caller: caller([], 5), onPatientTasks: () => { activated += 1 } }))
  const control = [...page.host.querySelectorAll("button")].find((item) => item.textContent?.trim() === "5 open Tasks")!
  await act(async () => control.click())
  assert.equal(activated, 1)
})

function LiveDetail({ revision, children }: { revision: number; children: ReactNode }) {
  useLiveRefresh({ workspaceVersion: 0, detailRevision: revision })
  return children
}

test("a completion that failed or conflicted re-reads the summary, so no stale row remains", async (t) => {
  const page = await harness(t)
  const previousAPIURL = process.env.NEXT_PUBLIC_PORTAL_API_URL
  process.env.NEXT_PUBLIC_PORTAL_API_URL = "http://portal.test"
  clearAccessToken()
  t.after(() => {
    clearAccessToken()
    if (previousAPIURL === undefined) delete process.env.NEXT_PUBLIC_PORTAL_API_URL
    else process.env.NEXT_PUBLIC_PORTAL_API_URL = previousAPIURL
  })
  const [stale, kept] = fiveTasks
  let openTasks = [stale, kept]
  let reads = 0
  t.mock.method(globalThis, "fetch", async (input: RequestInfo | URL) => {
    const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url
    if (url === "/api/auth/token") return Response.json({ token: "synthetic-token" })
    assert.match(url, /\/v1\/engagements\/%2B15551234567\/summary\?/)
    reads += 1
    return Response.json({
      phone: "+15551234567", displayName: "", names: [], openTasks, openTaskCount: openTasks.length, closedToday: [],
    })
  })
  const { PatientTasksPanel } = await import("./patient-tasks-panel.tsx")
  const render = (revision: number, completion: ViewProps["completion"]) => page.render(
    <DataProvider>
      <LiveDetail revision={revision}>
        <PatientTasksPanel
          practiceID="practice-1"
          phone="+15551234567"
          completion={completion}
          onComplete={() => {}}
          onTaskUpdated={() => {}}
        />
      </LiveDetail>
    </DataProvider>,
  )
  const listed = () => [...page.host.querySelectorAll('[data-testid="patient-task-row"]')].map((row) => row.getAttribute("data-patient-task-id"))
  const settle = async (ready: () => boolean) => {
    for (let attempt = 0; attempt < 50 && !ready(); attempt++) {
      await act(async () => new Promise((resolve) => setTimeout(resolve, 0)))
    }
    assert.ok(ready(), "condition was not reached")
  }

  await render(1, { pending: [], errors: {} })
  await settle(() => listed().length === 2)
  assert.equal(reads, 1)

  openTasks = [kept]
  await render(2, { pending: [], errors: { [stale.id]: "This Task changed elsewhere." } })
  await settle(() => listed().length === 1)
  assert.equal(reads, 2, "the failed completion's detail revision re-read the summary")
  assert.deepEqual(listed(), [kept.id])
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
