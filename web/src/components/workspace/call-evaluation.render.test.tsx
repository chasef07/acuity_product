import assert from "node:assert/strict"
import test from "node:test"
import { renderToStaticMarkup } from "react-dom/server"
import { CallEvaluation } from "./call-evaluation.tsx"

test("missing and incomplete evaluations never imply a passing call", () => {
 const missing = renderToStaticMarkup(<CallEvaluation />)
 assert.match(missing, /Not evaluated/)
 const incomplete = renderToStaticMarkup(<CallEvaluation evaluation={{status:"incomplete",reason:"TimeoutError"}} />)
 assert.match(incomplete, /incomplete/)
 assert.match(incomplete, /TimeoutError/)
 assert.doesNotMatch(incomplete, /Passed/)
})

test("evaluation preserves probability distributions, usage and legacy metadata", () => {
 const evaluation = {
   status: "complete",
   evaluatorVersion: "typesafe-trace-v1",
   results: {
     reaction: {
       answers: {
         expressed_satisfaction: {type: "score", score: 2, probabilities: {"0": 0.1, "2": 0.9}},
       },
       usage: {total_tokens: 12},
     },
   },
 }
 const html = renderToStaticMarkup(<CallEvaluation evaluation={evaluation} />)
 assert.match(html, /typesafe-trace-v1/)
 assert.match(html, /expressed satisfaction/)
 assert.match(html, /90.0%/)
 assert.match(html, /total_tokens/)
 assert.match(html, /Automatic red highlights apply only/)
})

const scorecard = {
  status: "complete",
  evaluatorVersion: "typesafe-scorecard-v1",
  results: Object.fromEntries([
    ...["request_understood", "appointment_datetime_correct", "office_rules_grounded", "results_reported_truthfully", "resolved_or_handed_off"].map((name) =>
      [name, { answers: { [name]: { type: "noul", noul: 0.85 } }, usage: { total_tokens: 42 } }]),
    ["expressed_sentiment", { answers: { expressed_sentiment: { type: "score", score: 2.5, probabilities: { "2": 0.5, "3": 0.5 } } } }],
  ]),
  errors: {},
}

test("partial scorecard preserves completed scores and exposes failures and missing results", () => {
  const html = renderToStaticMarkup(<CallEvaluation evaluation={{
    ...scorecard, status: "incomplete", reason: "judge_errors",
    results: { request_understood: scorecard.results.request_understood },
    errors: { office_rules_grounded: { cause: "HTTPStatusError", httpStatus: 503, attempts: 2 } },
  }} />)
  assert.match(html, /0.85 \/ 1/)
  assert.match(html, /Evaluation incomplete: judge errors/)
  assert.match(html, /Judge failed: HTTPStatusError/)
  assert.match(html, /HTTP 503/)
  assert.match(html, /Attempts: 2/)
  assert.match(html, /No valid score was recorded/)
})

test("invalid or failed scorecard answers cannot appear as valid scores", () => {
  for (const value of [null, "0.9", -0.1, 1.1, NaN, Infinity]) {
    const html = renderToStaticMarkup(<CallEvaluation evaluation={{ ...scorecard, results: {
      request_understood: { answers: { request_understood: { type: "noul", noul: value } } },
    } }} />)
    assert.doesNotMatch(html, / \/ 1</)
    assert.match(html, /No valid score was recorded/)
  }
  const failed = renderToStaticMarkup(<CallEvaluation evaluation={{ ...scorecard, errors: { request_understood: { cause: "TimeoutError" } } }} />)
  assert.equal(failed.match(/0.85 \/ 1/g)?.length, 4)
  assert.match(failed, /Judge failed: TimeoutError/)
})

const scorecardV2 = {
  ...scorecard,
  evaluatorVersion: "typesafe-scorecard-v2",
  results: {
    ...scorecard.results,
    conversation_responsive: { answers: { conversation_responsive: { type: "noul", noul: 0.05 } } },
  },
}

test("v2 renders six checks including low responsiveness without inverting the score", () => {
  const html = renderToStaticMarkup(<CallEvaluation evaluation={scorecardV2} />)
  assert.match(html, /Conversation responsive<\/dt><dd[^>]*>0.05 \/ 1/)
  assert.equal(html.match(/ \/ 1<\/dd>/g)?.length, 6)
  assert.match(html, /2.50 \/ 4/)
  assert.match(html, /Neutral or mixed: 50.0%/)
  assert.doesNotMatch(html, /No valid score was recorded|likely true|text-destructive/)
  assert.match(html, /Transcript evidence/)
  assert.match(html, /not measured silence or its technical cause/)
  assert.match(html, /&quot;noul&quot;: 0.05/)
  assert.match(html, /total_tokens/)
})

test("partial v2 preserves responsiveness and sentiment alongside failed and missing checks", () => {
  const html = renderToStaticMarkup(<CallEvaluation evaluation={{
    ...scorecardV2, status: "incomplete", reason: "judge_errors",
    results: {
      conversation_responsive: scorecardV2.results.conversation_responsive,
      expressed_sentiment: scorecard.results.expressed_sentiment,
    },
    errors: { office_rules_grounded: { cause: "HTTPStatusError", httpStatus: 503, attempts: 2 } },
  }} />)
  assert.match(html, /0.05 \/ 1/)
  assert.match(html, /2.50 \/ 4/)
  assert.match(html, /Evaluation incomplete: judge errors/)
  assert.match(html, /Judge failed: HTTPStatusError/)
  assert.match(html, /HTTP 503/)
  assert.match(html, /Attempts: 2/)
  assert.equal(html.match(/No valid score was recorded/g)?.length, 4)
})

test("v2 responsiveness preserves unavailable and judge error states", () => {
  for (const value of [undefined, null, "0.9", -0.1, 1.1, NaN, Infinity]) {
    const html = renderToStaticMarkup(<CallEvaluation evaluation={{
      ...scorecardV2, results: {
        ...scorecard.results,
        conversation_responsive: { answers: { conversation_responsive: { type: "noul", noul: value } } },
      },
    }} />)
    assert.match(html, /Conversation responsive<\/dt><dd[^>]*>Unavailable/)
    assert.equal(html.match(/0.85 \/ 1/g)?.length, 5)
    assert.match(html, /No valid score was recorded/)
  }
  const failed = renderToStaticMarkup(<CallEvaluation evaluation={{
    ...scorecardV2, errors: { conversation_responsive: { cause: "TimeoutError" } },
  }} />)
  assert.match(failed, /Conversation responsive<\/dt><dd[^>]*>Unavailable/)
  assert.match(failed, /Judge failed: TimeoutError/)
  assert.doesNotMatch(failed, /0.05 \/ 1/)
})
