import assert from "node:assert/strict"
import test from "node:test"
import { renderToStaticMarkup } from "react-dom/server"
import { JSDOM } from "jsdom"
import { CallBreakdowns, BreakdownView, initialBreakdownSelection } from "./call-breakdowns.tsx"

const data = {
  totalCalls: 10,
  daily: [],
  intents: [{ intent: "office_information" as const, calls: 2 }, { intent: "existing_patient_booking" as const, calls: 5 }],
  outcomes: [{ outcome: "resolved" as const, calls: 6 }],
  unlabeledIntentCalls: 3,
  unlabeledOutcomeCalls: 4,
  failures: [{ key: "unmet", label: "Unmet request", calls: 2 }, { key: "wrong", label: "Wrong information", calls: 1 }],
  evaluatedCalls: 4,
  failedCalls: 2,
  unevaluatedCalls: 6,
  paths: [],
}

function render(value = data, totalCalls = 10) {
  return new JSDOM(renderToStaticMarkup(totalCalls ? <>{(["intents", "outcomes", "failures"] as const).map((kind) => <BreakdownView key={kind} data={{ ...value, totalCalls }} kind={kind} />)}</> : <CallBreakdowns data={{ ...value, totalCalls }} selection={initialBreakdownSelection} onSelection={() => {}} onRetry={() => {}} onViewCalls={() => {}} />)).window.document
}

test("breakdowns include unlabeled calls in label denominators and distinguish failure flags from calls", () => {
  const document = render()
  const intents = document.querySelector('[aria-label="Intents"]')!
  assert.equal(intents.querySelector("dt")!.textContent, "Existing patient booking")
  assert.match(intents.textContent!, /5 calls50%/)
  assert.match(intents.textContent!, /Unlabeled3 calls30%/)
  const outcomes = document.querySelector('[aria-label="Outcomes"]')!
  assert.match(outcomes.textContent!, /Resolved6 calls60%/)
  assert.match(outcomes.textContent!, /Unlabeled4 calls40%/)
  const failures = document.querySelector('[aria-label="Judge failures"]')!
  assert.match(failures.textContent!, /3 flags across 2 calls/)
  assert.match(failures.textContent!, /Unmet request2 calls66.7%/)
  assert.match(failures.textContent!, /4 of 10 calls evaluated · 6 unevaluated/)
})

test("no evaluation is distinct from evaluated calls with no failures", () => {
  const noScores = render({ ...data, evaluatedCalls: 0, failedCalls: 0, unevaluatedCalls: 10, failures: [] })
  assert.match(noScores.querySelector('[aria-label="Judge failures"]')!.textContent!, /No evaluated calls/)
  const noFailures = render({ ...data, failedCalls: 0, failures: [] })
  assert.match(noFailures.querySelector('[aria-label="Judge failures"]')!.textContent!, /No judge failures recorded/)
  assert.doesNotMatch(noFailures.body.textContent!, /All calls passed/)
})

test("missing distribution data is unavailable, and an empty window has no fabricated percentages", () => {
  const missing = renderToStaticMarkup(<CallBreakdowns selection={initialBreakdownSelection} onSelection={() => {}} onRetry={() => {}} onViewCalls={() => {}} />)
  assert.match(missing, /Breakdowns unavailable/)
  assert.match(missing, /Retry/)
  const empty = render(data, 0)
  assert.match(empty.body.textContent!, /No calls in this selection/)
  assert.doesNotMatch(empty.body.textContent!, /NaN|Infinity|0%/)
})

test("the unfiltered view leads with problems to fix; a filtered view does not", () => {
  const paths = [
    { intent: "new_patient_booking" as const, outcome: "abandoned" as const, evaluated: true, failures: [], calls: 12 },
    { intent: "new_patient_booking" as const, outcome: "resolved" as const, evaluated: true, failures: ["knowledge_available"], calls: 16 },
    { intent: "cancel_appointment" as const, outcome: "abandoned" as const, evaluated: true, failures: [], calls: 2 },
  ]
  const withPaths = { ...data, totalCalls: 30, paths, failures: [{ key: "knowledge_available", label: "Office knowledge had the answer", calls: 16 }] }
  const page = (selection: typeof initialBreakdownSelection) => new JSDOM(renderToStaticMarkup(<CallBreakdowns data={withPaths} selection={selection} onSelection={() => {}} onRetry={() => {}} onViewCalls={() => {}} />)).window.document
  const problems = page(initialBreakdownSelection).querySelector('[aria-label="Problems to fix"]')!
  const rows = [...problems.querySelectorAll("li")].map((row) => row.textContent)
  assert.match(rows[0]!, /New patient booking.*Failed: Office knowledge had the answer.*16 calls.*57% of 28.*53% avg/)
  assert.match(rows[1]!, /New patient booking.*Abandoned.*12 calls.*43% of 28.*47% avg/)
  assert.match(rows[2]!, /Cancel appointment.*Too few calls to compare/)
  assert.ok(problems.querySelector('[aria-label="View calls: New patient booking, Abandoned"]'))
  assert.equal(page({ kind: "outcomes", intent: "new_patient_booking", outcome: "all" }).querySelector('[aria-label="Problems to fix"]'), null)
})
