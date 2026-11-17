import assert from "node:assert/strict"
import test, { type TestContext } from "node:test"
import { setTimeout as sleep } from "node:timers/promises"
import { act } from "react"
import { renderToStaticMarkup } from "react-dom/server"
import { JSDOM } from "jsdom"

import { SidebarProvider } from "../ui/sidebar.tsx"
import type {
  PatientWorkFilter,
  PatientWorkRow,
  Task,
} from "../../lib/api/generated/types.gen.ts"
import { rowClickGuardMilliseconds } from "../../lib/patient-work.ts"
import type { WorkspaceProjectionState } from "../../lib/workspace-projection.ts"
import { projectedPatient, projectedWorkspace } from "./workspace-projection.fixture.ts"
import { CompletedTaskRow } from "./workspace-rail-patient-row.tsx"
import { WorkspaceRail } from "./workspace-rail.tsx"
import { WorkspaceWindowFailure } from "./workspace-window-failure.tsx"

test("workspace window failure exposes one accessible retry control", () => {
  const failure = (
    <WorkspaceWindowFailure
      message="Texts are temporarily unavailable."
      onRetry={() => {}}
    />
  )
  const html = renderToStaticMarkup(failure)

  assert.match(html, /role="alert"/)
  assert.match(html, /Texts are temporarily unavailable\./)
  assert.match(html, /<button type="button"[^>]*>Retry<\/button>/)
})

