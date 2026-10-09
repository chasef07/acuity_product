import assert from "node:assert/strict"
import test from "node:test"
import { renderToStaticMarkup } from "react-dom/server"
import { ScorecardPanel } from "./call-review-scorecard.tsx"
import type { OperatorScorecardQuestion } from "../../lib/api/generated/types.gen.ts"

const catalog = new Map<string, OperatorScorecardQuestion>(
  ["booking_requested", "time_offered", "need_understood"].map((key, index) => [
    key,
    { key, code: ["B1", "B5", "H1"][index], label: key, prompt: `Short ${key}?`, group: "Synthetic", source: "judge" as const, question: `Synthetic ${key}?`, yes: "Yes.", no: "No.", appliesWhen: "Always." },
  ]),
)
const review = {
  interactionId: "00000000-0000-0000-0000-000000000001",
  assigned: true,
  sample: "random",
  facts: [],
  questions: ["booking_requested", "time_offered", "need_understood"],
  submitted: false,
  note: "",
  answers: [],
  judge: [],
}

test("the scorecard stays blind until the reviewer saves, and B5 waits for B1", () => {
  const html = renderToStaticMarkup(<ScorecardPanel review={review} catalog={catalog} onSaved={() => {}} />)
  assert.match(html, /0 of 2 answered/)
  assert.doesNotMatch(html, /Judge:/)
  assert.doesNotMatch(html, /B5/)
  assert.match(html, /Save and reveal judge/)
})

test("a saved review shows agreement and locks the answers", () => {
  const html = renderToStaticMarkup(<ScorecardPanel
    review={{
      ...review,
      submitted: true,
      answers: [
        { question: "booking_requested", answer: true, note: "" },
        { question: "time_offered", answer: true, note: "" },
        { question: "need_understood", answer: true, note: "" },
      ],
      judge: [
        { question: "booking_requested", answer: true, probability: 0.95, version: "typesafe-scorecard-v6" },
        { question: "time_offered", answer: false, version: "typesafe-scorecard-v6" },
        { question: "need_understood", answer: false, probability: 0.2, version: "typesafe-scorecard-v6" },
      ],
    }}
    catalog={catalog}
    onSaved={() => {}}
  />)
  assert.match(html, /Agrees/)
  assert.match(html, /Disagrees/)
  assert.match(html, /no availability returned/)
  assert.doesNotMatch(html, /Save and reveal judge/)
  assert.match(html, /disabled=""/)
})
