import assert from "node:assert/strict"
import test from "node:test"
import { renderToStaticMarkup } from "react-dom/server"
import { JSDOM } from "jsdom"

import { SidebarProvider } from "../ui/sidebar.tsx"
import type {
  PatientWorkFilter,
  PatientWorkRow,
  Task,
} from "../../lib/api/generated/types.gen.ts"
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
    ["unowned", patients.unowned, "Hank Okafor", "Referral to retina specialist"],
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

test("a voicemail row shows the play glyph and its length, and a missed call says there is none", () => {
  const missed = projectedPatient({ ...railTask, id: "missed", phone: "+13055550171", origin: "MISSED_CALL_RECOVERY", title: "Return missed call" })
  const document = rail(projection([patients.callBack, missed]))
  const voicemail = document.querySelector(`[data-phone='${patients.callBack.phone}'] [data-testid='patient-row-reason']`)!
  assert.equal(voicemail.textContent, "Voicemail · 0:38")
  assert.ok(voicemail.querySelector("svg.lucide-play"))
  const missedReason = document.querySelector(`[data-phone='${missed.phone}'] [data-testid='patient-row-reason']`)!
  assert.equal(missedReason.textContent, "Missed call, no voicemail")
  assert.equal(missedReason.querySelector("svg"), null)
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

test("the scope toggle offers My Tasks and Everyone and presses the current one", () => {
  const pressed = (state: WorkspaceProjectionState) =>
    [...rail(state).querySelectorAll("[aria-label='Whose work'] button")]
      .map((button) => [button.textContent, button.getAttribute("aria-pressed")])
  const base = projection([patients.callBack])
  assert.deepEqual(pressed(base), [["My Tasks", "true"], ["Everyone", "false"]])
  assert.deepEqual(
    pressed({ ...base, rail: { ...base.rail, scope: "everyone" } }),
    [["My Tasks", "false"], ["Everyone", "true"]],
  )
})

test("filter chips count patients, and No owner appears in the destructive tone only when something has no owner", () => {
  const chips = (state: WorkspaceProjectionState) =>
    [...rail(state).querySelectorAll("[aria-label='Filter patients'] button")]
  const base = projection([patients.callBack])
  assert.deepEqual(
    chips(base).map((chip) => chip.textContent),
    ["All9", "Tasks4", "Call back3", "Texts2", "Appointments1"],
  )
  const unowned = chips({
    ...base,
    patients: { ...base.patients, counts: { ...base.patients.counts, unowned: 4 } },
  }).at(-1)!
  assert.equal(unowned.textContent, "No owner4")
  assert.equal(unowned.getAttribute("data-filter"), "unowned")
  assert.match(unowned.getAttribute("class") ?? "", /\btext-destructive\b/)
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

test("the active chip is filled with the primary colour, and No owner fills with its own", () => {
  const base = projection([patients.callBack])
  const counted = { ...base, patients: { ...base.patients, counts: { ...base.patients.counts, unowned: 4 } } }
  const classes = (filter: PatientWorkFilter) => {
    const document = rail({ ...counted, rail: { ...counted.rail, filter } })
    return new Map(
      [...document.querySelectorAll("[aria-label='Filter patients'] button")]
        .map((chip) => [chip.getAttribute("data-filter"), (chip.getAttribute("class") ?? "").split(/\s+/)] as const),
    )
  }
  for (const filter of ["all", "followUp", "callBack", "texts", "appointments"] as const) {
    const chips = classes(filter)
    for (const [name, chip] of chips) {
      assert.equal(chip.includes("bg-primary") && chip.includes("text-primary-foreground"), name === filter, `${name} while ${filter} is active`)
    }
    assert.ok(chips.get("unowned")!.includes("text-destructive"), "No owner keeps its tone while inactive")
  }
  const unowned = classes("unowned").get("unowned")!
  assert.ok(unowned.includes("bg-destructive") && unowned.includes("text-primary-foreground"))
  assert.equal(unowned.includes("text-destructive"), false)
})

test("the footer states the totals and what My Tasks leaves under Everyone", () => {
  const footer = (state: WorkspaceProjectionState) =>
    rail(state).querySelector("[data-testid='patient-work-footer']")?.textContent
  const base = projection([patients.callBack])
  assert.equal(footer(base), "14 open for 9 patients · 2 more under Everyone")
  assert.equal(footer({ ...base, rail: { ...base.rail, scope: "everyone" } }), "14 open for 9 patients")
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
      <CompletedTaskRow task={task} active={false} onSelect={() => {}} />
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
