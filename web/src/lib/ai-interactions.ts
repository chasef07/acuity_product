import type {
  AiAppointmentOutcome,
  AiInteractionCallStatus,
} from "@/lib/api/generated/types.gen"
import { appointmentOutcomeTitles, callLineLabels } from "./vocabulary.ts"

export function aiCallTitle(
  outcome: AiAppointmentOutcome,
  status: AiInteractionCallStatus,
) {
  if (outcome !== "INDETERMINATE") return appointmentOutcomeTitles[outcome]

  switch (status) {
    case "COMPLETED":
      return appointmentOutcomeTitles.INDETERMINATE
    case "ESCALATED":
      return callLineLabels.transferRequested
    case "FAILED":
      return callLineLabels.aiFailed
    default:
      return callLineLabels.aiInProgress
  }
}
