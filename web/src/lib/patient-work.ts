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
  unowned: (counts) => counts.unowned > 0,
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

type PatientListHoldReason = "pointer" | "moving" | "keyboard" | "touch"

export type PatientListHold = {
  rows: readonly PatientWorkRow[]
  incoming: readonly PatientWorkRow[]
  loading: boolean
  reasons: readonly PatientListHoldReason[]
}

export type PatientListHoldEvent =
  | { type: "incoming"; rows: readonly PatientWorkRow[]; loading: boolean }
  | { type: "hold"; reason: PatientListHoldReason }
  | { type: "release"; reason: PatientListHoldReason }
  | { type: "acted" }

export function patientListShowing(
  rows: readonly PatientWorkRow[],
  loading: boolean,
): PatientListHold {
  return { rows, incoming: rows, loading, reasons: [] }
}

export function patientListHold(
  state: PatientListHold,
  event: PatientListHoldEvent,
): PatientListHold {
  switch (event.type) {
    case "incoming": {
      const requested = state.loading || event.loading
      const latest = new Map(event.rows.map((row) => [row.phone, row]))
      return {
        ...state,
        incoming: event.rows,
        loading: event.loading,
        rows: state.reasons.length > 0 && !requested
          ? state.rows.map((row) => latest.get(row.phone) ?? row)
          : event.rows,
      }
    }
    case "hold":
      return state.reasons.includes(event.reason)
        ? state
        : { ...state, reasons: [...state.reasons, event.reason] }
    case "release": {
      const reasons = state.reasons.filter((reason) => reason !== event.reason)
      return { ...state, reasons, rows: reasons.length === 0 ? state.incoming : state.rows }
    }
    case "acted":
      return { ...state, rows: state.incoming }
  }
}

export function patientListHeld(state: PatientListHold) {
  return state.rows.length !== state.incoming.length ||
    state.rows.some((row, index) => row.phone !== state.incoming[index].phone)
}
