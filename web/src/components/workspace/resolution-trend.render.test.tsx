import assert from "node:assert/strict"
import test from "node:test"
import { renderToStaticMarkup } from "react-dom/server"
import { JSDOM } from "jsdom"
import type { OperatorAiAnalyticsDistributionDay } from "@/lib/api/generated/types.gen"
import { intentResolution, outcomeShareDays, ResolutionTrend } from "./resolution-trend.tsx"

const route = (intent: string | undefined, outcome: string | undefined, calls: number) =>
  ({ intent, outcome, evaluated: true, failures: [], calls }) as OperatorAiAnalyticsDistributionDay["paths"][number]
const day = (date: string, paths: OperatorAiAnalyticsDistributionDay["paths"]) => ({ date, paths }) as OperatorAiAnalyticsDistributionDay

const days = [
  day("2026-11-01", [
    route("office_information", "resolved", 4),
    route("office_information", "transferred", 2),
    route("speak_to_staff", "transferred", 1),
    route("speak_to_staff", "abandoned", 1),
    route(undefined, undefined, 3),
    route("other", undefined, 1),
  ]),
  day("2026-11-02", []),
  day("2026-11-03", [route("office_information", "resolved", 2), route("speak_to_staff", "resolved", 6)]),
]

function render(selection = {}, value = days) {
  return new JSDOM(renderToStaticMarkup(<ResolutionTrend days={value} selection={selection} />)).window.document
}

test("daily outcome shares count only calls with an outcome, and empty days stay empty", () => {
  assert.deepEqual(outcomeShareDays(days, {}), [
    { date: "2026-11-01", resolved: 0.5, transferred: 0.375, abandoned: 0.125 },
    { date: "2026-11-02", resolved: null, transferred: null, abandoned: null },
    { date: "2026-11-03", resolved: 1, transferred: 0, abandoned: 0 },
  ])
})

test("daily outcome shares follow the page selection", () => {
  assert.deepEqual(outcomeShareDays(days, { intent: "speak_to_staff" }).map((row) => row.resolved), [0, null, 1])
})

test("intent resolution ranks intents by calls and leaves thin days blank", () => {
  const rows = intentResolution(days, {})
  assert.deepEqual(rows.map((row) => [row.intent, row.calls, row.rate]), [["office_information", 8, 0.75], ["speak_to_staff", 8, 0.75]])
  assert.deepEqual(rows[0].days.map((row) => [row.date, row.calls, row.rate]), [["2026-11-01", 6, 4 / 6], ["2026-11-02", 0, null], ["2026-11-03", 2, null]])
  assert.deepEqual(rows[1].days.map((row) => row.rate), [null, null, 1])
})

test("intent resolution keeps at most five intents", () => {
  const many = ["other", "speak_to_staff", "office_information", "cancel_appointment", "billing_question", "records_or_forms"]
    .map((intent, index) => route(intent, "resolved", 10 - index))
  assert.deepEqual(intentResolution([day("2026-11-01", many)], {}).map((row) => row.intent), ["other", "speak_to_staff", "office_information", "cancel_appointment", "billing_question"])
})

test("the card shows the overall outcome chart and says how many calls were left out", () => {
  const card = render().querySelector('[aria-label="Resolution over time"]')!
  assert.ok(card.querySelector('[aria-label="Daily outcome share"]'))
  assert.match(card.querySelector('[aria-label="Chart legend"]')!.textContent!, /Resolved.*Transferred.*Abandoned/)
  assert.match(card.textContent!, /Leaves out 4 calls without an outcome\./)
})

test("a range with no outcomes says so instead of drawing empty bars", () => {
  const card = render({}, [day("2026-11-01", [route(undefined, undefined, 3)])]).querySelector('[aria-label="Resolution over time"]')!
  assert.match(card.textContent!, /No calls in this range have an outcome\./)
  assert.equal(card.querySelector('[aria-label="Daily outcome share"]'), null)
})

test("by intent says so when no call has both an intent and an outcome", () => {
  assert.deepEqual(intentResolution([day("2026-11-01", [route(undefined, "resolved", 3)])], {}), [])
})
