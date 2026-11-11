import type {
  PatientWorkCounts,
  PatientWorkFilter,
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
} from "./vocabulary.ts"

const filterShown: Record<
  PatientWorkFilter,
  (counts: PatientWorkCounts, appointmentsOffered: boolean) => boolean
> = {
  all: () => true,
  followUp: () => true,
  callBack: () => true,
  texts: () => true,
  appointments: (counts, appointmentsOffered) => appointmentsOffered || counts.appointments > 0,
  unowned: () => true,
}

export function patientWorkFilterShown(
  filter: PatientWorkFilter,
  counts: PatientWorkCounts,
  appointmentsOffered: boolean,
) {
  return filterShown[filter](counts, appointmentsOffered)
}

export function shownPatientWorkFilters(
  counts: PatientWorkCounts,
  active: PatientWorkFilter,
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

export function patientRowName(row: Pick<PatientWorkRow, "displayName" | "phone">) {
  return row.displayName || formatPhone(row.phone)
}

export function openTaskReason(
  task: Pick<Task, "origin" | "title" | "preview" | "voicemailDurationSeconds">,
): { voicemail: boolean; text: string } {
  switch (task.origin) {
    case "VOICEMAIL_RECOVERY":
      return {
        voicemail: true,
        text: task.voicemailDurationSeconds
          ? `Voicemail · ${formatClock(task.voicemailDurationSeconds)}`
          : "Voicemail",
      }
    case "MISSED_CALL_RECOVERY":
      return { voicemail: false, text: "Missed call, no voicemail" }
    case "INBOUND_MESSAGE_REVIEW":
      return { voicemail: false, text: task.preview || task.title }
    default:
      return { voicemail: false, text: task.title }
  }
}

export function patientWorkFooter(totals: PatientWorkTotals, scope: PatientWorkScope) {
  const open = `${totals.openTasks} open for ${totals.patients} ${totals.patients === 1 ? "patient" : "patients"}`
  return scope === "mine" && totals.patientsOutsideScope > 0
    ? `${open} · ${totals.patientsOutsideScope} more under Everyone`
    : open
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
  return after.map((phone, index) => {
    if (index >= before.length) return undefined
    return before[index] === phone ? changedAt[index] : now
  })
}

export function rowClickAllowed(changedAt: number | undefined, now: number) {
  return changedAt === undefined || now - changedAt >= rowClickGuardMilliseconds
}
