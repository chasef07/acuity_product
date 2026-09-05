import type {
  AiAppointmentOutcome,
  AiInteractionCallStatus,
} from "@/lib/api/generated/types.gen"

const appointmentPresentations: Record<
  AiAppointmentOutcome,
  {
    label: string
    title: string
  }
> = {
  BOOKING: {
    label: "Booking",
    title: "Appointment booked",
  },
  CANCELLATION: {
    label: "Cancellation",
    title: "Appointment cancelled",
  },
  RESCHEDULE: {
    label: "Reschedule",
    title: "Appointment rescheduled",
  },
  PARTIAL: {
    label: "Partially completed",
    title: "Appointment change needs review",
  },
  INDETERMINATE: {
    label: "No appointment actions",
    title: "AI call",
  },
}

export function aiCallCompletionLabel(status: AiInteractionCallStatus) {
  switch (status) {
    case "COMPLETED":
      return "Call completed"
    case "ESCALATED":
      return "Transferred to staff"
    case "FAILED":
      return "AI call failed"
    default:
      return "AI call in progress"
  }
}

export function aiCallTimelinePresentation(
  outcome: AiAppointmentOutcome,
  status: AiInteractionCallStatus,
) {
  if (outcome !== "INDETERMINATE") {
    return {
      title: appointmentOutcomeTitle(outcome),
      detail: aiCallTimelineDetail(status),
    }
  }

  switch (status) {
    case "COMPLETED":
      return { title: "AI call", detail: "" }
    case "ESCALATED":
      return { title: "Transferred to staff", detail: "AI call" }
    case "FAILED":
      return { title: "AI call failed", detail: "" }
    default:
      return { title: "AI call in progress", detail: "" }
  }
}

function aiCallTimelineDetail(status: AiInteractionCallStatus) {
  switch (status) {
    case "COMPLETED":
      return "AI call"
    case "ESCALATED":
      return "AI call transferred to staff"
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
