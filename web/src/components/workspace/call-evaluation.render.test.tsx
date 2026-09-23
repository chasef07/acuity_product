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
