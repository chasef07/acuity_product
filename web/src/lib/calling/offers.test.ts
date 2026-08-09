import assert from "node:assert/strict"
import test from "node:test"

import type { RingingCallLeg } from "../api/generated/types.gen.ts"
import { activeRingingOffers, offerSecondsRemaining } from "./offers.ts"

const offer = {
  callId: "call-id",
  callLegId: "call-leg-id",
  mediaToken: "a".repeat(43),
  practiceId: "practice-id",
  locationId: "location-id",
  locationName: "Main office",
  displayName: "Incoming caller",
  phone: "+15555550100",
  transferReason: "Needs help",
  state: "RINGING",
  version: 1,
  createdAt: "2026-08-09T12:00:00Z",
  deadline: "2026-08-09T12:00:20Z",
} satisfies RingingCallLeg

test("incoming offer countdown uses the server deadline", () => {
  assert.equal(
    offerSecondsRemaining(offer.deadline, Date.parse("2026-08-09T12:00:00.001Z")),
    20,
  )
  assert.equal(
    offerSecondsRemaining(offer.deadline, Date.parse("2026-08-09T12:00:19.001Z")),
    1,
  )
})

test("incoming offer is hidden at its authoritative deadline", () => {
  assert.deepEqual(
    activeRingingOffers([offer], Date.parse("2026-08-09T12:00:19.999Z")),
    [offer],
  )
  assert.deepEqual(
    activeRingingOffers([offer], Date.parse("2026-08-09T12:00:20Z")),
    [],
  )
})
