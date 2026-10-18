import assert from "node:assert/strict"
import test from "node:test"
import { renderToStaticMarkup } from "react-dom/server"
import { JSDOM } from "jsdom"
import { CallBreakdowns, BreakdownView } from "./call-breakdowns.tsx"

const data = {
  daily: [],
  intents: [{ intent: "office_information" as const, calls: 2 }, { intent: "existing_patient_booking" as const, calls: 5 }],
  outcomes: [{ outcome: "resolved" as const, calls: 6 }],
  unlabeledIntentCalls: 3,
  unlabeledOutcomeCalls: 4,
  failures: [{ key: "unmet", label: "Unmet request", calls: 2 }, { key: "wrong", label: "Wrong information", calls: 1 }],
  evaluatedCalls: 4,
  failedCalls: 2,
  unevaluatedCalls: 6,
}

function render(value = data, totalCalls = 10) {
  return new JSDOM(renderToStaticMarkup(totalCalls ? <>{(["intents", "outcomes", "failures"] as const).map((kind) => <BreakdownView key={kind} data={value} totalCalls={totalCalls} kind={kind} />)}</> : <CallBreakdowns data={value} totalCalls={totalCalls} onRetry={() => {}} />)).window.document
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
  const missing = renderToStaticMarkup(<CallBreakdowns totalCalls={10} onRetry={() => {}} />)
  assert.match(missing, /Breakdowns unavailable/)
  assert.match(missing, /Retry/)
  const empty = render(data, 0)
  assert.match(empty.body.textContent!, /No calls in this selection/)
  assert.doesNotMatch(empty.body.textContent!, /NaN|Infinity|0%/)
})
