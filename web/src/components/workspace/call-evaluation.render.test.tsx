import assert from "node:assert/strict"
import test from "node:test"
import { renderToStaticMarkup } from "react-dom/server"
import { CallEvaluation } from "./call-evaluation.tsx"
import { juryScorecardVersions } from "../../lib/scorecard-flow.ts"

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

const allChecks = ["request_understood", "appointment_datetime_correct", "office_rules_grounded", "results_reported_truthfully", "conversation_responsive"]
const v4Checks = allChecks.filter((check) => check !== "results_reported_truthfully")
const v5Checks = v4Checks.filter((check) => check !== "request_understood")
const legacyChecks = (checks: string[], flagged: string[] = []) => checks.map((check) => ({ check, needsReview: flagged.includes(check) }))
const flaggedResponsive = legacyChecks(allChecks, ["conversation_responsive"])

const scorecard = {
  status: "complete",
  evaluatorVersion: "typesafe-scorecard-v1",
  results: Object.fromEntries([
    ...["request_understood", "appointment_datetime_correct", "office_rules_grounded", "results_reported_truthfully"].map((name) =>
      [name, { answers: { [name]: { type: "noul", noul: 0.85 } }, usage: { total_tokens: 42 } }]),
    ["expressed_sentiment", { answers: { expressed_sentiment: { type: "score", score: 2.5, probabilities: { "2": 0.5, "3": 0.5 } } } }],
  ]),
  errors: {},
}

