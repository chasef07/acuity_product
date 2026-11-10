import assert from "node:assert/strict"
import test, { type TestContext } from "node:test"
import { act, type ComponentProps, type ReactNode } from "react"
import { renderToStaticMarkup } from "react-dom/server"
import { JSDOM } from "jsdom"

import type { CallingCall, StaffTaskCloseReason, Task } from "../../lib/api/generated/types.gen.ts"
import { clearAccessToken } from "../../lib/auth-client.ts"
import { DataProvider } from "../../lib/data/provider.tsx"
import { useLiveRefresh } from "../../lib/data/queries.ts"
import { EngagementWorkspaceView } from "./engagement-workspace.tsx"
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
    listed.map((row) => /<span class="truncate">([^<]*)<\/span>/.exec(row)?.[1]),
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
  assert.doesNotMatch(listed[4], /<button/, "a text review title is text, because selecting it would close the panel")
  assert.match(listed[4], /Synthetic fifth request/)
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

test("loading holds the heading and one row of space so the Task detail below does not jump", () => {
  const current = fiveTasks[0]
  for (const currentTaskID of [current.id, undefined]) {
    const loading = renderToStaticMarkup(view({ summary: { status: "loading" }, currentTaskID }))
    assert.match(loading, /aria-busy="true"/)
    assert.match(loading, /<h2[^>]*>Open Tasks<\/h2>/)
    assert.match(loading, /Loading open Tasks/)
    assert.equal(loading.match(/data-slot="skeleton"/g)?.length, 3)
  }
  const one = renderToStaticMarkup(view({ summary: ready([current]), currentTaskID: current.id }))
  assert.equal(rows(one).length, 1, "the one Task takes the row the skeleton held")
  assert.match(rows(one)[0], /aria-current="true"/)
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
  await act(async () => toggle()!.click())
  assert.equal(shown(), 50)
  assert.match(page.host.textContent ?? "", /Showing 50 of 63/)
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

test("rows scroll beneath a heading as tall as the close button, so the close menu is never covered", () => {
  const markup = renderToStaticMarkup(view({ summary: ready(fiveTasks) }))
  const [, sectionClass] = /<section[^>]*class="([^"]*)"/.exec(markup) ?? []
  const [, headerClass] = /<header class="([^"]*)"/.exec(markup) ?? []
  const [, bodyClass] = /data-testid="patient-tasks-body" class="([^"]*)"/.exec(markup) ?? []
  assert.match(sectionClass ?? "", /\bpt-3\b/, "the heading starts where the close button starts")
  assert.match(headerClass ?? "", /\bh-7\b/, "and is as tall as it")
  assert.match(headerClass ?? "", /\bshrink-0\b/)
  assert.match(headerClass ?? "", /\bpr-12\b/, "and ends before it")
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
  const closed = markup.slice(markup.indexOf("Closed today"))
  assert.match(closed, /<div class="min-w-0 flex-1"><p class="truncate /)
})

