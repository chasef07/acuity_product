import assert from "node:assert/strict"
import test from "node:test"

import {
  formatClock,
  formatDuration,
  formatDurationBetween,
  formatPhone,
  formatRelativeTime,
  taskGroupLabel,
  taskGroups,
} from "./vocabulary.ts"

test("formats durations at the edges where the copies disagreed", () => {
  assert.equal(formatDuration(0), "0s")
  assert.equal(formatDuration(59), "59s")
  assert.equal(formatDuration(59.6), "1m 0s")
  assert.equal(formatDuration(60), "1m 0s")
  assert.equal(formatDuration(65), "1m 5s")
  assert.equal(formatDuration(3_599), "59m 59s")
  assert.equal(formatDuration(3_600), "1h 0m 0s")
  assert.equal(formatDuration(3_725), "1h 2m 5s")
  assert.equal(formatDuration(-4), "0s")
  assert.equal(formatDuration(null), "—")
  assert.equal(formatDuration(undefined), "—")
  assert.equal(formatDuration(Number.NaN), "—")
})

test("formats a duration between timestamps", () => {
  assert.equal(formatDurationBetween("2026-10-01T12:00:00Z"), "In progress")
  assert.equal(formatDurationBetween("2026-10-01T12:00:00Z", "2026-10-01T12:00:59Z"), "59s")
  assert.equal(formatDurationBetween("2026-10-01T12:00:00Z", "2026-10-01T13:00:00Z"), "1h 0m 0s")
  assert.equal(formatDurationBetween("not a time", "2026-10-01T12:00:00Z"), "—")
})

test("formats an elapsed clock", () => {
  assert.equal(formatClock(0), "0:00")
  assert.equal(formatClock(59), "0:59")
  assert.equal(formatClock(59.9), "0:59")
  assert.equal(formatClock(60), "1:00")
  assert.equal(formatClock(3_599), "59:59")
  assert.equal(formatClock(3_600), "1:00:00")
  assert.equal(formatClock(3_725), "1:02:05")
  assert.equal(formatClock(-1), "0:00")
  assert.equal(formatClock(Number.NaN), "—")
  assert.equal(formatClock(undefined), "—")
})

test("formats US phone numbers from any common spelling", () => {
  for (const input of ["+17277092035", "7277092035", "1-727-709-2035", "(727) 709-2035"]) {
    assert.equal(formatPhone(input), "(727) 709-2035")
  }
  for (const input of ["", "727709203", "+447277092035", "+6591234567", "Incoming caller"]) {
    assert.equal(formatPhone(input), input)
  }
})

test("formats relative time", () => {
  const now = Date.parse("2026-10-02T12:00:00Z")
  assert.equal(formatRelativeTime("2026-10-02T11:59:31Z", now), "now")
  assert.equal(formatRelativeTime("2026-10-02T12:00:30Z", now), "now")
  assert.equal(formatRelativeTime("2026-10-02T11:59:00Z", now), "1m")
  assert.equal(formatRelativeTime("2026-10-02T11:00:00Z", now), "1h")
  assert.equal(formatRelativeTime("2026-10-01T12:00:00Z", now), "1d")
})

test("lists active task groups and labels historical billing", () => {
  assert.deepEqual(taskGroups.map((group) => group.value), [
    "appointments", "documentation", "medication", "pre_op", "post_op",
    "insurance", "optical", "referrals", "other",
  ])
  assert.equal(taskGroupLabel("pre_op"), "Pre-op")
  assert.equal(taskGroupLabel("billing"), "Billing (historical)")
  assert.equal(taskGroupLabel(undefined), "Uncategorized")
})
