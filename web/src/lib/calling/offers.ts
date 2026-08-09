import type { RingingCallLeg } from "../api/generated/types.gen.ts"

export function offerSecondsRemaining(deadline: string, now: number) {
  return Math.max(0, Math.ceil((new Date(deadline).getTime() - now) / 1000))
}

export function activeRingingOffers(offers: RingingCallLeg[], now: number) {
  return offers.filter((offer) => new Date(offer.deadline).getTime() > now)
}
