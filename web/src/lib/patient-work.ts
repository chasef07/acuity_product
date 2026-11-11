import type {
  PatientWorkCounts,
  PatientWorkFilter,
  PatientWorkRow,
  PatientWorkScope,
  PatientWorkTotals,
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
  callBack: () => true,
  texts: () => true,
  appointments: (counts, appointmentsOffered) => appointmentsOffered || counts.appointments > 0,
  unowned: (counts) => counts.unowned > 0,
}

export function shownPatientWorkFilters(
  counts: PatientWorkCounts,
  active: PatientWorkFilter,
  appointmentsOffered: boolean,
) {
  return labelOptions(patientWorkFilterLabels)
    .filter(({ value }) => value === active || filterShown[value](counts, appointmentsOffered))
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

export function patientRowReason(
  row: Pick<PatientWorkRow, "top" | "voicemailDurationSeconds">,
): { voicemail: boolean; text: string } {
  switch (row.top.origin) {
    case "VOICEMAIL_RECOVERY":
      return {
        voicemail: true,
        text: row.voicemailDurationSeconds
          ? `Voicemail · ${formatClock(row.voicemailDurationSeconds)}`
          : "Voicemail",
      }
    case "MISSED_CALL_RECOVERY":
      return { voicemail: false, text: "Missed call, no voicemail" }
    case "INBOUND_MESSAGE_REVIEW":
      return { voicemail: false, text: row.top.preview || row.top.title }
    default:
      return { voicemail: false, text: row.top.title }
  }
}

export function patientWorkFooter(totals: PatientWorkTotals, scope: PatientWorkScope) {
  const open = `${totals.openTasks} open for ${totals.patients} ${totals.patients === 1 ? "patient" : "patients"}`
  return scope === "mine" && totals.patientsOutsideScope > 0
    ? `${open} · ${totals.patientsOutsideScope} more under Everyone`
    : open
}
