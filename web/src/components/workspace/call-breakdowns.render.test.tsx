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
  return new JSDOM(renderToStaticMarkup(<CallBreakdowns data={value} selection={selection} onSelection={() => {}} onRetry={() => {}} />)).window.document
}

test("biggest gaps lead the page, skipping intents with too few calls", () => {
  const gaps = [...render({}).querySelector('[aria-label="Biggest gaps"]')!.querySelectorAll("button")].map((button) => button.textContent)
  assert.deepEqual(gaps, [
    "New patient booking · Failed: Office knowledge had the answer · 57%",
    "New patient booking · Abandoned · 43%",
  ])
})

test("with no selection every call shows, and unlabeled calls stay in the totals without a link", () => {
  const page = render({})
  assert.match(page.querySelector('[aria-label="Selection"]')!.textContent!, /ShowingAll calls/)
  const intents = page.querySelector('section[aria-label="Intents"]')!
  assert.match(intents.textContent!, /New patient booking28 calls80%/)
  assert.match(intents.textContent!, /Unlabeled5 calls14.3%/)
  assert.equal(intents.querySelector('[aria-label="Select Unlabeled"]'), null)
  assert.ok(intents.querySelector('[aria-label="Select New patient booking"]'))
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
  const missing = renderToStaticMarkup(<CallBreakdowns selection={{}} onSelection={() => {}} onRetry={() => {}} />)
  assert.match(missing, /Breakdowns unavailable/)
  assert.match(missing, /Retry/)
  const empty = render({}, { ...data, totalCalls: 0, paths: [] })
  assert.match(empty.body.textContent!, /No calls in this selection/)
  assert.doesNotMatch(empty.body.textContent!, /NaN|Infinity|0%/)
})
