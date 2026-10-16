import assert from "node:assert/strict"
import test from "node:test"
import { formatClockTime, formatDate, formatTimestamp } from "./format-time.ts"

const now = new Date(2026, 9, 14, 18, 30)
const at = (year: number, month: number, day: number, hour: number, minute: number, second = 0) =>
  new Date(year, month - 1, day, hour, minute, second).toISOString()
const plain = (value: string) => value.replace(/ /g, " ")

test("a timestamp names today and yesterday instead of their dates", () => {
  assert.equal(plain(formatTimestamp(at(2026, 10, 14, 17, 57), now)), "Today, 5:57 PM")
  assert.equal(plain(formatTimestamp(at(2026, 10, 13, 9, 5), now)), "Yesterday, 9:05 AM")
})

test("a timestamp shows the year only outside the current year", () => {
  assert.equal(plain(formatTimestamp(at(2026, 9, 30, 11, 0), now)), "Sep 30, 11:00 AM")
  assert.equal(plain(formatTimestamp(at(2025, 10, 14, 17, 57), now)), "Oct 14, 2025, 5:57 PM")
})

test("a timestamp and a clock time never show seconds", () => {
  const value = at(2026, 10, 14, 17, 57, 24)
  assert.equal(plain(formatTimestamp(value, now)), "Today, 5:57 PM")
  assert.equal(plain(formatClockTime(value)), "5:57 PM")
})

test("a date label is the timestamp without its time", () => {
  assert.equal(formatDate(at(2026, 10, 14, 0, 0), now), "Today")
  assert.equal(formatDate(at(2026, 10, 13, 23, 59), now), "Yesterday")
  assert.equal(formatDate(at(2026, 10, 12, 23, 59), now), "Oct 12")
  assert.equal(formatDate(at(2025, 12, 31, 12, 0), now), "Dec 31, 2025")
})

test("an unreadable value says so instead of throwing", () => {
  assert.equal(formatTimestamp("not a date", now), "Unknown time")
  assert.equal(formatDate("not a date", now), "Unknown date")
  assert.equal(formatClockTime("not a date"), "Unknown time")
})
