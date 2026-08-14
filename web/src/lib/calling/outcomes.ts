import type { CallingCall } from "@/lib/api/generated/types.gen"

type HangupFailureInput = {
  status?: number
  code?: string
}

export type HangupFailure =
  | "authentication"
  | "conflict"
  | "request"
  | "retry"

export function callIsSettled(state: CallingCall["state"]) {
  switch (state) {
    case "NEEDS_DISPOSITION":
    case "UNANSWERED":
    case "MISSED":
    case "VOICEMAIL":
    case "RESOLVED":
    case "FOLLOW_UP_REQUIRED":
      return true
    default:
      return false
  }
}

export function hangupFailure(
  failure: HangupFailureInput | undefined,
): HangupFailure {
  if (!failure?.status || failure.status === 503) return "retry"
  if (failure.status === 401 || failure.status === 403) {
    return "authentication"
  }
  if (failure.status === 409 && failure.code === "CALL_CONFLICT") {
    return "conflict"
  }
  return "request"
}

export function providerOutcomeLabel(value: string) {
  switch (value) {
    case "COMPLETED":
      return "Completed"
    case "NO_ANSWER":
      return "No answer"
    case "BUSY":
      return "Busy"
    case "DECLINED":
      return "Declined"
    case "STATUS_UNKNOWN":
      return "Status unknown"
    case "MEDIA_READINESS_FAILED":
    case "MEDIA_FAILURE":
      return "Media failure"
    case "FAILED":
      return "Failed"
    default:
      return value.toLowerCase().replaceAll("_", " ")
  }
}

export function dispositionWindowIsOpen(
  deadline: string | undefined,
  now: number,
) {
  return !deadline || new Date(deadline).getTime() > now
}
