import type {
  AiAppointmentOutcome,
  AiInteractionCallStatus,
} from "@/lib/api/generated/types.gen"

export type AppointmentFolder =
  | "bookings"
  | "cancellations"
  | "reschedules"

export type TranscriptTurn = {
  id: string
  speaker: "Caller" | "AI"
  text: string
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

export function transcriptTurns(
  transcript: Record<string, unknown> | undefined,
): TranscriptTurn[] {
  const history = recordValue(transcript?.chat_history ?? transcript?.chatHistory)
  const items = Array.isArray(history?.items) ? history.items : []
  return items.flatMap((item, index) => {
    const entry = recordValue(item)
    const role = stringValue(entry?.role)?.toLowerCase()
    if (role !== "user" && role !== "assistant") return []
    const text = contentText(entry?.content)
    if (!text) return []
    return [
      {
        id: stringValue(entry?.id) ?? `${role}-${index}`,
        speaker: role === "user" ? "Caller" : "AI",
        text,
      } satisfies TranscriptTurn,
    ]
  })
}

function contentText(value: unknown): string {
  if (typeof value === "string") return value.trim()
  if (!Array.isArray(value)) {
    const item = recordValue(value)
    return stringValue(item?.text)?.trim() ?? ""
  }
  return value
    .map((part) => {
      if (typeof part === "string") return part.trim()
      return stringValue(recordValue(part)?.text)?.trim() ?? ""
    })
    .filter(Boolean)
    .join("\n")
}

function recordValue(value: unknown): Record<string, unknown> | undefined {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return undefined
  }
  return value as Record<string, unknown>
}

function stringValue(value: unknown): string | undefined {
  return typeof value === "string" && value.trim() ? value : undefined
}
