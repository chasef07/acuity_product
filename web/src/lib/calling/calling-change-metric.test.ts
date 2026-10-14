import assert from "node:assert/strict"
import test from "node:test"

import type { CallingState } from "../api/generated/types.gen.ts"
import { createCallingChangeMetric } from "./calling-change-metric.ts"

const covered = { hidden: false, covered: true }

test("a change the poll finds with no preceding hint is reported as a missed hint", () => {
  const metric = createCallingChangeMetric()
  metric.observe(metric.beginRead(0, covered), idle())

  const poll = metric.beginRead(10_000, { hidden: true, covered: true })
  metric.observe(poll, ringing())

  assert.equal(
    metric.beginRead(20_000, covered).report,
    "discovery=missed;hints=covered;tab=hidden;offer=ringing",
  )
  assert.equal(metric.beginRead(30_000, covered).report, undefined)
})

test("a hint that trails the poll that found the change is late, not missed, and credits only that change", () => {
  const metric = createCallingChangeMetric()
  metric.observe(metric.beginRead(0, covered), idle())

  metric.observe(metric.beginRead(10_000, covered), ringing())
  metric.signal("hint", 10_150)
  const next = metric.beginRead(10_250, covered)
  metric.observe(next, idle())
  assert.equal(next.report, "discovery=late_hint;hints=covered;tab=visible;offer=ringing")
  assert.equal(
    metric.beginRead(12_251, covered).report,
    "discovery=missed;hints=covered;tab=visible;offer=ringing",
  )

  const inFlight = metric.beginRead(20_000, covered)
  metric.signal("hint", 20_050)
  metric.observe(inFlight, ringing())
  assert.equal(
    metric.beginRead(20_500, covered).report,
    "discovery=late_hint;hints=covered;tab=visible;offer=ringing",
  )
})

test("a hint after the grace window does not rescue a missed change", () => {
  const metric = createCallingChangeMetric()
  metric.observe(metric.beginRead(0, covered), idle())

  metric.observe(metric.beginRead(10_000, covered), ringing())
  metric.signal("hint", 12_001)

  assert.equal(
    metric.beginRead(12_001, covered).report,
    "discovery=missed;hints=covered;tab=visible;offer=ringing",
  )

  const slow = metric.beginRead(20_000, covered)
  metric.signal("hint", 24_900)
  metric.observe(slow, idle())

  assert.equal(
    metric.beginRead(25_000, covered).report,
    "discovery=missed;hints=covered;tab=visible;offer=ringing",
  )
})

test("own lease heartbeats and uncovered streams are reported as they are", () => {
  const metric = createCallingChangeMetric()
  metric.observe(metric.beginRead(0, covered), idle())

  metric.observe(
    metric.beginRead(4_000, covered),
    idle({ leaseExpiresAt: "2026-08-27T00:02:00Z", available: true }),
  )
  assert.equal(metric.beginRead(10_000, covered).report, undefined)

  metric.observe(
    metric.beginRead(14_000, { hidden: false, covered: false }),
    idle({ activeCallId: "call-1" }),
  )
  assert.equal(
    metric.beginRead(20_000, covered).report,
    "discovery=missed;hints=uncovered;tab=visible;offer=none",
  )
})

test("a read that fails leaves its hint and stream gap for the retry", () => {
  const metric = createCallingChangeMetric()
  metric.observe(metric.beginRead(0, covered), idle())

  metric.signal("disconnected", 3_000)
  metric.signal("connected", 4_000)
  metric.signal("hint", 5_000)
  metric.readFailed(metric.beginRead(5_000, covered))
  metric.observe(metric.beginRead(13_000, covered), ringing())
  const afterRetry = metric.beginRead(23_000, covered)
  metric.observe(afterRetry, idle())

  assert.equal(
    afterRetry.report,
    "discovery=hint;hints=uncovered;tab=visible;offer=ringing",
  )
  assert.equal(
    metric.beginRead(33_000, covered).report,
    "discovery=missed;hints=covered;tab=visible;offer=ringing",
  )
})

test("reset forgets the previous state and pending changes", () => {
  const metric = createCallingChangeMetric()
  metric.observe(metric.beginRead(0, covered), idle())
  metric.observe(metric.beginRead(10_000, covered), ringing())

  metric.reset()
  metric.observe(metric.beginRead(20_000, covered), idle())

  assert.equal(metric.beginRead(30_000, covered).report, undefined)
})

function idle(softphone: Partial<CallingState["softphone"]> = {}): CallingState {
  return {
    softphone: {
      sessionId: "session-1",
      leaseExpiresAt: "2026-08-27T00:01:00Z",
      owner: true,
      available: false,
      activeCallId: "",
      pendingOutcomeCallId: "",
      ...softphone,
    },
    ringing: [],
    staffTransfers: [],
    callingPracticeIds: ["practice-1"],
  }
}

function ringing(): CallingState {
  return {
    ...idle(),
    ringing: [{
      callId: "call-1",
      callLegId: "leg-1",
      mediaToken: "media-1",
      practiceId: "practice-1",
      locationId: "location-1",
      locationName: "Main",
      displayName: "Caller",
      phone: "",
      transferReason: "",
      state: "RINGING",
      version: 1,
      createdAt: "2026-08-27T00:00:00Z",
      deadline: "2026-08-27T00:00:20Z",
      offerKind: "INBOUND_OFFER",
      staffTransferId: "",
      originatorEmail: "",
      handoffNote: "",
    }],
  }
}
