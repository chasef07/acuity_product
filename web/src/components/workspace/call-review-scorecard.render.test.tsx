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
  const html = renderToStaticMarkup(<ScorecardPanel review={review} catalog={catalog} onSaved={() => {}} />)
  assert.match(html, /0 of 3 answered/)
  assert.doesNotMatch(html, /Judge:/)
  assert.match(html, /B5/)
  const ungated = renderToStaticMarkup(<ScorecardPanel review={{ ...review, questions: ["new_booking_requested", "promises_backed"] }} catalog={catalog} onSaved={() => {}} />)
  assert.match(ungated, /0 of 2 answered/)
  assert.doesNotMatch(ungated, /B5/)
  assert.match(html, /Save and reveal judge/)
})

test("a saved review shows agreement and stays editable", () => {
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
  />)
  assert.match(html, /Agrees/)
  assert.match(html, /Disagrees/)
  assert.match(html, /title="0.95"/)
  assert.doesNotMatch(html, /Save and reveal judge/)
  assert.match(html, /Saved · Next call/)
  assert.doesNotMatch(html, /aria-pressed="false"[^>]*disabled=""/)
  assert.doesNotMatch(html, /readonly=""/i)
})

test("human flow uses its own answers and preserves excluded unknown checks without revealing the judge", () => {
  const questions = ["transfer_policy_followed", "knowledge_available", "appointment_datetime_correct"].map((key) => ({ key, code: key, label: key, prompt: key, source: "judge" as const, question: "", yes: "", no: "", failure: true }))
  const render = (answer: boolean, unknown: boolean) => renderToStaticMarkup(<ScorecardPanel
    review={{ ...review, questions: ["transfer_policy_followed"], answers: [{ question: "transfer_policy_followed", answer, note: "" }] }}
    catalog={new Map(questions.map((question) => [question.key, question]))} onSaved={() => {}}
    call={{ intent: "speak_to_staff", outcome: "transferred" }} evidenceReady
    evaluation={{ results: { knowledge_available: { status: unknown ? "unknown" : "not_applicable" }, appointment_datetime_correct: { status: "not_applicable" }, transfer_policy_followed: { verdict: false } } }}
  />)
  const incomplete = render(true, true)
  assert.match(incomplete, /Speak to staff/)
  assert.match(incomplete, /Transferred/)
  assert.match(incomplete, />Pass<|>Pass\s*</)
  assert.match(incomplete, /Checks incomplete/)
  assert.doesNotMatch(incomplete, /All applicable checks passed|Judge Fail|Judge Pass|Agrees|Disagrees/)
  assert.match(render(true, false), /All applicable checks passed/)
  assert.match(render(false, true), /1 failed check/)
})

test("revealing the judge exposes failures above collapsed details even when the reviewer passed the call", () => {
  const question = { key: "transfer_policy_followed", code: "T1", label: "Transfer policy followed", prompt: "Transfer policy?", source: "judge" as const, question: "", yes: "", no: "", failure: true }
  const html = renderToStaticMarkup(<ScorecardPanel
    review={{ ...review, submitted: true, questions: [question.key], answers: [{ question: question.key, answer: true, note: "" }], judge: [{ question: question.key, answer: false, version: "typesafe-scorecard-v8" }] }}
    catalog={new Map([[question.key, question]])} evidenceReady onSaved={() => {}}
  />)
  const summary = html.slice(0, html.indexOf("<details"))
  assert.match(summary, /Judge results/)
  assert.match(summary, /1 failed check/)
  assert.match(summary, /Transfer policy followed/)
  assert.doesNotMatch(summary, /All applicable checks passed/)
})

test("revealed judges include failures outside human question gates and preserve unavailable evidence", () => {
  const questions = ["call_ended_correctly", "appointment_datetime_correct"].map((key) => ({ key, code: key, label: key, prompt: key, source: "judge" as const, question: "", yes: "", no: "", failure: true }))
  const render = (result: Record<string, unknown>, errors = {}) => renderToStaticMarkup(<ScorecardPanel
    review={{ ...review, submitted: true, questions: ["call_ended_correctly"], answers: [{ question: "call_ended_correctly", answer: true, note: "" }], judge: questions.map((question) => ({ question: question.key, answer: question.key === "call_ended_correctly", version: "typesafe-scorecard-v8" })) }}
    catalog={new Map(questions.map((question) => [question.key, question]))} evidenceReady onSaved={() => {}}
    evaluation={{ results: { call_ended_correctly: { verdict: true }, appointment_datetime_correct: result }, errors }}
  />)
  const failed = render({ verdict: false })
  assert.match(failed.slice(0, failed.indexOf("<details")), /1 failed check/)
  assert.match(failed, /appointment_datetime_correct<\/span><span class="shrink-0">Failed/)
  assert.equal((failed.match(/role="group"/g) ?? []).length, 1)
  for (const html of [render({ status: "unknown" }), render({ verdict: false }, { appointment_datetime_correct: { reason: "quorum" } })]) {
    assert.match(html, /Checks incomplete/)
    assert.doesNotMatch(html, /1 failed check|All applicable checks passed/)
  }
  const inapplicable = render({ status: "not_applicable", verdict: false })
  assert.match(inapplicable, /All applicable checks passed/)
  assert.doesNotMatch(inapplicable, /1 failed check/)
})

test("blind reviews require explicit inapplicability and complete evaluation evidence for a pass", () => {
  const questions = ["call_ended_correctly", "appointment_datetime_correct"].map((key) => ({ key, code: key, label: key, prompt: key, source: "judge" as const, question: "", yes: "", no: "", failure: true }))
  const render = (evaluation: Record<string, unknown>) => renderToStaticMarkup(<ScorecardPanel
    review={{ ...review, questions: ["call_ended_correctly"], answers: [{ question: "call_ended_correctly", answer: true, note: "" }] }}
    catalog={new Map(questions.map((question) => [question.key, question]))} evidenceReady onSaved={() => {}}
    evaluation={evaluation}
  />)
  for (const evaluation of [
    { status: "complete", results: {} },
    { status: "incomplete", errors: { appointment_datetime_correct: { reason: "quorum" } } },
    { status: "complete", results: { appointment_datetime_correct: { status: "not_applicable" } }, errors: { appointment_datetime_correct: { reason: "quorum" } } },
    { status: "incomplete", results: { appointment_datetime_correct: { status: "not_applicable" } } },
  ]) {
    const html = render(evaluation)
    assert.match(html, /Checks incomplete/, JSON.stringify(evaluation))
    assert.doesNotMatch(html, /All applicable checks passed|Judge Fail|Judge Pass|Agrees|Disagrees/)
  }
  const applicable = render({ status: "complete", results: { call_ended_correctly: { verdict: false }, appointment_datetime_correct: { status: "not_applicable" } } })
  assert.match(applicable, /All applicable checks passed/)
})