test("partial scorecard preserves completed scores and exposes failures and missing results", () => {
  const html = renderToStaticMarkup(<CallEvaluation legacyScorecard={legacyChecks(allChecks)} evaluation={{
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

const scorecardV2 = {
  ...scorecard,
  evaluatorVersion: "typesafe-scorecard-v2",
  results: {
    ...scorecard.results,
    conversation_responsive: { answers: { conversation_responsive: { type: "noul", noul: 0.05 } } },
  },
}

test("v2 renders five checks including low responsiveness without inverting the score and an inapplicable date check unless it failed", () => {
  const html = renderToStaticMarkup(<CallEvaluation legacyScorecard={flaggedResponsive} evaluation={scorecardV2} />)
  assert.match(html, /Conversation responsive<\/dt><dd[^>]*>0.05 \/ 1/)
  assert.equal(html.match(/ \/ 1<\/dd>/g)?.length, 5)
  assert.match(html, /2.50 \/ 4/)
  assert.match(html, /Neutral or mixed: 50.0%/)
  assert.doesNotMatch(html, /No valid score was recorded|likely true/)
  assert.match(html, /Transcript evidence/)
  assert.match(html, /not measured silence or its technical cause/)
  assert.match(html, /&quot;noul&quot;: 0.05/)
  assert.match(html, /total_tokens/)

  const inapplicable = {
    ...scorecardV2,
    results: {
      ...scorecardV2.results,
      appointment_datetime_correct: { status: "not_applicable", reason: "no_appointment_action_result" },
    },
  }
  const notApplicable = renderToStaticMarkup(<CallEvaluation legacyScorecard={flaggedResponsive} evaluation={inapplicable} />)
  assert.match(notApplicable, /Appointment date and time correct<\/dt><dd[^>]*>Not applicable/)
  assert.match(notApplicable, /No booking, rescheduling, or cancellation tool returned a result/)
  assert.doesNotMatch(notApplicable, /No valid score was recorded/)
  assert.equal(notApplicable.match(/ \/ 1<\/dd>/g)?.length, 4)
  const failed = renderToStaticMarkup(<CallEvaluation legacyScorecard={flaggedResponsive} evaluation={{
    ...inapplicable, errors: { appointment_datetime_correct: { cause: "TimeoutError" } },
  }} />)
  assert.match(failed, /Judge failed: TimeoutError/)
  assert.doesNotMatch(failed, />Not applicable</)
})

test("v2 responsiveness preserves unavailable and judge error states", () => {
  for (const value of [undefined, null, "0.9", -0.1, 1.1, NaN, Infinity]) {
    const html = renderToStaticMarkup(<CallEvaluation legacyScorecard={legacyChecks(allChecks)} evaluation={{
      ...scorecardV2, results: {
        ...scorecard.results,
        conversation_responsive: { answers: { conversation_responsive: { type: "noul", noul: value } } },
      },
    }} />)
    assert.match(html, /Conversation responsive<\/dt><dd[^>]*>Unavailable/)
    assert.equal(html.match(/0.85 \/ 1/g)?.length, 4)
    assert.match(html, /No valid score was recorded/)
  }
  const failed = renderToStaticMarkup(<CallEvaluation legacyScorecard={legacyChecks(allChecks)} evaluation={{
    ...scorecardV2, errors: { conversation_responsive: { cause: "TimeoutError" } },
  }} />)
  assert.match(failed, /Conversation responsive<\/dt><dd[^>]*>Unavailable/)
  assert.match(failed, /Judge failed: TimeoutError/)
  assert.doesNotMatch(failed, /0.05 \/ 1/)
})

test("scorecard highlights exactly the checks the backend reading flags, not its own threshold", () => {
  for (const [value, flagged] of [[0, false], [0.9, true]] as const) {
    const html = renderToStaticMarkup(<CallEvaluation legacyScorecard={legacyChecks(["request_understood"], flagged ? ["request_understood"] : [])} evaluation={{
      ...scorecardV2, results: { request_understood: { answers: { request_understood: { type: "noul", noul: value } } } },
    }} />)
    assert.equal(html.includes(">Needs review<"), flagged)
  }
  const unread = renderToStaticMarkup(<CallEvaluation evaluation={scorecardV2} />)
  assert.doesNotMatch(unread, />Needs review</)
})

for (const [version, checks] of [["typesafe-scorecard-v1", allChecks], ["typesafe-scorecard-v2", allChecks], ["typesafe-scorecard-v3", allChecks], ["typesafe-scorecard-v4", v4Checks], ["typesafe-scorecard-v5", v5Checks]] as const) test(`${version} detail shows the backend's flagged checks`, () => {
  const results = Object.fromEntries(checks.map((name) => [name, { answers: { [name]: { type: "noul", noul: name === "conversation_responsive" ? 0.3 : 0.85 } } }]))
  const html = renderToStaticMarkup(<CallEvaluation legacyScorecard={legacyChecks([...checks], ["conversation_responsive"])} evaluation={{ ...scorecard, evaluatorVersion: version, results }} />)
  assert.equal(html.match(/ \/ 1<\/dd>/g)?.length, checks.length)
  assert.match(html, /Conversation responsive<\/dt><dd class="[^"]*text-destructive">0.30 \/ 1<\/dd><\/div><dd class="mt-1 text-destructive">Needs review/)
  assert.equal(html.match(/>Needs review</g)?.length, 1)
})

test("v3 renders the new scorecard contract and preserves review alerts", () => {
  const html = renderToStaticMarkup(<CallEvaluation legacyScorecard={flaggedResponsive} evaluation={{
    ...scorecardV2, evaluatorVersion: "typesafe-scorecard-v3",
    results: {
      ...scorecardV2.results,
      appointment_datetime_correct: { status: "not_applicable", reason: "no_appointment_action_result" },
    },
  }} />)
  assert.match(html, /Appointment date and time correct<\/dt><dd[^>]*>Not applicable/)
  assert.match(html, /Conversation responsive<\/dt><dd[^>]*>0.05 \/ 1/)
  assert.match(html, />Needs review</)
  assert.match(html, /2.50 \/ 4/)
  assert.doesNotMatch(html, /Requests resolved or handed off|No valid score was recorded|Automatic red highlights apply only/)
})

test("v4 omits the removed truthfulness judge while historical scorecards keep it", () => {
  const v4Results: Record<string, unknown> = { ...scorecardV2.results }
  delete v4Results.results_reported_truthfully
  const v4 = renderToStaticMarkup(<CallEvaluation legacyScorecard={legacyChecks(v4Checks, ["conversation_responsive"])} evaluation={{ ...scorecardV2, evaluatorVersion: "typesafe-scorecard-v4", results: v4Results }} />)
  assert.doesNotMatch(v4, /Action results reported truthfully|No valid score was recorded|Automatic red highlights apply only/)
  assert.match(v4, /Conversation responsive<\/dt><dd[^>]*>0.05 \/ 1/)
  assert.match(v4, />Needs review</)
  for (const version of ["typesafe-scorecard-v1", "typesafe-scorecard-v2", "typesafe-scorecard-v3"]) {
    const historical = renderToStaticMarkup(<CallEvaluation legacyScorecard={flaggedResponsive} evaluation={{ ...scorecardV2, evaluatorVersion: version }} />)
    assert.match(historical, /Action results reported truthfully<\/dt><dd[^>]*>0.85 \/ 1/)
  }
})

test("v5 omits the request-understood judge and keeps active review alerts and historical scores", () => {
  const results: Record<string, unknown> = { ...scorecardV2.results }
  delete results.request_understood
  delete results.results_reported_truthfully
  for (const status of ["complete", "incomplete"]) {
    const html = renderToStaticMarkup(<CallEvaluation legacyScorecard={legacyChecks(v5Checks, ["conversation_responsive"])} evaluation={{ ...scorecardV2, status, evaluatorVersion: "typesafe-scorecard-v5", results }} />)
    assert.doesNotMatch(html, /Caller request understood|Action results reported truthfully|No valid score was recorded|Automatic red highlights apply only/)
    assert.equal(html.match(/ \/ 1<\/dd>/g)?.length, 3)
    assert.match(html, /Conversation responsive<\/dt><dd[^>]*>0.05 \/ 1/)
    assert.match(html, />Needs review</)
    assert.match(html, /2.50 \/ 4/)
  }
  const failed = renderToStaticMarkup(<CallEvaluation legacyScorecard={legacyChecks(v5Checks)} evaluation={{
    ...scorecardV2, status: "incomplete", evaluatorVersion: "typesafe-scorecard-v5", results,
    errors: { conversation_responsive: { cause: "TimeoutError" } },
  }} />)
  assert.match(failed, /Judge failed: TimeoutError/)
  assert.doesNotMatch(failed, /Caller request understood|>Needs review</)
  for (const version of ["typesafe-scorecard-v1", "typesafe-scorecard-v2", "typesafe-scorecard-v3", "typesafe-scorecard-v4"]) {
    const historical = renderToStaticMarkup(<CallEvaluation legacyScorecard={legacyChecks(version === "typesafe-scorecard-v4" ? v4Checks : allChecks)} evaluation={{ ...scorecardV2, evaluatorVersion: version }} />)
    assert.match(historical, /Caller request understood<\/dt><dd[^>]*>0.85 \/ 1/)
  }
})

for (const version of ["typesafe-scorecard-v6", "typesafe-scorecard-v7"]) test(`jury scorecard ${version} shows the backend verdict, each juror's vote, quorum failures and sentiment`, () => {
  const question = (key: string, prompt: string, failure: boolean) => ({ key, code: "", label: "", prompt, source: "judge" as const, question: "", yes: "", no: "", failure })
  const questions = [question("booking_requested", "Did the caller ask to book?", false), question("need_understood", "Did the agent understand every request?", true), question("clear_and_responsive", "Was the call clear and responsive?", true)]
  const scorecard = [
    { question: "booking_requested", code: "B1", prompt: "", answer: "no" as const },
    { question: "need_understood", code: "H1", prompt: "", answer: "yes" as const },
    { question: "clear_and_responsive", code: "H4", prompt: "", answer: "no" as const },
  ]
  const html = renderToStaticMarkup(<CallEvaluation questions={questions} verdicts={scorecard} evaluation={{
    evaluator: "jury", evaluatorVersion: version, jurors: ["typesafe-ai/jev", "liquid/d1"], evaluatedAt: "2026-10-07T12:00:00Z", status: "incomplete",
    results: {
      booking_requested: { verdict: false, probability: 0.1, votes: { "typesafe-ai/jev": 0.1, "liquid/d1": 0.1 }, errors: {} },
      need_understood: { verdict: true, probability: 0.3, votes: { "typesafe-ai/jev": 0.45, "liquid/d1": 0.15 }, errors: {} },
      clear_and_responsive: { verdict: false, probability: 0.2, votes: { "typesafe-ai/jev": 0.1, "liquid/d1": 0.3 }, errors: {} },
      time_offered: { status: "not_applicable", reason: "no_availability_result" },
      expressed_sentiment: { score: 1.2, probabilities: { "1": 0.8 }, model: "typesafe-ai/jev" },
    },
    errors: { right_help: { cause: "no_quorum", votes: { "typesafe-ai/jev": 0.9 }, errors: { "liquid/d1": "TimeoutError" } } },
  }} />)
  assert.match(html, /Jury: jev, d1/)
  assert.match(html, /Was the call clear and responsive\?<\/dt><dd class="shrink-0 font-medium text-destructive">No/)
  assert.match(html, /Did the caller ask to book\?<\/dt><dd class="shrink-0 font-medium ">No/)
  assert.match(html, /Did the agent understand every request\?<\/dt><dd class="shrink-0 font-medium ">Yes/)
  assert.match(html, /d1: failed \(TimeoutError\)/)
  assert.match(html, /No quorum/)
  assert.match(html, /No availability search returned a result/)
  assert.match(html, /Negative · 1.2 \/ 4/)
})

for (const version of juryScorecardVersions) test(`${version} renders the jury scorecard`, () => {
  const html = renderToStaticMarkup(<CallEvaluation evaluation={{ evaluator: "jury", evaluatorVersion: version, jurors: ["model/a"], status: "complete", results: {} }} />)
  assert.match(html, /Jury: a\. The verdict is the majority/)
  assert.match(html, /Caller sentiment/)
  assert.doesNotMatch(html, /shown as recorded/)
})

for (const version of ["typesafe-scorecard-v8", "typesafe-scorecard-v9", "typesafe-scorecard-v10", "typesafe-scorecard-v11"]) test(`${version} shows unknown knowledge separately from failures and missing sentiment as unavailable`, () => {
 const question = (key: string, prompt: string) => ({ key, prompt, code: "", label: "", source: "judge" as const, question: "", yes: "", no: "", failure: true })
 const html = renderToStaticMarkup(<CallEvaluation questions={[question("knowledge_available", "Did returned knowledge contain the answer?"), question("transfer_policy_followed", "Was transfer policy followed?")]} verdicts={[{question: "transfer_policy_followed", code: "T1", prompt: "", answer: "no"}]} evaluation={{evaluatorVersion: version, evaluator:"jury", status:"complete", jurors:["a","b","c"], results:{knowledge_available:{status:"unknown",reason:"no_office_knowledge_result"}, transfer_policy_followed:{verdict:false, probability:0.2,votes:{a:0.1,b:0.2,c:0.3},errors:{}}}}} />)
 assert.match(html, /Was transfer policy followed\?<\/dt><dd class="shrink-0 font-medium text-destructive">Failed/)
 assert.match(html, />Unknown<\/dd>/)
 assert.match(html, /No usable office knowledge result was recorded/)
 assert.doesNotMatch(html, /Automatic red highlights|knowledge available/)
 assert.match(html, /Caller sentiment<\/dt><dd[^>]*>Unavailable/)
 assert.match(html, /No valid score was recorded/)
})

test("the intent/outcome flow cannot hide an expected missing check or turn a resolved call into a pass", () => {
  const questions = ["transfer_policy_followed", "call_ended_correctly"].map((key) => ({ key, code: key, label: key, prompt: key, source: "judge" as const, question: "", yes: "", no: "", failure: true }))
  const render = (answer: "yes" | "no", results: Record<string, unknown>) => renderToStaticMarkup(<CallEvaluation
    call={{ intent: "speak_to_staff", outcome: "resolved" }} questions={questions}
    verdicts={[{ question: "transfer_policy_followed", code: "T1", prompt: "", answer }]}
    evaluation={{ evaluatorVersion: "typesafe-scorecard-v8", status: "complete", results }}
  />)
  const missing = render("yes", { transfer_policy_followed: { verdict: true } })
  assert.match(missing, /Speak to staff/)
  assert.match(missing, /Resolved/)
  assert.match(missing, /Checks incomplete/)
  assert.match(missing, /call_ended_correctly/)
  assert.doesNotMatch(missing, /All applicable checks passed/)
  const failed = render("no", { transfer_policy_followed: { verdict: false }, call_ended_correctly: { status: "not_applicable" } })
  assert.match(failed, /1 failed check/)
  assert.doesNotMatch(failed, /All applicable checks passed/)
  const passed = render("yes", { transfer_policy_followed: { verdict: true }, call_ended_correctly: { status: "not_applicable" } })
  assert.match(passed, /All applicable checks passed/)
})

for (const score of [0, 1.5, 2, 4]) test(`v8 displays numeric sentiment ${score} independently of failure checks`, () => {
  const html = renderToStaticMarkup(<CallEvaluation evaluation={{
    evaluatorVersion: "typesafe-scorecard-v8", status: "complete", jurors: ["model/a", "model/b", "model/c"],
    results: { expressed_sentiment: { score, votes: { "model/a": score, "model/b": score }, errors: { "model/c": "TimeoutError" } } },
  }} />)
  assert.match(html, new RegExp(`Caller sentiment</dt><dd[^>]*>[^<]+ · ${score.toFixed(1)} / 4`))
  assert.match(html, new RegExp(`a: ${score.toFixed(1)} / 4`))
  assert.match(html, /c: failed \(TimeoutError\)/)
  assert.match(html, /Sentiment reflects transcript text, not vocal tone/)
  assert.doesNotMatch(html, /failed check|>Failed<|>No quorum<|a: .*%/)
})

for (const score of [undefined, null, "2", -1, 5, NaN, Infinity]) test(`v8 sentiment rejects invalid score ${String(score)}`, () => {
  const html = renderToStaticMarkup(<CallEvaluation evaluation={{
    evaluatorVersion: "typesafe-scorecard-v8", status: "complete", results: { expressed_sentiment: { score } },
  }} />)
  assert.match(html, /Caller sentiment<\/dt><dd[^>]*>Unavailable/)
  assert.match(html, /No valid score was recorded/)
  assert.doesNotMatch(html, /Neutral or mixed/)
})

test("v8 sentiment no quorum overrides a stale score and shows retained votes and failures", () => {
  const html = renderToStaticMarkup(<CallEvaluation evaluation={{
    evaluatorVersion: "typesafe-scorecard-v8", status: "incomplete", jurors: ["model/a", "model/b", "model/c"],
    results: { expressed_sentiment: { score: 2 } },
    errors: { expressed_sentiment: { cause: "no_quorum", votes: { "model/a": 0 }, errors: { "model/b": "TimeoutError", "model/c": "ValueError" } } },
  }} />)
  assert.match(html, /Caller sentiment<\/dt><dd[^>]*>Unavailable/)
  assert.match(html, /No quorum/)
  assert.match(html, /a: 0.0 \/ 4/)
  assert.match(html, /b: failed \(TimeoutError\)/)
  assert.match(html, /c: failed \(ValueError\)/)
  assert.doesNotMatch(html, /Neutral or mixed|failed check|>Failed</)
})

test("a sentiment-only failure does not make six completed checks incomplete", () => {
  const keys = ["knowledge_available", "office_question_answered", "appointment_datetime_correct", "call_ended_correctly", "transfer_policy_followed", "emergency_policy_followed"]
  const questions = keys.map((key) => ({ key, code: key, label: key, prompt: key, source: "judge" as const, question: "", yes: "", no: "", failure: true }))
  const verdicts = keys.map((question) => ({ question, code: question, prompt: question, answer: "yes" as const }))
  const results = Object.fromEntries(keys.map((key) => [key, { verdict: true, probability: 0.9 }]))
  const sentimentError = { cause: "no_quorum", votes: { a: 2 }, errors: { b: "TimeoutError", c: "TimeoutError" } }
  const render = (result: Record<string, unknown>, errors: Record<string, unknown>) => renderToStaticMarkup(<CallEvaluation
    questions={questions} verdicts={verdicts}
    evaluation={{ evaluatorVersion: "typesafe-scorecard-v8", status: "incomplete", jurors: ["a", "b", "c"], results: result, errors }}
  />)
  const html = render(results, { expressed_sentiment: sentimentError })
  assert.match(html, /All applicable checks passed/)
  assert.match(html, /Caller sentiment<\/dt><dd[^>]*>Unavailable/)
  assert.match(html, /No quorum/)
  assert.doesNotMatch(html, /Checks incomplete/)
  for (const incomplete of [
    render(results, {}),
    render(results, { expressed_sentiment: sentimentError, call_ended_correctly: { cause: "no_quorum" } }),
    render({ ...results, call_ended_correctly: { status: "unknown" } }, { expressed_sentiment: sentimentError }),
  ]) {
    assert.match(incomplete, /Checks incomplete/)
    assert.doesNotMatch(incomplete, /All applicable checks passed/)
  }
})

test("a call the agent did not hang up shows call ending as not applicable with its reason", () => {
 const question = (key: string, prompt: string) => ({ key, prompt, code: "", label: "", source: "judge" as const, question: "", yes: "", no: "", failure: true })
 const html = renderToStaticMarkup(<CallEvaluation questions={[question("call_ended_correctly", "Did the agent end the call only after the caller was finished?")]} verdicts={[]} evaluation={{evaluatorVersion: "typesafe-scorecard-v11", evaluator:"jury", status:"complete", jurors:["a","b","c"], results:{call_ended_correctly:{status:"not_applicable",reason:"no_agent_hangup"}}}} />)
 assert.match(html, />Not applicable<\/dd>/)
 assert.match(html, /The agent did not end the call; it was transferred or the caller hung up\./)
 assert.doesNotMatch(html, /text-destructive/)
})
