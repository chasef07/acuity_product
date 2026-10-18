import assert from "node:assert/strict"
import test from "node:test"
import { renderToStaticMarkup } from "react-dom/server"
import { JSDOM } from "jsdom"
import { OperatorJudges } from "./operator-judges.tsx"

const report = {
  version: "typesafe-scorecard-v8", totalCalls: 8, reportedCalls: 6, unavailableCalls: 2, yesAbove: 0.4,
  models: [{ model: "model-a", votes: 6, missingVotes: 0 }, { model: "model-b", votes: 4, missingVotes: 2 }],
  lineups: [{ models: ["model-a", "model-b"], requiredVotes: 2, calls: 6 }],
  pairs: [{ left: "model-a", right: "model-b", compared: 4, agreed: 3, disagreed: 1 }],
  questions: [{ key: "office_question_answered", label: "Office question answered correctly", prompt: "Was the answer supported?", question: "Check earlier knowledge.", yes: "Supported.", no: "Unsupported.", attempted: 6, quorumMet: 4, noQuorum: 2, unknown: 0, notApplicable: 0, unavailable: 0, pairs: [{ left: "model-a", right: "model-b", compared: 4, agreed: 3, disagreed: 1 }] }],
}

test("model agreement uses shared judgments rather than calls or expected votes", () => {
  const document = new JSDOM(renderToStaticMarkup(<OperatorJudges report={report} onRetry={() => {}} />)).window.document
  const cells = document.querySelector('[aria-label="Model agreement"] tbody tr')!.querySelectorAll("td")
  assert.deepEqual([...cells].map((cell) => cell.textContent), ["model-a", "model-b", "4", "3 · 75%", "1 · 25%"])
  assert.match(document.querySelector('[aria-label="Observed jury"]')!.textContent!, /6 of 8 calls/)
  assert.match(document.querySelector('[aria-label="Model vote coverage"]')!.textContent!, /model-b42/)
  assert.match(document.body.textContent!, /Agreement measures consistency, not correctness/)
})

test("missing or unpaired judge evidence does not render perfect agreement", () => {
  assert.match(renderToStaticMarkup(<OperatorJudges onRetry={() => {}} />), /Judges unavailable/)
  const noPairs = renderToStaticMarkup(<OperatorJudges report={{ ...report, pairs: [] }} onRetry={() => {}} />)
  assert.match(noPairs, /No paired votes yet/)
  assert.doesNotMatch(noPairs, /100%/)
})
