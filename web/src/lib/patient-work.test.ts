import assert from "node:assert/strict"
import test from "node:test"

import type { PatientWorkRow, Task } from "./api/generated/types.gen.ts"
import {
  nextAfterClose,
  offersAppointmentChecks,
  patientRowName,
  patientRowReason,
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
  const cases: Array<[Partial<PatientWorkRow>, { voicemail: boolean; text: string }]> = [
    [{ top: top({ origin: "VOICEMAIL_RECOVERY", title: "Review voicemail" }), voicemailDurationSeconds: 38 }, { voicemail: true, text: "Voicemail · 0:38" }],
    [{ top: top({ origin: "VOICEMAIL_RECOVERY", title: "Review voicemail" }) }, { voicemail: true, text: "Voicemail" }],
    [{ top: top({ origin: "MISSED_CALL_RECOVERY", title: "Return missed call" }) }, { voicemail: false, text: "Missed call, no voicemail" }],
    [{ top: top({ origin: "INBOUND_MESSAGE_REVIEW", title: "Review inbound text", preview: "Is Dr. Shah in on Friday?" }) }, { voicemail: false, text: "Is Dr. Shah in on Friday?" }],
    [{ top: top({ origin: "INBOUND_MESSAGE_REVIEW", title: "Review inbound text" }) }, { voicemail: false, text: "Review inbound text" }],
    [{ top: top({ origin: "APPOINTMENT_REVIEW", title: "Verify Nov 14 appointment" }) }, { voicemail: false, text: "Verify Nov 14 appointment" }],
    [{}, { voicemail: false, text: "Send prior authorization form" }],
  ]
  for (const [overrides, reason] of cases) {
    assert.deepEqual(patientRowReason(row(overrides)), reason)
  }
})

test("filter chips count patients and hide the ones with nothing to offer", () => {
  const counts = { all: 9, callBack: 3, texts: 2, appointments: 0, unowned: 0 }
  const values = (shown: ReturnType<typeof shownPatientWorkFilters>) => shown.map((filter) => filter.value)

  assert.deepEqual(shownPatientWorkFilters(counts, "all", false), [
    { value: "all", label: "All", count: 9 },
    { value: "callBack", label: "Call back", count: 3 },
    { value: "texts", label: "Texts", count: 2 },
  ])
  assert.deepEqual(values(shownPatientWorkFilters(counts, "all", true)), ["all", "callBack", "texts", "appointments"])
  assert.deepEqual(values(shownPatientWorkFilters({ ...counts, appointments: 1 }, "all", false)), ["all", "callBack", "texts", "appointments"])
  assert.deepEqual(values(shownPatientWorkFilters({ ...counts, unowned: 4 }, "all", false)), ["all", "callBack", "texts", "unowned"])
  assert.deepEqual(values(shownPatientWorkFilters(counts, "unowned", false)), ["all", "callBack", "texts", "unowned"], "the active filter stays reachable at zero")
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

test("the footer reconciles My Tasks with Everyone", () => {
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