const railTask: Task = {
  id: "task-1",
  practiceId: "practice-1",
  locationId: "location-1",
  locationName: "Downtown",
  phone: "+13055550142",
  title: "Send prior authorization form",
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

const patients: Record<Exclude<PatientWorkFilter, "all">, PatientWorkRow> = {
  followUp: projectedPatient(
    { ...railTask, id: "follow-up", phone: "+13055550177", origin: "HUMAN_CALL_FOLLOW_UP", title: "Mail the surgery packet" },
    { displayName: "Marta Ruiz" },
  ),
  callBack: projectedPatient(
    { ...railTask, id: "voicemail", phone: "+13055550142", origin: "VOICEMAIL_RECOVERY", title: "Review voicemail", voicemailDurationSeconds: 38 },
    { displayName: "Dolores Vega", openCount: 5 },
  ),
  texts: projectedPatient(
    { ...railTask, id: "text", phone: "+13525550109", origin: "INBOUND_MESSAGE_REVIEW", title: "Review inbound text", preview: "Is Dr. Shah in on Friday?" },
    { displayName: "Priya Raman", openCount: 2 },
  ),
  appointments: projectedPatient(
    { ...railTask, id: "appointment", phone: "+13525550110", origin: "APPOINTMENT_REVIEW", title: "Verify Nov 14 appointment" },
  ),
  unowned: projectedPatient(
    { ...railTask, id: "unowned", phone: "+13055550126", title: "Referral to retina specialist", urgency: "high_priority" },
    { displayName: "Hank Okafor", hasUnowned: true },
  ),
}

function projection(
  rows: PatientWorkRow[],
  overrides: Partial<WorkspaceProjectionState> = {},
): WorkspaceProjectionState {
  const base = projectedWorkspace(rows[0]?.top ?? railTask)
  return {
    ...base,
    patients: {
      ...base.patients,
      items: rows,
      counts: { all: 9, followUp: 4, callBack: 3, texts: 2, appointments: 1, unowned: 0 },
      totals: { openTasks: 14, patients: 9, patientsOutsideScope: 2 },
    },
    ...overrides,
  }
}

function rail(state: WorkspaceProjectionState) {
  const html = renderToStaticMarkup(
    <SidebarProvider>
      <WorkspaceRail projection={state} onIntent={() => {}} />
    </SidebarProvider>,
  )
  return new JSDOM(html).window.document
}

function rowText(document: Document, phone: string, part: "name" | "count" | "reason") {
  return document
    .querySelector(`[data-testid='patient-row'][data-phone='${phone}'] [data-testid='patient-row-${part}']`)
    ?.textContent
}

test("each filter lists one row per patient with why that patient is waiting", () => {
  for (const [filter, row, name, reason] of [
    ["followUp", patients.followUp, "Marta Ruiz", "Mail the surgery packet"],
    ["callBack", patients.callBack, "Dolores Vega", "Voicemail · 0:38"],
    ["texts", patients.texts, "Priya Raman", "Is Dr. Shah in on Friday?"],
    ["appointments", patients.appointments, "(352) 555-0110", "Verify Nov 14 appointment"],
  ] as const) {
    const base = projection([row])
    const document = rail({ ...base, rail: { ...base.rail, filter } })
    assert.equal(document.querySelectorAll("[data-testid='patient-row']").length, 1, filter)
    assert.equal(rowText(document, row.phone, "name"), name)
    assert.equal(rowText(document, row.phone, "reason"), reason)
    assert.equal(
      document.querySelector("[aria-label='Filter patients'] [aria-pressed='true']")?.getAttribute("data-filter"),
      filter,
    )
  }
  const all = rail(projection(Object.values(patients)))
  assert.deepEqual(
    [...all.querySelectorAll("[data-testid='patient-row']")].map((row) => row.getAttribute("data-phone")),
    Object.values(patients).map((row) => row.phone),
    "rows keep the order the server gave",
  )
  assert.equal(all.querySelector("[aria-label='Filter patients'] [aria-pressed='true']")?.getAttribute("data-filter"), "all")
})

test("each row names what is waiting with a small icon, and a voicemail shows its length", () => {
  const row = (id: string, origin: Task["origin"], phone: string) =>
    projectedPatient({ ...railTask, id, phone, origin, title: `Synthetic ${id}` })
  const cases = [
    [row("missed", "MISSED_CALL_RECOVERY", "+13055550181"), "lucide-phone-missed"],
    [row("texted", "INBOUND_MESSAGE_REVIEW", "+13055550182"), "lucide-message-square"],
    [row("booked-ai", "APPOINTMENT_REVIEW", "+13055550183"), "lucide-calendar-check"],
    [row("abita-call", "ABITA_AI", "+13055550184"), "lucide-phone-incoming"],
    [row("staff-follow-up", "HUMAN_CALL_FOLLOW_UP", "+13055550185"), "lucide-phone"],
  ] as const
  const document = rail(projection([patients.callBack, ...cases.map(([patient]) => patient)]))
  const reason = (phone: string) => document.querySelector(`[data-phone='${phone}'] [data-testid='patient-row-reason']`)!
  assert.equal(reason(patients.callBack.phone).textContent, "Voicemail · 0:38")
  const icons = (phone: string) => document.querySelectorAll(`[data-phone='${phone}'] button svg`)
  assert.ok(icons(patients.callBack.phone)[0]!.classList.contains("lucide-voicemail"))
  assert.equal(reason(patients.callBack.phone).querySelector("svg"), null, "the reason line is text only")
  assert.equal(reason(cases[0][0].phone).textContent, "Missed call, no voicemail")
  for (const [patient, icon] of cases) {
    const shown = icons(patient.phone)
    assert.equal(shown.length, 1, `${patient.top.origin} has one icon`)
    assert.ok(shown[0]!.classList.contains(icon), `${patient.top.origin} shows ${icon}`)
    assert.equal(shown[0]!.getAttribute("aria-hidden"), "true")
  }
})

test("each row's time is how long the patient has been waiting, from their oldest open request", () => {
  const waiting = projectedPatient(railTask, { activityAt: "2026-11-09T12:00:00Z", waitingSince: "2026-11-09T09:00:00Z" })
  const document = rail(projection([waiting]))
  const time = document.querySelector(`[data-phone='${waiting.phone}'] time`)!
  assert.equal(time.getAttribute("datetime"), "2026-11-09T09:00:00Z")
  assert.match(time.getAttribute("title") ?? "", /^Waiting since /)
})

test("the count pill shows only when a patient has more than one thing open", () => {
  const document = rail(projection([patients.callBack, patients.appointments]))
  assert.equal(rowText(document, patients.callBack.phone, "count"), "5 open")
  assert.equal(rowText(document, patients.appointments.phone, "count"), undefined)
})

test("a high-priority patient carries the marker and the selected patient's row is active", () => {
  const selected = patients.texts.top
  const base = projection([patients.unowned, patients.texts])
  const document = rail({
    ...base,
    selection: { ...base.selection, task: selected, engagement: { phone: selected.phone, locations: [] } },
  })
  const urgent = document.querySelector(`[data-phone='${patients.unowned.phone}'] button`)!
  assert.equal(document.getElementById(urgent.getAttribute("aria-describedby")!)?.textContent, "High priority")
  const calm = document.querySelector(`[data-phone='${patients.texts.phone}'] button`)!
  assert.equal(calm.getAttribute("aria-describedby"), null)
  assert.equal(calm.hasAttribute("data-active"), true)
  assert.equal(urgent.hasAttribute("data-active"), false)
})

test("the patient list has no My Tasks or Everyone switch", () => {
  assert.equal(rail(projection([patients.callBack])).querySelector("[aria-label='Whose work']"), null)
})
test("filter chips count patients in one quiet tone and never offer No owner", () => {
  const chips = (state: WorkspaceProjectionState) =>
    [...rail(state).querySelectorAll("[aria-label='Filter patients'] button")]
  const base = projection([patients.callBack])
  const counted = { ...base, patients: { ...base.patients, counts: { ...base.patients.counts, unowned: 4 } } }
  assert.deepEqual(
    chips(counted).map((chip) => chip.textContent),
    ["All9", "Tasks4", "Call back3", "Texts2", "Appointments1"],
  )
  assert.ok(chips(counted).every((chip) => !(chip.getAttribute("class") ?? "").split(/\s+/).some((name) => /^(text|bg)-destructive$/.test(name))))
  const withoutAppointments = chips({
    ...base,
    patients: { ...base.patients, counts: { ...base.patients.counts, appointments: 0 } },
  })
  assert.deepEqual(
    withoutAppointments.map((chip) => chip.getAttribute("data-filter")),
    ["all", "followUp", "callBack", "texts"],
    "Appointments is offered only where appointment checks exist",
  )
})

test("the active chip is filled with the primary colour", () => {
  const base = projection([patients.callBack])
  for (const filter of ["all", "followUp", "callBack", "texts", "appointments"] as const) {
    const document = rail({ ...base, rail: { ...base.rail, filter } })
    for (const chip of document.querySelectorAll("[aria-label='Filter patients'] button")) {
      const classes = (chip.getAttribute("class") ?? "").split(/\s+/)
      const name = chip.getAttribute("data-filter")
      assert.equal(classes.includes("bg-primary") && classes.includes("text-primary-foreground"), name === filter, `${name} while ${filter} is active`)
    }
  }
})

test("the footer states the totals", () => {
  const footer = rail(projection([patients.callBack])).querySelector("[data-testid='patient-work-footer']")?.textContent
  assert.equal(footer, "14 open for 9 patients")
})

test("the list says when it is loading, empty, failed, or has more to show", () => {
  const base = projection([])
  const list = (patientsWindow: Partial<WorkspaceProjectionState["patients"]>) =>
    rail({ ...base, patients: { ...base.patients, ...patientsWindow } })
      .querySelector("[aria-label='Patients with open work']")!
  assert.match(list({}).textContent ?? "", /Nothing waiting here\./)
  const loading = list({ loading: true }).textContent ?? ""
  assert.match(loading, /Loading patients/)
  assert.doesNotMatch(loading, /Nothing waiting here/)
  const failed = list({ error: "Open work is temporarily unavailable." })
  assert.match(failed.querySelector("[role='alert']")?.textContent ?? "", /Open work is temporarily unavailable\./)
  assert.doesNotMatch(failed.textContent ?? "", /Nothing waiting here/)
  const more = list({ items: [patients.callBack], nextCursor: "next" })
  assert.ok([...more.querySelectorAll("button")].some((button) => button.textContent === "Show more"))
  assert.equal([...list({ items: [patients.callBack] }).querySelectorAll("button")].some((button) => button.textContent === "Show more"), false)
})

function completedRow(task: Task) {
  return renderToStaticMarkup(
    <SidebarProvider>
      <CompletedTaskRow task={task} active={false} refused={false} onSelect={() => {}} />
    </SidebarProvider>,
  )
}

test("a completed Task row reads why it closed and who closed it", () => {
  const html = completedRow({
    ...railTask,
    state: "COMPLETED",
    completedBy: { kind: "HUMAN", subject: "user-1", email: "ines@abita.test" },
    completedAt: "2026-08-30T13:00:00Z",
    closeReason: "NOT_NEEDED",
    version: 2,
  })
  assert.match(html, /Not needed, by ines@abita\.test/)
  assert.doesNotMatch(html, /Close Task as|Complete Task:/)
})

test("a completed Task row credits the team when no email is known and stays quiet when no reason was recorded", () => {
  const completed = {
    ...railTask,
    state: "COMPLETED",
    completedBy: { kind: "HUMAN", subject: "user-1" },
    completedAt: "2026-08-30T13:00:00Z",
    version: 2,
  } as const
  assert.match(completedRow({ ...completed, closeReason: "DONE" }), /Done, by the team/)
  const withoutReason = completedRow(completed)
  assert.match(withoutReason, /Send prior authorization form/)
  assert.doesNotMatch(withoutReason, /undefined|by the team|text-xs leading-4 text-muted-foreground/)
})

test("patient rows carry no tick and no close menu", () => {
  const html = rail(projection(Object.values(patients))).body.innerHTML
  assert.doesNotMatch(html, /Complete Task:|Close Task as:/)
})

async function mountedRail(t: TestContext, initial: WorkspaceProjectionState) {
  const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost/workspace" })
  Object.defineProperties(dom.window, {
    requestAnimationFrame: { configurable: true, value: () => 0 },
    cancelAnimationFrame: { configurable: true, value: () => {} },
    matchMedia: { configurable: true, value: () => ({ matches: false, addEventListener() {}, removeEventListener() {} }) },
  })
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
  const host = dom.window.document.createElement("div")
  dom.window.document.body.append(host)
  const { createRoot } = await import("react-dom/client")
  const root = createRoot(host)
  t.after(async () => {
    await act(async () => root.unmount())
    dom.window.close()
  })
  const opened: string[] = []
  const show = (state: WorkspaceProjectionState) => act(async () => root.render(
    <SidebarProvider>
      <WorkspaceRail
        projection={state}
        onIntent={(intent) => {
          if (intent.type === "select-task") opened.push(intent.task.phone)
        }}
      />
    </SidebarProvider>,
  ))
  await show(initial)
  const activateRow = (row: string, detail: number) => act(async () => {
    host.querySelector(`${row} button`)!
      .dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true, detail }))
  })
  const activate = (phone: string, detail: number) =>
    activateRow(`[data-testid='patient-row'][data-phone='${phone}']`, detail)
  const completedRow = (taskID: string) => `[data-testid='completed-task-row'][data-task-id='${taskID}']`
  return {
    show,
    opened,
    pointerClick: (phone: string) => activate(phone, 1),
    keyboardActivate: (phone: string) => activate(phone, 0),
    pointerClickCompleted: (taskID: string) => activateRow(completedRow(taskID), 1),
    keyboardActivateCompleted: (taskID: string) => activateRow(completedRow(taskID), 0),
    toggleCompleted: () => act(async () => {
      [...host.querySelectorAll("button")].find((button) => button.textContent === "Recently completed")!
        .dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true, detail: 1 }))
    }),
    completedText: (taskID: string) => host.querySelector(`${completedRow(taskID)} button`)!.textContent,
    phones: () => [...host.querySelectorAll("[data-testid='patient-row']")].map((item) => item.getAttribute("data-phone")),
    notice: () => host.querySelector("[data-testid='patient-list-updated']")!.textContent,
    refused: () => [...host.querySelectorAll("[data-testid='patient-row']")]
      .filter((item) => item.querySelector("[data-testid='row-refused']"))
      .map((item) => item.getAttribute("data-phone")),
    reason: (phone: string) => host.querySelector(`[data-testid='patient-row'][data-phone='${phone}'] button > span > span:last-child`)!.textContent,
    wait: (milliseconds: number) => act(() => sleep(milliseconds)),
  }
}

