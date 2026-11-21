import type {
  PatientWorkCounts,
  PatientWorkRow,
  PatientWorkScope,
  PatientWorkTotals,
  Task,
} from "./api/generated/types.gen.ts"
import {
  formatClock,
  formatPhone,
  labelOptions,
  patientWorkFilterLabels,
  type RailFilter,
} from "./vocabulary.ts"

export function patientWorkFilterShown(
  filter: RailFilter,
  counts: PatientWorkCounts,
  appointmentsOffered: boolean,
) {
  return filter !== "appointments" || appointmentsOffered || counts.appointments > 0
}

export function shownPatientWorkFilters(
  counts: PatientWorkCounts,
  active: RailFilter,
  appointmentsOffered: boolean,
) {
  return labelOptions(patientWorkFilterLabels)
    .filter(({ value }) => value === active || patientWorkFilterShown(value, counts, appointmentsOffered))
    .map((option) => ({ ...option, count: counts[option.value] }))
}

export function offersAppointmentChecks(
  locations: Array<{ id: string; name: string }>,
  locationScopeID: string,
) {
  return locations.some(
    (location) =>
      location.name.trim().toLowerCase() === "spring hill" &&
      (locationScopeID === location.id || locations.length === 1),
  )
}

export function patientName(name: string | undefined, phone: string) {
  return name || formatPhone(phone)
}

export function phoneNames(names: string[], callName = "") {
  const known = callName.trim().toLowerCase()
  return !known || names.some((name) => name.toLowerCase() === known)
    ? names
    : [...names, callName.trim()]
}

export function openTaskReason(
  task: Pick<Task, "origin" | "title" | "preview" | "voicemailDurationSeconds">,
): string {
  switch (task.origin) {
    case "VOICEMAIL_RECOVERY":
      return task.voicemailDurationSeconds
        ? `Voicemail · ${formatClock(task.voicemailDurationSeconds)}`
        : "Voicemail"
    case "MISSED_CALL_RECOVERY":
      return "Missed call, no voicemail"
    case "INBOUND_MESSAGE_REVIEW":
      return task.preview || task.title
    default:
      return task.title
  }
}

export function patientWorkFooter(totals: PatientWorkTotals, scope: PatientWorkScope) {
  const verb = scope === "mine" ? "you" : "attention"
  const patients = totals.patients === 1
    ? `1 patient needs ${verb}`
    : `${totals.patients} patients need ${verb}`
  return scope === "mine" && totals.patientsOutsideScope > 0
    ? `${patients} · ${totals.patientsOutsideScope} more under Everyone`
    : patients
}

export function nextAfterClose({
  before,
  after,
  phone,
  closedTaskIDs,
}: {
  before: readonly PatientWorkRow[]
  after: readonly PatientWorkRow[]
  phone: string
  closedTaskIDs: readonly string[]
}): Task | undefined {
  const open = after.filter((row) => !closedTaskIDs.includes(row.top.id))
  const samePatient = open.find((row) => row.phone === phone)
  if (samePatient) return samePatient.top
  const index = before.findIndex((row) => row.phone === phone)
  const neighbours = index < 0
    ? []
    : [...before.slice(index + 1), ...before.slice(0, index).reverse()]
  for (const neighbour of neighbours) {
    const listed = open.find((row) => row.phone === neighbour.phone)
    if (listed) return listed.top
  }
  return open[0]?.top
}

export const rowClickGuardMilliseconds = 1200

export function rowChangedAtAfter(
  changedAt: readonly (number | undefined)[],
  before: readonly string[],
  after: readonly string[],
  now: number,
) {
  const onlyAppended = before.every((phone, index) => after[index] === phone)
  return after.map((phone, index) => {
    if (index >= before.length) return onlyAppended ? undefined : now
    return before[index] === phone ? changedAt[index] : now
  })
}

export function belowListChangedAtAfter(
  changedAt: number | undefined,
  before: readonly string[],
  after: readonly string[],
  now: number,
) {
  return before.length === after.length ? changedAt : now
}

export function rowClickAllowed(changedAt: number | undefined, now: number) {
  return changedAt === undefined || now - changedAt >= rowClickGuardMilliseconds
}