test("a failed or stale read is said, with a way to retry", () => {
  const failure = { kind: "unavailable", retryable: true } as const
  const failed = renderToStaticMarkup(view({ summary: { status: "failed", failure }, currentTaskID: "task-first" }))
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
        view="none"
        taskCallError=""
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
  assert.equal(markup.match(/overflow-y-auto/g)?.length, 1, "header, list and details scroll as one")
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

test("a selected Task shows the patient's Tasks above its detail, for one Task and for a group", () => {
  const task = fiveTasks[0]
  const single = context({ task, view: "task" })
  assert.deepEqual(single.asked, [[task.phone, { beside: "task", currentTaskID: task.id }]])
  assert.ok(single.markup.indexOf("patient tasks for") < single.markup.indexOf('aria-label="Focused Task"'))
  const group = { ...task, groupMembers: [task, fiveTasks[1]] }
  const grouped = context({ task, group, taskRows: [group], onSelectTask: () => {}, view: "task" })
  assert.deepEqual(grouped.asked, [[task.phone, { beside: "task", currentTaskID: task.id }]])
  assert.ok(grouped.markup.indexOf("patient tasks for") < grouped.markup.indexOf('aria-label="Related Tasks"'))
  assert.deepEqual(context({}).asked, [])
})

type HeaderProps = Partial<ComponentProps<typeof EngagementWorkspaceView>>

function headerView(props: HeaderProps = {}) {
  return (
    <DataProvider>
      <EngagementWorkspaceView
        practiceName="Synthetic Practice"
        engagement={{ phone: "+15551234567", locations: [{ id: "location-north", name: "North Office" }] }}
        practiceID="practice-1"
        patientTasksOpen={false}
        onPatientTasks={() => {}}
        onTaskCreated={() => {}}
        onTaskOpen={() => {}}
        onCallOpen={() => {}}
        onAIInteractionOpen={() => {}}
        calling={{ callingOccupied: false, outboundPending: false, ownsSoftphone: false, startOutbound: async () => undefined }}
        {...props}
      />
    </DataProvider>
  )
}

function header(props: HeaderProps = {}) {
  return renderToStaticMarkup(headerView(props)).split("</header>")[0]
}

function caller(names: string[], openTaskCount = 0) {
  return { names, openTaskCount }
}

test("the conversation header names the caller only when one name is known for the number", () => {
  const unnamed = header({ caller: caller([]) })
  assert.match(unnamed, /<h1[^>]*>\(555\) 123-4567<\/h1>/)
  assert.doesNotMatch(unnamed, /caller-name|used by/)
  assert.equal(header(), unnamed)
  const named = header({ caller: caller(["Dolores Reyes"]) })
  assert.ok(named.indexOf("Dolores Reyes") < named.indexOf("(555) 123-4567"))
  assert.match(named, /<h1[^>]*>\(555\) 123-4567<\/h1>/, "the number stays the heading")
  assert.doesNotMatch(named, /used by/)
})

test("a shared phone is titled by its number and lists every name without favouring one", () => {
  const names = ["Dolores Reyes", "Marco Reyes", "Ana Reyes", "Luis Reyes", "Rosa Reyes"]
  const shared = header({ caller: caller(names) })
  assert.doesNotMatch(shared, /caller-name/, "no single name stands as the caller")
  assert.match(shared, /<h1[^>]*>\(555\) 123-4567<\/h1>/)
  const [, noteClass, note] = /data-testid="shared-phone-names" class="([^"]*)">([^<]*)</.exec(shared) ?? []
  assert.equal(note, "used by Dolores Reyes, Marco Reyes, Ana Reyes, Luis Reyes, Rosa Reyes")
  assert.doesNotMatch(noteClass ?? "", /truncate/, "no name is cut off")
})

test("a call under a different name is shown as a shared phone, never as two callers", () => {
  const differs = header({ caller: caller(["Dolores Reyes"]), callName: "Marco Reyes" })
  assert.doesNotMatch(differs, /caller-name/)
  assert.match(differs, />used by Dolores Reyes, Marco Reyes</)
  const same = header({ caller: caller(["Dolores Reyes"]), callName: "dolores reyes" })
  assert.match(same, /data-testid="caller-name"[^>]*>Dolores Reyes</)
  assert.doesNotMatch(same, /used by/)
  const onlyCall = header({ caller: caller([]), callName: "Marco Reyes" })
  assert.match(onlyCall, /data-testid="caller-name"[^>]*>Marco Reyes</)
  const third = header({ caller: caller(["Dolores Reyes", "Ana Reyes"]), callName: "Marco Reyes" })
  assert.match(third, />used by Dolores Reyes, Ana Reyes, Marco Reyes</)
})

test("the header offers the patient's open Tasks by count and stays quiet when there are none", async (t) => {
  const button = (markup: string) => /<button[^>]*aria-pressed="(true|false)"[^>]*>([^<]*open Tasks?)<\/button>/.exec(markup)
  assert.equal(button(header({ caller: caller([], 0) })), null)
  assert.equal(button(header()), null, "nothing is offered before the summary loads")
  assert.equal(button(header({ caller: caller([], 1) }))?.[2], "1 open Task")
  assert.equal(button(header({ caller: caller([], 5) }))?.[2], "5 open Tasks")
  assert.equal(button(header({ caller: caller([], 5) }))?.[1], "false")
  assert.equal(button(header({ caller: caller([], 5), patientTasksOpen: true }))?.[1], "true")

  const page = await harness(t)
  let activated = 0
  await page.render(headerView({ caller: caller([], 5), onPatientTasks: () => { activated += 1 } }))
  const control = [...page.host.querySelectorAll("button")].find((item) => item.textContent === "5 open Tasks")!
  await act(async () => control.click())
  assert.equal(activated, 1)
})

function LiveDetail({ revision, children }: { revision: number; children: ReactNode }) {
  useLiveRefresh({ workspaceVersion: 0, taskCounts: "", detailRevision: revision })
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

  await render(1, { pendingTaskID: "", errorTaskID: "", error: "" })
  await settle(() => listed().length === 2)
  assert.equal(reads, 1)

  openTasks = [kept]
  await render(2, { pendingTaskID: "", errorTaskID: stale.id, error: "This Task changed elsewhere." })
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
