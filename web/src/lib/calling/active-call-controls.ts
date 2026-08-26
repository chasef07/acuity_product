import type { CallingCall } from "../api/generated/types.gen.ts"
import { callIsSettled } from "./outcomes.ts"

const activeOutboundCallStates = new Set<CallingCall["state"]>([
  "PREPARING",
  "RINGING",
  "CONNECTING",
  "CONNECTED",
])

export function showActiveCallEndControl(
  call: Pick<CallingCall, "direction" | "state">,
  owner: boolean,
) {
  return (
    owner &&
    call.direction === "OUTBOUND" &&
    activeOutboundCallStates.has(call.state)
  )
}

export function endingCallIDAfterProjection(
  endingCallID: string,
  call: Pick<CallingCall, "id" | "state"> | undefined,
) {
  if (
    !endingCallID ||
    !call ||
    call.id !== endingCallID ||
    callIsSettled(call.state)
  ) {
    return ""
  }
  return endingCallID
}

export function activeCallEndPending(
  call: Pick<CallingCall, "endRequested" | "id" | "state">,
  endingCallID: string,
) {
  return (
    !callIsSettled(call.state) &&
    (call.endRequested || call.id === endingCallID)
  )
}
