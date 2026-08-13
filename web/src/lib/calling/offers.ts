import type { RingingCallLeg } from "../api/generated/types.gen.ts"

export const incomingCallPendingMessage =
  "An incoming Call is pending. Wait for it to clear and try again."

export const outboundCallOccupiedMessage =
  "Another Call is pending or active."

export function offerSecondsRemaining(deadline: string, now: number) {
  return Math.max(0, Math.ceil((new Date(deadline).getTime() - now) / 1000))
}

export function activeRingingOffers(offers: RingingCallLeg[], now: number) {
  return offers.filter((offer) => new Date(offer.deadline).getTime() > now)
}

export function outboundCallBlockReason(
  offers: RingingCallLeg[],
  now: number,
) {
  return activeRingingOffers(offers, now).length > 0
    ? incomingCallPendingMessage
    : undefined
}