test("the list follows live work at once and refuses a pointer click only where the patient under the pointer just changed", async (t) => {
  const { followUp, callBack, texts, unowned } = patients
  const base = projection([callBack, texts, followUp])
  const withRows = (rows: PatientWorkRow[]) => ({ ...base, patients: { ...base.patients, items: rows } })
  const page = await mountedRail(t, base)
  assert.equal(page.notice(), "")

  await page.show(withRows([callBack, { ...texts, top: { ...texts.top, preview: "Thanks!" } }, followUp]))
  assert.equal(page.notice(), "", "a row that only refreshed in place changed no position")
  await page.pointerClick(texts.phone)
  assert.deepEqual(page.opened, [texts.phone])

  await page.show(withRows([callBack, texts, unowned, followUp]))
  assert.deepEqual(page.phones(), [callBack.phone, texts.phone, unowned.phone, followUp.phone], "the new patient is listed at once")
  assert.equal(page.notice(), "List just updated")
  await page.pointerClick(texts.phone)
  assert.deepEqual(page.opened, [texts.phone, texts.phone], "a row above the arrival did not move and opens")
  assert.deepEqual(page.refused(), [])
  await page.pointerClick(unowned.phone)
  assert.deepEqual(page.opened, [texts.phone, texts.phone], "the click that may have been aimed at the patient who was there opens nothing")
  assert.deepEqual(page.refused(), [unowned.phone], "the clicked row says why nothing opened")
  assert.equal(page.reason(unowned.phone), "List just updated, click again")
  await page.keyboardActivate(unowned.phone)
  assert.deepEqual(page.opened, [texts.phone, texts.phone, unowned.phone], "Enter on the focused row opens it without waiting")

  await page.wait(rowClickGuardMilliseconds + 50)
  assert.equal(page.notice(), "")
  assert.deepEqual(page.refused(), [])
  assert.equal(page.reason(unowned.phone), "Referral to retina specialist")
  await page.pointerClick(unowned.phone)
  assert.deepEqual(page.opened.slice(3), [unowned.phone])

  await page.show(withRows([callBack, texts, followUp]))
  await page.pointerClick(texts.phone)
  await page.pointerClick(followUp.phone)
  assert.deepEqual(page.opened.slice(4), [texts.phone], "a patient left: only the positions below it refuse")
  assert.deepEqual(page.refused(), [followUp.phone])

  await page.show(withRows([]))
  await page.show(withRows([followUp, texts]))
  assert.equal(page.notice(), "", "a list the staff member asked for starts clean")
  assert.deepEqual(page.refused(), [])
  await page.pointerClick(followUp.phone)
  assert.deepEqual(page.opened.slice(5), [followUp.phone])
})

