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

export function appointmentFolder(
  outcome: AiAppointmentOutcome,
): AppointmentFolder | undefined {
  switch (outcome) {
    case "BOOKING":
      return "bookings"
    case "CANCELLATION":
      return "cancellations"
    case "RESCHEDULE":
      return "reschedules"
    default:
      return undefined
  }
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
  switch (outcome) {
    case "BOOKING":
      return "Booking"
    case "CANCELLATION":
      return "Cancellation"
    case "RESCHEDULE":
      return "Reschedule"
    case "PARTIAL":
      return "Partially completed"
    default:
      return "No verified appointment outcome"
  }
}

export function appointmentOutcomeTitle(outcome: AiAppointmentOutcome) {
  switch (outcome) {
    case "BOOKING":
      return "Appointment booked"
    case "CANCELLATION":
      return "Appointment cancelled"
    case "RESCHEDULE":
      return "Appointment rescheduled"
    case "PARTIAL":
      return "Appointment change needs review"
    default:
      return "AI call"
  }
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
