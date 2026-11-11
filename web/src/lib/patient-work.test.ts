import assert from "node:assert/strict"
import test from "node:test"

import type { PatientWorkRow, Task } from "./api/generated/types.gen.ts"
import {
  nextAfterClose,
  offersAppointmentChecks,
  patientRowName,
  openTaskReason,
  patientWorkFooter,
  rowChangedAtAfter,
  rowClickAllowed,
  rowClickGuardMilliseconds,
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

test("filter chips count patients, No owner is always offered and Appointments only where it has something to offer", () => {
  const counts = { all: 9, followUp: 4, callBack: 3, texts: 2, appointments: 0, unowned: 0 }
  const values = (shown: ReturnType<typeof shownPatientWorkFilters>) => shown.map((filter) => filter.value)

  assert.deepEqual(shownPatientWorkFilters(counts, "all", false), [
    { value: "all", label: "All", count: 9 },
    { value: "followUp", label: "Tasks", count: 4 },
    { value: "callBack", label: "Call back", count: 3 },
    { value: "texts", label: "Texts", count: 2 },
    { value: "unowned", label: "No owner", count: 0 },
  ])
  assert.deepEqual(values(shownPatientWorkFilters(counts, "all", true)), ["all", "followUp", "callBack", "texts", "appointments", "unowned"])
  assert.deepEqual(values(shownPatientWorkFilters({ ...counts, appointments: 1 }, "all", false)), ["all", "followUp", "callBack", "texts", "appointments", "unowned"])
  assert.deepEqual(values(shownPatientWorkFilters({ ...counts, unowned: 4 }, "all", false)), ["all", "followUp", "callBack", "texts", "unowned"])
  assert.deepEqual(values(shownPatientWorkFilters(counts, "appointments", false)), ["all", "followUp", "callBack", "texts", "appointments", "unowned"], "the active filter stays reachable at zero")
  assert.deepEqual(values(shownPatientWorkFilters({ ...counts, followUp: 0 }, "all", false)), ["all", "followUp", "callBack", "texts", "unowned"], "Tasks is offered at zero")
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

test("each list position remembers when the patient shown there last changed", () => {
  const earlier = 4_000
  const now = 9_000
  const _ = undefined
  for (const [label, before, times, after, want] of [
    ["a refetch of the same rows", ["a", "b", "c"], [_, earlier, _], ["a", "b", "c"], [_, earlier, _]],
    ["a patient arrives above: every position below changes", ["a", "b"], [_, _], ["n", "a", "b"], [now, now, _]],
    ["a patient arrives between two rows: the row above is untouched", ["a", "b", "c"], [_, _, _], ["a", "n", "b", "c"], [_, now, now, _]],
    ["a patient arrives below every row", ["a", "b"], [earlier, _], ["a", "b", "n"], [earlier, _, _]],
    ["one patient leaves and another takes its place", ["a", "b", "c"], [_, _, _], ["a", "n", "c"], [_, now, _]],
    ["Show more appends rows", ["a", "b"], [_, earlier], ["a", "b", "c", "d"], [_, earlier, _, _]],
    ["new work swaps two patients", ["a", "b", "c"], [earlier, _, _], ["a", "c", "b"], [earlier, now, now]],
    ["a patient is closed: the rows below move up, the rows above stay", ["a", "b", "c", "d"], [_, _, _, _], ["a", "c", "d"], [_, now, now]],
    ["the last row leaves", ["a", "b"], [earlier, earlier], ["a"], [earlier]],
    ["a chip, scope or search empties the list", ["a", "b"], [earlier, earlier], [], []],
    ["the list they asked for loads", [], [], ["b", "a"], [_, _]],
  ] as const) {
    assert.deepEqual(rowChangedAtAfter(times, before, after, now), want, label)
  }
})

test("a pointer click on a row is refused until the guard time has passed since its position last changed", () => {
  const changedAt = 10_000
  for (const [label, at, now, want] of [
    ["the position never changed", undefined, changedAt, true],
    ["the click lands as the position changes", changedAt, changedAt, false],
    ["the click was queued before the change rendered", changedAt, changedAt - 5, false],
    ["just inside the guard", changedAt, changedAt + rowClickGuardMilliseconds - 1, false],
    ["the guard has passed", changedAt, changedAt + rowClickGuardMilliseconds, true],
  ] as const) {
    assert.equal(rowClickAllowed(at, now), want, label)
  }
})
