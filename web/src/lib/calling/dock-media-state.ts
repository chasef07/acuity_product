import type { IncomingMediaLeg, MediaState } from "./media-adapter.ts"

type MediaIdentity = Pick<IncomingMediaLeg, "providerLegID" | "mediaToken">

export type IncomingMediaRoute =
  | "RECOVER_ATTACHED"
  | "REJECT"
  | "CONFIRM_OUTBOUND"
  | "QUEUE_INBOUND"

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
