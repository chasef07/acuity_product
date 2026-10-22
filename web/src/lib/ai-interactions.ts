import type {
  AiAppointmentOutcome,
  AiInteractionCallStatus,
} from "@/lib/api/generated/types.gen"
import { appointmentOutcomeTitles } from "./vocabulary.ts"

export function aiCallTimelinePresentation(
  outcome: AiAppointmentOutcome,
  status: AiInteractionCallStatus,
) {
  if (outcome !== "INDETERMINATE") {
    return {
      title: appointmentOutcomeTitles[outcome],
      detail: aiCallTimelineDetail(status),
    }
  }

  switch (status) {
    case "COMPLETED":
      return { title: "AI call", detail: "" }
    case "ESCALATED":
      return { title: "Transfer requested", detail: "AI call" }
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
      return "AI call · Transfer requested"
    case "FAILED":
      return "AI call failed"
    default:
      return "AI call in progress"
  }
}
