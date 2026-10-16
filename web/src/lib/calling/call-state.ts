import type { CallingCall } from "../api/generated/types.gen.ts"

const settledCallStates: Record<CallingCall["state"], boolean> = {
  PREPARING: false,
  RINGING: false,
  CONNECTING: false,
  CONNECTED: false,
  VOICEMAIL_GREETING: false,
  VOICEMAIL_RECORDING: false,
  UNANSWERED: true,
  VOICEMAIL: true,
  MISSED: true,
  NEEDS_DISPOSITION: true,
  RESOLVED: true,
  FOLLOW_UP_REQUIRED: true,
}

export function isSettledCallState(state: CallingCall["state"]): boolean {
  return settledCallStates[state]
}
