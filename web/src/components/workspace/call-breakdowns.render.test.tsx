import assert from "node:assert/strict"
import test from "node:test"
import { renderToStaticMarkup } from "react-dom/server"
import { JSDOM } from "jsdom"
import { CallBreakdowns } from "./call-breakdowns.tsx"
import type { CallSelection } from "./call-paths.ts"

const paths = [
  { intent: "new_patient_booking" as const, outcome: "abandoned" as const, evaluated: true, failures: [], calls: 12 },
  { intent: "new_patient_booking" as const, outcome: "resolved" as const, evaluated: true, failures: ["knowledge_available"], calls: 16 },
  { intent: "cancel_appointment" as const, outcome: "abandoned" as const, evaluated: true, failures: [], calls: 2 },
  { evaluated: false, failures: [], calls: 5 },
]
const data = {
  totalCalls: 35,
  daily: [],
  intents: [],
  outcomes: [],
  failures: [{ key: "knowledge_available", label: "Office knowledge had the answer", calls: 16 }],
  unlabeledIntentCalls: 5,
  unlabeledOutcomeCalls: 5,
  evaluatedCalls: 30,
  failedCalls: 16,
  unevaluatedCalls: 5,
  paths,
}

function render(selection: CallSelection, value = data) {
  return new JSDOM(renderToStaticMarkup(<CallBreakdowns data={value} selection={selection} onSelection={() => {}} mode="flow" onMode={() => {}} onRetry={() => {}} />)).window.document
}

test("biggest gaps lead the page, skipping intents with too few calls", () => {
  const gaps = [...render({}).querySelector('[aria-label="Biggest gaps"]')!.querySelectorAll("button")].map((button) => button.textContent)
  assert.deepEqual(gaps, [
    "New patient booking · Failed: Office knowledge had the answer · 57%",
    "New patient booking · Abandoned · 43%",
  ])
})

test("with no selection every call shows, and label shares count labeled calls only and say how many were left out", () => {
  const page = render({})
  assert.match(page.querySelector('[aria-label="Selection"]')!.textContent!, /ShowingAll calls/)
  const intents = page.querySelector('section[aria-label="Intents"]')!
  assert.match(intents.textContent!, /New patient booking28 calls93.3%/)
  assert.doesNotMatch(intents.textContent!, /Unlabeled/)
  assert.match(intents.textContent!, /Leaves out 5 calls without an intent\./)
  assert.match(page.querySelector('section[aria-label="Outcomes"]')!.textContent!, /Leaves out 5 calls without an outcome\./)
  assert.doesNotMatch(page.querySelector('section[aria-label="Judge results"]')!.textContent!, /Leaves out/)
  assert.ok(intents.querySelector('[aria-label="Select New patient booking"]'))
})

test("a flow with no fully labeled calls says so instead of drawing an empty chart", () => {
  const page = render({}, { ...data, paths: [paths[3]] })
  assert.match(page.querySelector('[aria-label="Call flow"]')!.textContent!, /No calls in this range have both an intent and an outcome\./)
})

test("a selection shows removable chips and narrows every list to its calls", () => {
  const page = render({ intent: "new_patient_booking", failure: "knowledge_available" })
  const chips = page.querySelector('[aria-label="Selection"]')!
  assert.ok(chips.querySelector('[aria-label="Remove Intent: New patient booking"]'))
  assert.ok(chips.querySelector('[aria-label="Remove Failed: Office knowledge had the answer"]'))
  assert.match(page.querySelector('section[aria-label="Outcomes"]')!.textContent!, /^Outcomes.*Resolved16 calls100%$/)
  assert.match(page.querySelector('section[aria-label="Judge results"]')!.textContent!, /Fail16 calls100%/)
})

test("missing distribution data is unavailable, and an empty window has no fabricated percentages", () => {
  const missing = renderToStaticMarkup(<CallBreakdowns selection={{}} onSelection={() => {}} mode="flow" onMode={() => {}} onRetry={() => {}} />)
  assert.match(missing, /Breakdowns unavailable/)
  assert.match(missing, /Retry/)
  const empty = render({ intent: "cancel_appointment" }, { ...data, totalCalls: 0, paths: [] })
  assert.match(empty.body.textContent!, /No calls in this date range/)
  assert.ok(empty.querySelector('[aria-label="Remove Intent: Cancel appointment"]'))
  assert.doesNotMatch(empty.body.textContent!, /NaN|Infinity|%/)
})
