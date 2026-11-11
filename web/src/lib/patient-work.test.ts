import assert from "node:assert/strict"
import test from "node:test"

import type { PatientWorkRow, Task } from "./api/generated/types.gen.ts"
import {
  nextAfterClose,
  offersAppointmentChecks,
  patientRowName,
  openTaskReason,
  patientListHeld,
  patientListHold,
  patientListShowing,
  type PatientListHoldEvent,
  patientWorkFooter,
  shownPatientWorkFilters,
} from "./patient-work.ts"

function top(overrides: Partial<Task> = {}): Task {
  return {
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
    createdAt: "2026-11-09T12:00:00Z",
    version: 1,
    updatedAt: "2026-11-09T12:00:00Z",
    relatedInteractionCount: 0,
    interactions: [],
    ...overrides,
  }
}

function row(overrides: Partial<PatientWorkRow> = {}): PatientWorkRow {
  return {
    phone: "+13055550142",
    displayName: "",
    openCount: 1,
    highPriority: false,
    hasUnowned: false,
    activityAt: "2026-11-09T12:00:00Z",
    top: top(),
    ...overrides,
  }
}

test("a patient row is named by its one known name, else by the phone", () => {
  assert.equal(patientRowName(row({ displayName: "Dolores Vega" })), "Dolores Vega")
  assert.equal(patientRowName(row()), "(305) 555-0142")
})

test("the second line says why the patient is waiting", () => {
  const cases: Array<[Partial<Task>, { voicemail: boolean; text: string }]> = [
    [{ origin: "VOICEMAIL_RECOVERY", title: "Review voicemail", voicemailDurationSeconds: 38 }, { voicemail: true, text: "Voicemail · 0:38" }],
    [{ origin: "VOICEMAIL_RECOVERY", title: "Review voicemail" }, { voicemail: true, text: "Voicemail" }],
    [{ origin: "MISSED_CALL_RECOVERY", title: "Return missed call" }, { voicemail: false, text: "Missed call, no voicemail" }],
    [{ origin: "INBOUND_MESSAGE_REVIEW", title: "Review inbound text", preview: "Is Dr. Shah in on Friday?" }, { voicemail: false, text: "Is Dr. Shah in on Friday?" }],
    [{ origin: "INBOUND_MESSAGE_REVIEW", title: "Review inbound text" }, { voicemail: false, text: "Review inbound text" }],
    [{ origin: "APPOINTMENT_REVIEW", title: "Verify Nov 14 appointment" }, { voicemail: false, text: "Verify Nov 14 appointment" }],
    [{}, { voicemail: false, text: "Send prior authorization form" }],
  ]
  for (const [overrides, reason] of cases) {
    assert.deepEqual(openTaskReason(top(overrides)), reason)
  }
})

test("filter chips count patients and hide the ones with nothing to offer", () => {
  const counts = { all: 9, followUp: 4, callBack: 3, texts: 2, appointments: 0, unowned: 0 }
  const values = (shown: ReturnType<typeof shownPatientWorkFilters>) => shown.map((filter) => filter.value)

  assert.deepEqual(shownPatientWorkFilters(counts, "all", false), [
    { value: "all", label: "All", count: 9 },
    { value: "followUp", label: "Tasks", count: 4 },
    { value: "callBack", label: "Call back", count: 3 },
    { value: "texts", label: "Texts", count: 2 },
  ])
  assert.deepEqual(values(shownPatientWorkFilters(counts, "all", true)), ["all", "followUp", "callBack", "texts", "appointments"])
  assert.deepEqual(values(shownPatientWorkFilters({ ...counts, appointments: 1 }, "all", false)), ["all", "followUp", "callBack", "texts", "appointments"])
  assert.deepEqual(values(shownPatientWorkFilters({ ...counts, unowned: 4 }, "all", false)), ["all", "followUp", "callBack", "texts", "unowned"])
  assert.deepEqual(values(shownPatientWorkFilters(counts, "unowned", false)), ["all", "followUp", "callBack", "texts", "unowned"], "the active filter stays reachable at zero")
  assert.deepEqual(values(shownPatientWorkFilters({ ...counts, followUp: 0 }, "all", false)), ["all", "followUp", "callBack", "texts"], "Tasks is offered at zero")
})

test("appointment checks are offered where Spring Hill is the office in view", () => {
  const springHill = { id: "spring-hill", name: " Spring Hill " }
  const hollywood = { id: "hollywood", name: "Hollywood" }
  assert.equal(offersAppointmentChecks([springHill], ""), true)
  assert.equal(offersAppointmentChecks([springHill, hollywood], "spring-hill"), true)
  assert.equal(offersAppointmentChecks([springHill, hollywood], ""), false)
  assert.equal(offersAppointmentChecks([springHill, hollywood], "hollywood"), false)
  assert.equal(offersAppointmentChecks([hollywood], ""), false)
})

test("the footer describes the scope in view and says what Everyone adds", () => {
  const totals = { openTasks: 14, patients: 9, patientsOutsideScope: 2 }
  assert.equal(patientWorkFooter(totals, "mine"), "14 open for 9 patients · 2 more under Everyone")
  assert.equal(patientWorkFooter(totals, "everyone"), "14 open for 9 patients")
  assert.equal(patientWorkFooter({ openTasks: 1, patients: 1, patientsOutsideScope: 0 }, "mine"), "1 open for 1 patient")
})