test("a Recently completed row refuses a pointer click just after the patient list above it grew or shrank", async (t) => {
  const { followUp, callBack, texts, unowned } = patients
  const done: Task = {
    ...railTask,
    id: "done",
    phone: "+13055550199",
    title: "Fax the referral",
    state: "COMPLETED",
    completedBy: { kind: "HUMAN", subject: "user-1", email: "ines@abita.test" },
    completedAt: "2026-08-30T13:00:00Z",
    closeReason: "DONE",
    version: 2,
  }
  const base = projection([callBack, texts])
  const withRows = (rows: PatientWorkRow[]) => ({
    ...base,
    patients: { ...base.patients, items: rows },
    completedTasks: { ...base.completedTasks, items: [done] },
    rail: { ...base.rail, completedExpanded: true },
  })
  const page = await mountedRail(t, withRows([callBack, texts]))
  await page.wait(rowClickGuardMilliseconds + 50)

  await page.show(withRows([callBack, unowned]))
  await page.pointerClickCompleted(done.id)
  assert.deepEqual(page.opened, [done.phone], "one patient took another's place: nothing below the list moved")

  await page.show(withRows([followUp, callBack, unowned]))
  await page.pointerClickCompleted(done.id)
  assert.deepEqual(page.opened, [done.phone], "a patient arrived: the click may have been aimed at what was there")
  assert.match(page.completedText(done.id) ?? "", /Fax the referralList just updated, click again$/)
  await page.keyboardActivateCompleted(done.id)
  assert.deepEqual(page.opened, [done.phone, done.phone], "Enter on the focused row opens it without waiting")

  await page.wait(rowClickGuardMilliseconds + 50)
  assert.equal(page.completedText(done.id), "Fax the referralDone, by ines@abita.test")
  await page.pointerClickCompleted(done.id)
  assert.deepEqual(page.opened.slice(2), [done.phone])

  await page.show(withRows([followUp, callBack]))
  await page.pointerClickCompleted(done.id)
  assert.deepEqual(page.opened.slice(3), [], "a patient left: the rows below moved up")

  await page.toggleCompleted()
  await page.pointerClickCompleted(done.id)
  assert.deepEqual(page.opened.slice(3), [done.phone], "the staff member opened or closed the group themselves: its rows start clean")
})
