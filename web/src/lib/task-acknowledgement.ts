import type { TaskAutomaticAcknowledgement } from "./api/generated/types.gen.ts"

const safeFailureLabels: Record<string, string> = {
	SENDER_CONFIGURATION_INACTIVE: "Messaging configuration is inactive",
  SENDER_CONFIGURATION_UNAVAILABLE: "Messaging is not configured",
  TASK_ALREADY_RESOLVED: "Task was already resolved",
}

export function automaticAcknowledgementLabel(
  acknowledgement: TaskAutomaticAcknowledgement,
) {
  switch (acknowledgement.state) {
    case "PENDING":
		if (acknowledgement.safeFailureCode) {
			return `Waiting to retry · ${
				safeFailureLabels[acknowledgement.safeFailureCode] ??
				acknowledgement.safeFailureCode
			}`
		}
      return "Preparing automatic text"
    case "MESSAGE_QUEUED":
      return "Automatic text created · see Messages for delivery evidence"
    case "NOT_NEEDED":
      return `Not sent · ${
        safeFailureLabels[acknowledgement.safeFailureCode ?? ""] ??
        acknowledgement.safeFailureCode ??
        "Reason unavailable"
      }`
  }
}
