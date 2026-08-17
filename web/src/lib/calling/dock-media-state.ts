import type { IncomingMediaLeg, MediaState } from "./media-adapter.ts"
import type { CallingState } from "../api/generated/types.gen.ts"

type MediaIdentity = Pick<IncomingMediaLeg, "providerLegID" | "mediaToken">

export type IncomingMediaRoute =
  | "RECOVER_ATTACHED"
  | "REJECT"
  | "CONFIRM_OUTBOUND"
  | "QUEUE_INBOUND"

type ConfirmationAttempt<T> = {
  data?: T
  status?: number
}

type CurrentCallingState = Pick<
  CallingState,
  "ringing" | "bridged" | "voicemail" | "disposition"
>

type AnsweredCallLeg = {
  callId: string
  callLegId: string
}

export function answeredCallLegStatus(
  state: CurrentCallingState,
  expected: AnsweredCallLeg,
) {
  if (
    state.bridged?.callId === expected.callId &&
    state.bridged.callLegId === expected.callLegId
  ) {
    return "BRIDGED" as const
  }
  if (
    state.disposition?.callId === expected.callId &&
    state.disposition.callLegId === expected.callLegId
  ) {
    return "ENDED" as const
  }
  if (
    state.ringing?.some(
      (leg) =>
        leg.callId === expected.callId && leg.callLegId === expected.callLegId,
    )
  ) {
    return "PENDING" as const
  }
  return "LOST" as const
}

export function currentCallingStateCallID(state: CurrentCallingState) {
  return state.bridged?.callId ?? state.disposition?.callId
}

export async function confirmOutboundMediaWithRetry<T>(
  confirm: () => Promise<ConfirmationAttempt<T>>,
  wait: () => Promise<void> = () =>
    new Promise((resolve) => window.setTimeout(resolve, 250)),
  attempts = 20,
) {
  for (let attempt = 0; attempt < attempts; attempt += 1) {
    const result: ConfirmationAttempt<T> = await confirm().catch(() => ({}))
    if (result.data) return result.data
    if (result.status && result.status !== 409) return
    if (attempt + 1 < attempts) await wait()
  }
}

export function microphoneFailureMessage(error: unknown) {
  const name = error instanceof DOMException ? error.name : ""
  if (name === "NotAllowedError" || name === "SecurityError") {
    return "Allow microphone access in your browser to use calling."
  }
  if (name === "NotFoundError" || name === "DevicesNotFoundError") {
    return "No microphone was found. Connect one and try again."
  }
  if (name === "NotReadableError" || name === "AbortError") {
    return "Your microphone is busy or unavailable. Close other audio apps and try again."
  }
  return "Browser audio could not be started. Check your microphone and try again."
}

export function mediaAttachmentAfterState<T extends MediaIdentity>(
  state: MediaState,
  attached: T | null,
) {
  return state === "unavailable" ? null : attached
}

export function routeIncomingMedia(
  attached: MediaIdentity | null,
  incoming: MediaIdentity & Pick<IncomingMediaLeg, "recovery">,
  owner: boolean,
  mediaState: MediaState,
  expectedCallID: string,
): IncomingMediaRoute {
  if (
    attached?.providerLegID === incoming.providerLegID &&
    attached.mediaToken === incoming.mediaToken &&
    incoming.recovery
  ) {
    return "RECOVER_ATTACHED"
  }
  if (attached || !owner || mediaState !== "ready") return "REJECT"
  if (expectedCallID) return "CONFIRM_OUTBOUND"
  return "QUEUE_INBOUND"
}
