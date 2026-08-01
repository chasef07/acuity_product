import type { CallingCall } from "@/lib/api/generated/types.gen"

export type MediaConfirmationDecision = "retry" | "stop"

export function mediaConfirmationDecision(
  call:
    | Pick<CallingCall, "expectedMediaToken" | "mediaReady" | "state">
    | undefined,
  status: number | undefined,
  mediaToken: string,
): MediaConfirmationDecision {
  if (!call) {
    return status === 400 || status === 401 || status === 403 || status === 404
      ? "stop"
      : "retry"
  }
  if (call.expectedMediaToken !== mediaToken) {
    return "stop"
  }
  switch (call.state) {
    case "PREPARING":
      return call.mediaReady ? "stop" : "retry"
    case "RECONCILING":
      return call.mediaReady ? "stop" : "retry"
    default:
      return "stop"
  }
}