test("after a Task is closed the next selection stays with the patient, then the neighbouring row", () => {
  const phones = { a: "+13055550101", b: "+13055550102", c: "+13055550103", d: "+13055550104" }
  const patient = (name: keyof typeof phones, taskID = `${name}-1`) =>
    row({ phone: phones[name], top: top({ id: taskID, phone: phones[name] }) })
  const [a, b, c, d] = [patient("a"), patient("b"), patient("c"), patient("d")]
  for (const [label, before, after, closed, want] of [
    ["the patient still has an open item", [a, b, c], [a, patient("b", "b-2"), c], ["b-1"], "b-2"],
    ["the patient is done, a row followed", [a, b, c], [a, c], ["b-1"], "c-1"],
    ["the following row is gone too", [a, b, c, d], [a, d], ["b-1"], "d-1"],
    ["the patient was last in the list", [a, b], [a], ["b-1"], "a-1"],
    ["nothing follows or precedes that is still listed", [a, b, c], [d], ["b-1"], "d-1"],
    ["the list is now empty", [b], [], ["b-1"], undefined],
    ["the refreshed list still shows the closed Task", [a, b, c], [a, b, c], ["b-1"], "c-1"],
    ["two of the patient's Tasks closed at once", [a, b, c], [a, patient("b", "b-2"), c], ["b-1", "b-2"], "c-1"],
    ["the patient was not in the list", [a, c], [a, c], ["b-1"], "a-1"],
  ] as const) {
    assert.equal(
      nextAfterClose({ before, after, phone: phones.b, closedTaskIDs: closed })?.id,
      want,
      label,
    )
  }
})

test("the patient list holds its order and membership while the staff member is in it", () => {
  const phones = { a: "+13055550101", b: "+13055550102", c: "+13055550103", n: "+13055550109" }
  const patient = (name: keyof typeof phones, openCount = 1) =>
    row({ phone: phones[name], openCount, top: top({ id: `${name}-${openCount}`, phone: phones[name] }) })
  const [a, b, c, n] = [patient("a"), patient("b"), patient("c"), patient("n")]
  const incoming = (rows: PatientWorkRow[], loading = false): PatientListHoldEvent => ({ type: "incoming", rows, loading })
  const pointer: PatientListHoldEvent = { type: "hold", reason: "pointer" }
  const keyboard: PatientListHoldEvent = { type: "hold", reason: "keyboard" }
  const leave: PatientListHoldEvent = { type: "release", reason: "pointer" }
  const blur: PatientListHoldEvent = { type: "release", reason: "keyboard" }
  const acted: PatientListHoldEvent = { type: "acted" }
  const moving: PatientListHoldEvent = { type: "hold", reason: "moving" }
  const rest: PatientListHoldEvent = { type: "release", reason: "moving" }
  const b2 = patient("b", 2)

  const cases: Array<[string, PatientListHoldEvent[], PatientWorkRow[], boolean]> = [
    ["nobody is in the list, a new patient sorts to the top", [incoming([n, a, b, c])], [n, a, b, c], false],
    ["pointer in the list, a new patient sorts above", [pointer, incoming([n, a, b, c])], [a, b, c], true],
    ["pointer in the list, a row changes content", [pointer, incoming([a, b2, c])], [a, b2, c], false],
    ["pointer in the list, a row's last item closed elsewhere", [pointer, incoming([a, c])], [a, b, c], true],
    ["pointer in the list, the rows reorder", [pointer, incoming([c, a, b2])], [a, b2, c], true],
    ["pointer leaves, held changes apply", [pointer, incoming([n, a, c]), leave], [n, a, c], false],
    ["the pointer is on its way, a new patient sorts above", [moving, incoming([n, a, b, c])], [a, b, c], true],
    ["the pointer comes to rest outside the list", [moving, incoming([n, a, b, c]), rest], [n, a, b, c], false],
    ["the pointer arrives in the list and stops", [moving, incoming([n, a, b, c]), pointer, rest], [a, b, c], true],
    ["keyboard focus in the list holds too", [keyboard, incoming([n, a, b, c])], [a, b, c], true],
    ["focus leaves, held changes apply", [keyboard, incoming([n, a, b, c]), blur], [n, a, b, c], false],
    ["pointer leaves while focus stays", [pointer, keyboard, incoming([n, a, b, c]), leave], [a, b, c], true],
    ["the staff member clicks or presses a key", [pointer, incoming([n, a, b, c]), acted], [n, a, b, c], false],
    ["changes after their click are held again", [pointer, incoming([n, a, b, c]), acted, incoming([a, b, c])], [n, a, b, c], true],
    ["a touch holds until its click", [{ type: "hold", reason: "touch" }, incoming([n, a, b, c]), acted, { type: "release", reason: "touch" }], [n, a, b, c], false],
    ["a view they asked for starts loading", [pointer, incoming([], true)], [], false],
    ["a view they asked for arrives", [pointer, incoming([], true), incoming([c, n])], [c, n], false],
    ["more rows they asked for arrive", [pointer, incoming([a, b, c], true), incoming([a, b, c, n])], [a, b, c, n], false],
    ["the pointer is reported twice", [pointer, pointer, incoming([n, a, b, c]), leave], [n, a, b, c], false],
  ]
  for (const [label, events, rows, held] of cases) {
    const state = events.reduce(patientListHold, patientListShowing([a, b, c], false))
    assert.deepEqual(state.rows, rows, label)
    assert.equal(patientListHeld(state), held, label)
  }
})
