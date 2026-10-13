import assert from "node:assert/strict"
import test from "node:test"
import { renderToStaticMarkup } from "react-dom/server"
import { ScorecardPanel } from "./call-review-scorecard.tsx"
import type { OperatorScorecardQuestion } from "../../lib/api/generated/types.gen.ts"

const catalog = new Map<string, OperatorScorecardQuestion>(
  ["new_booking_requested", "time_offered", "promises_backed"].map((key, index) => [
    key,
    { key, code: ["B1", "B5", "P1"][index], label: key, prompt: `Short ${key}?`, source: "judge" as const, question: `Synthetic ${key}?`, yes: "Yes.", no: "No.", failure: key === "promises_backed" },
  ]),
)
const review = {
  interactionId: "00000000-0000-0000-0000-000000000001",
  assigned: true,
  facts: [],
  questions: ["new_booking_requested", "time_offered", "promises_backed"],
  submitted: false,
  note: "",
  questionIdea: "",
  answers: [],
  judge: [],
}

test("the scorecard stays blind until the reviewer saves, and asks exactly the questions the server gated in", () => {
  const html = renderToStaticMarkup(<ScorecardPanel review={review} catalog={catalog} onSaved={() => {}} onConflict={() => {}} />)
  assert.match(html, /0 of 3 answered/)
  assert.doesNotMatch(html, /Judge:/)
  assert.match(html, /B5/)
  const ungated = renderToStaticMarkup(<ScorecardPanel review={{ ...review, questions: ["new_booking_requested", "promises_backed"] }} catalog={catalog} onSaved={() => {}} onConflict={() => {}} />)
  assert.match(ungated, /0 of 2 answered/)
  assert.doesNotMatch(ungated, /B5/)
  assert.match(html, /Save and reveal judge/)
})

test("a saved review shows agreement and locks the answers", () => {
  const html = renderToStaticMarkup(<ScorecardPanel
    review={{
      ...review,
      submitted: true,
      answers: [
        { question: "new_booking_requested", answer: true, note: "" },
        { question: "time_offered", answer: true, note: "" },
        { question: "promises_backed", answer: true, note: "" },
      ],
      judge: [
        { question: "new_booking_requested", answer: true, probability: 0.95, version: "typesafe-scorecard-v7" },
        { question: "time_offered", answer: false, version: "typesafe-scorecard-v7" },
        { question: "promises_backed", answer: false, probability: 0.2, version: "typesafe-scorecard-v7" },
      ],
    }}
    catalog={catalog}
    onSaved={() => {}}
    onConflict={() => {}}
  />)
  assert.match(html, /Agrees/)
  assert.match(html, /Disagrees/)
  assert.match(html, /no availability returned/)
  assert.doesNotMatch(html, /Save and reveal judge/)
  assert.match(html, /disabled=""/)
})
