import type {
  AiAppointmentOutcome,
  AiInteractionCallStatus,
} from "@/lib/api/generated/types.gen"

export type AppointmentFolder =
  | "bookings"
  | "cancellations"
  | "reschedules"

export type AppointmentFacts = {
  appointmentDate?: string
  appointmentId?: string
  appointmentTime?: string
  appointmentTypeName?: string
  careLane?: string
  locationName?: string
  patientName?: string
  providerName?: string
  startDatetime?: string
}

export type AIAppointmentDetails = {
  primary: AppointmentFacts
  previous?: AppointmentFacts
}

const appointmentPresentations: Record<
  AiAppointmentOutcome,
  {
    folder?: AppointmentFolder
    label: string
    title: string
  }
> = {
  BOOKING: {
    folder: "bookings",
    label: "Booking",
    title: "Appointment booked",
  },
  CANCELLATION: {
    folder: "cancellations",
    label: "Cancellation",
    title: "Appointment cancelled",
  },
  RESCHEDULE: {
    folder: "reschedules",
    label: "Reschedule",
    title: "Appointment rescheduled",
  },
  PARTIAL: {
    label: "Partially completed",
    title: "Appointment change needs review",
  },
  INDETERMINATE: {
    label: "No verified appointment outcome",
    title: "AI call needs review",
  },
}

export function appointmentFolder(
  outcome: AiAppointmentOutcome,
): AppointmentFolder | undefined {
  return appointmentPresentations[outcome].folder
}

export function aiCallCompletionLabel(status: AiInteractionCallStatus) {
  switch (status) {
    case "COMPLETED":
      return "Completed by AI"
    case "ESCALATED":
      return "Transferred to staff"
    case "FAILED":
      return "AI call failed"
    default:
      return "AI call in progress"
  }
}

export function appointmentOutcomeLabel(outcome: AiAppointmentOutcome) {
  return appointmentPresentations[outcome].label
}

export function appointmentOutcomeTitle(outcome: AiAppointmentOutcome) {
  return appointmentPresentations[outcome].title
}

export function aiAppointmentDetails(input: {
  appointmentOutcome: AiAppointmentOutcome
  bookingResult?: Record<string, unknown>
  cancellationResult?: Record<string, unknown>
  closeoutPayload?: Record<string, unknown>
  newAppointmentId?: string
  oldAppointmentId?: string
}): AIAppointmentDetails {
  const action = latestAppointmentAction(
    input.closeoutPayload,
    input.appointmentOutcome,
  )
  const appointment = recordValue(action?.appointment)
  const cancelledAppointment = recordValue(action?.cancelledAppointment)
  const bookingAppointment = recordValue(input.bookingResult?.appointment)
  const cancellationAppointment = recordValue(
    input.cancellationResult?.appointment,
  )

  if (input.appointmentOutcome === "CANCELLATION") {
    return {
      primary: appointmentFacts(
        [
          cancelledAppointment,
          appointment,
          cancellationAppointment,
          input.cancellationResult,
        ],
        input.oldAppointmentId,
      ),
    }
  }

  const primary = appointmentFacts(
    [appointment, bookingAppointment, input.bookingResult],
    input.newAppointmentId,
  )
  if (input.appointmentOutcome !== "RESCHEDULE") return { primary }

  return {
    primary,
    previous: appointmentFacts(
      [cancelledAppointment, cancellationAppointment, input.cancellationResult],
      input.oldAppointmentId,
    ),
  }
}

function latestAppointmentAction(
  closeoutPayload: Record<string, unknown> | undefined,
  outcome: AiAppointmentOutcome,
) {
  const actions = Array.isArray(closeoutPayload?.appointmentActions)
    ? closeoutPayload.appointmentActions
    : []
  const expectedAction = {
    BOOKING: "booked",
    CANCELLATION: "cancelled",
    RESCHEDULE: "rescheduled",
    PARTIAL: "",
    INDETERMINATE: "",
  }[outcome]
  const records = actions
    .map(recordValue)
    .filter((action): action is Record<string, unknown> => Boolean(action))
  if (!expectedAction) return records.at(-1)
  return [...records]
    .reverse()
    .find((action) => stringValue(action.action)?.toLowerCase() === expectedAction)
}

function appointmentFacts(
  sources: Array<Record<string, unknown> | undefined>,
  appointmentId: string | undefined,
): AppointmentFacts {
  const values = sources.filter(
    (source): source is Record<string, unknown> => Boolean(source),
  )
  return compactRecord({
    appointmentDate: firstValue(values, ["appointmentDate"]),
    appointmentId:
      appointmentId ??
      firstValue(values, [
        "appointmentId",
        "cancelledAppointmentId",
      ]),
    appointmentTime: firstValue(values, ["appointmentTime"]),
    appointmentTypeName: firstValue(values, ["appointmentTypeName"]),
    careLane: firstValue(values, ["careLane"]),
    locationName: firstValue(values, ["locationName"]),
    patientName: firstValue(values, ["patientName"]),
    providerName: firstValue(values, ["providerName"]),
    startDatetime: firstValue(values, ["startDatetime"]),
  })
}

function firstValue(
  records: Record<string, unknown>[],
  keys: string[],
): string | undefined {
  for (const record of records) {
    for (const key of keys) {
      const value = stringValue(record[key])
      if (value) return value
    }
  }
  return undefined
}

function compactRecord<T extends Record<string, string | undefined>>(value: T) {
  return Object.fromEntries(
    Object.entries(value).filter(([, item]) => Boolean(item)),
  ) as T
}

function recordValue(value: unknown): Record<string, unknown> | undefined {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return undefined
  }
  return value as Record<string, unknown>
}

function stringValue(value: unknown): string | undefined {
  if (typeof value === "number" && Number.isFinite(value)) return String(value)
  return typeof value === "string" && value.trim() ? value.trim() : undefined
}
