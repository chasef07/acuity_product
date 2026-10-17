import assert from "node:assert/strict"
import test from "node:test"
import { homeDay, homeGreeting } from "./home-greeting.ts"

test("Home rotates through ten greetings once per New York calendar day", () => {
  const greetings = Array.from({ length: 10 }, (_, index) => homeGreeting(`2026-10-${String(index + 10).padStart(2, "0")}`).greeting)
  assert.equal(new Set(greetings).size, 10)
  for (const greeting of ["Good Morning", "Howdy", "Namaste"]) assert.ok(greetings.includes(greeting))
  assert.equal(homeGreeting("2026-10-10").greeting, homeGreeting("2026-10-20").greeting)
  assert.deepEqual(homeGreeting("2026-10-17"), homeGreeting("2026-10-17"))
  assert.equal(homeGreeting("2026-10-17").dateLabel, "Saturday October 17th 2026")
})

test("Home uses New York midnight, including daylight saving transitions", () => {
  for (const [instant, day] of [
    ["2026-10-18T03:59:59Z", "2026-10-17"],
    ["2026-10-18T04:00:00Z", "2026-10-18"],
    ["2026-03-08T06:59:59Z", "2026-03-08"],
    ["2026-03-08T07:00:00Z", "2026-03-08"],
    ["2026-11-01T05:59:59Z", "2026-11-01"],
    ["2026-11-01T06:00:00Z", "2026-11-01"],
    ["2027-01-01T04:59:59Z", "2026-12-31"],
    ["2027-01-01T05:00:00Z", "2027-01-01"],
  ]) assert.equal(homeDay(new Date(instant)), day)
})

test("Home date includes the year and correct day ordinals", () => {
  for (const ordinal of ["1st", "2nd", "3rd", "4th", "11th", "12th", "13th", "21st", "22nd", "23rd", "31st"]) {
    const day = ordinal.match(/^\d+/)![0].padStart(2, "0")
    assert.ok(homeGreeting(`2027-01-${day}`).dateLabel.endsWith(`January ${ordinal} 2027`))
  }
})
