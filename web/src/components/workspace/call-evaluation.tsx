import { Badge } from "@/components/ui/badge"

export const evaluationLabels: Record<string, string> = {
  request_understood: "Caller request understood",
  appointment_datetime_correct: "Appointment date and time correct",
  office_rules_grounded: "Office rules supported by evidence",
  results_reported_truthfully: "Action results reported truthfully",
  conversation_responsive: "Conversation responsive",
  request_fulfilled: "Request fulfilled",
  handoff_required: "Handoff required",
  claims_supported: "Factual claims supported",
  request_specificity: "Request specificity",
  in_scope: "Request in scope",
  expressed_sentiment: "Expressed caller sentiment",
  reports_unresolved: "Caller reports unresolved issue",
  booking_requested: "B1 · Booking requested",
  time_offered: "B5 · Specific time offered",
  need_understood: "H1 · Understood every request",
  right_help: "H2 · Right help or next step",
  clear_and_responsive: "H4 · Clear and responsive",
  person_request_honored: "T1 · Person request honored",
}
export const evaluationScales: Record<string, string[]> = {
  request_specificity: ["Contradictory or shifting", "Vague", "Mostly clear", "Fully specified"],
  expressed_sentiment: ["Very negative", "Negative", "Neutral or mixed", "Positive", "Very positive"],
}
function record(value: unknown): Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : {}
}
function percent(value: unknown): string {
  return typeof value === "number" && Number.isFinite(value) && value >= 0 && value <= 1
    ? `${(value * 100).toFixed(1)}%` : "Unavailable"
}
function title(value: string): string {
  return value.replaceAll("_", " ")
}

const scorecardChecks = [
  "appointment_datetime_correct",
  "office_rules_grounded",
]
const unflaggedQuestions = ["booking_requested", "time_offered", "need_understood", "right_help", "clear_and_responsive", "person_request_honored"]
const notApplicableReasons: Record<string, string> = {
  no_appointment_action_result: "No booking, rescheduling, or cancellation tool returned a result.",
  no_availability_result: "No availability search returned a result.",
}

function Scorecard({ evaluation }: { evaluation: Record<string, unknown> }) {
  const results = record(evaluation.results)
  const errors = record(evaluation.errors)
  const version = evaluation.evaluatorVersion
  const current = version === "typesafe-scorecard-v6"
  const checks = current ? [...unflaggedQuestions, ...scorecardChecks, "expressed_sentiment"] : [
    ...(version !== "typesafe-scorecard-v5" ? ["request_understood"] : []),
    ...scorecardChecks,
    ...(version !== "typesafe-scorecard-v4" && version !== "typesafe-scorecard-v5" ? ["results_reported_truthfully"] : []),
    ...(version !== "typesafe-scorecard-v1" ? ["conversation_responsive"] : []),
    "expressed_sentiment",
  ]
  return <>
    <dl className="mt-4 divide-y rounded-lg border px-3">
      {checks.map((name) => {
        const answer = record(record(record(results[name]).answers)[name])
        const failed = Object.hasOwn(errors, name)
        const reason = record(results[name]).reason
        const notApplicable = !failed && record(results[name]).status === "not_applicable" &&
          typeof reason === "string" && Object.hasOwn(notApplicableReasons, reason)
        const error = record(errors[name])
        const sentiment = name === "expressed_sentiment"
        const value = sentiment ? answer.score : answer.noul
        const maximum = sentiment ? 4 : 1
        const valid = !failed && answer.type === (sentiment ? "score" : "noul") &&
          typeof value === "number" && Number.isFinite(value) && value >= 0 && value <= maximum
        const needsReview = valid && !notApplicable && !sentiment && value <= 0.4 && !unflaggedQuestions.includes(name) &&
          (evaluation.status === "complete" || evaluation.status === "incomplete")
        const probabilities = record(answer.probabilities)
        return <div key={name} className="py-3 text-xs">
          <div className="flex items-start justify-between gap-3">
            <dt>{evaluationLabels[name]}</dt>
            <dd className={`shrink-0 font-mono tabular-nums ${needsReview ? "text-destructive" : ""}`}>{notApplicable ? "Not applicable" : valid ? `${value.toFixed(2)} / ${maximum}` : "Unavailable"}</dd>
          </div>
          {needsReview && <dd className="mt-1 text-destructive">Needs review</dd>}
          {name === "conversation_responsive" && <dd className="mt-1 text-muted-foreground">Transcript evidence of a responsive conversation, not measured silence or its technical cause. Lower scores indicate caller evidence of stalls, even if the conversation later recovered. Ordinary greetings, clarifications, and caller-requested pauses do not count as stalls.</dd>}
          {failed ? <dd className="mt-1 text-destructive">
            Judge failed{typeof error.cause === "string" ? `: ${title(error.cause)}` : ""}
            {typeof error.httpStatus === "number" ? ` · HTTP ${error.httpStatus}` : ""}
            {typeof error.attempts === "number" ? ` · Attempts: ${error.attempts}` : ""}
          </dd> : notApplicable ? <dd className="mt-1 text-muted-foreground">{notApplicableReasons[String(reason)]}</dd> : !valid ? <dd className="mt-1 text-muted-foreground">No valid score was recorded.</dd> : sentiment ? <>
            <dd className="mt-1 text-muted-foreground">{evaluationScales.expressed_sentiment.map((label, index) => `${index}: ${label}`).join(" · ")}</dd>
            <dd className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-muted-foreground">
              {Object.entries(probabilities).map(([label, probability]) => <span key={label}>{evaluationScales.expressed_sentiment[Number(label)] ?? label}: {percent(probability)}</span>)}
            </dd>
          </> : null}
        </div>
      })}
    </dl>
    <p className="mt-3 text-xs text-muted-foreground">Model estimates, not verified outcomes. Higher check scores indicate stronger support for the criterion. Sentiment reflects caller language across the whole call, not vocal tone. Checks at 0.4 or lower are highlighted for review. Sentiment, inapplicable checks, and scorecard v6 questions still being measured against human review do not trigger review alerts.</p>
  </>
}

export function CallEvaluation({ evaluation }: { evaluation?: Record<string, unknown> }) {
  const results = record(evaluation?.results)
  const currentVersion = evaluation?.evaluatorVersion === "typesafe-trace-v4"
  const scorecard = evaluation?.evaluatorVersion === "typesafe-scorecard-v1" || evaluation?.evaluatorVersion === "typesafe-scorecard-v2" || evaluation?.evaluatorVersion === "typesafe-scorecard-v3" || evaluation?.evaluatorVersion === "typesafe-scorecard-v4" || evaluation?.evaluatorVersion === "typesafe-scorecard-v5" || evaluation?.evaluatorVersion === "typesafe-scorecard-v6"
  return (
    <section aria-label="AI evaluation" className="border-b px-5 py-4 sm:px-6">
      <div className="flex flex-wrap items-center gap-2">
        <h2 className="text-sm font-semibold">AI evaluation</h2>
        <Badge variant="outline">{typeof evaluation?.status === "string" ? title(evaluation.status) : "Not evaluated"}</Badge>
      </div>
      {!evaluation ? <p className="mt-2 text-xs text-muted-foreground">No evaluation was recorded for this call.</p> : <>
        <dl className="mt-3 grid grid-cols-1 gap-2 text-xs sm:grid-cols-2">
          {[["Evaluator", evaluation.evaluator], ["Model", evaluation.model], ["Version", evaluation.evaluatorVersion], ["Evaluated at", evaluation.evaluatedAt]].map(([label, value]) => (
            <div key={String(label)} className="min-w-0"><dt className="text-muted-foreground">{String(label)}</dt><dd className="break-words">{typeof value === "string" ? value : "Unavailable"}</dd></div>
          ))}
        </dl>
        {typeof evaluation.reason === "string" && <p className="mt-3 text-xs">{evaluation.status === "incomplete" ? "Evaluation incomplete" : "Evaluation unavailable"}: {title(evaluation.reason)}</p>}
        {scorecard ? <Scorecard evaluation={evaluation} /> : <>
        {!currentVersion && <p className="mt-3 text-xs text-muted-foreground">This evaluator version is shown as recorded. Automatic red highlights apply only to typesafe-trace-v4.</p>}
        {Object.entries(results).map(([group, raw]) => {
          const result = record(raw)
          const answers = record(result.answers)
          return <div key={group} className="mt-4">
            <h3 className="text-xs font-semibold capitalize">{title(group)}</h3>
            {typeof result.status === "string" && <p className="mt-1 text-xs text-muted-foreground">{title(result.status)}{typeof result.reason === "string" ? ` · ${result.reason}` : ""}</p>}
            <dl className="mt-2 divide-y rounded-lg border px-3">
              {Object.entries(answers).map(([name, rawAnswer]) => {
                const answer = record(rawAnswer)
                const scale = currentVersion ? evaluationScales[name] : undefined
                const probabilities = record(answer.probabilities)
                const score = typeof answer.score === "number" && Number.isFinite(answer.score) ? answer.score : undefined
                return <div key={name} className="py-2.5 text-xs">
                  <div className="flex items-start justify-between gap-3">
                    <dt>{evaluationLabels[name] ?? title(name)}</dt>
                    <dd className="shrink-0 font-mono tabular-nums">{answer.type === "boolean" ? `${percent(answer.probability)} likely true` : score !== undefined ? `${score.toFixed(2)}${scale ? ` / ${scale.length - 1}` : ""}` : "Unavailable"}</dd>
                  </div>
                  {scale && <dd className="mt-1 text-muted-foreground">{scale.map((label, index) => `${index}: ${label}`).join(" · ")}</dd>}
                  {Object.keys(probabilities).length > 0 && <dd className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-muted-foreground">
                    {Object.entries(probabilities).map(([label, probability]) => <span key={label}>{scale?.[Number(label)] ?? label}: {percent(probability)}</span>)}
                  </dd>}
                </div>
              })}
            </dl>
          </div>
        })}
        <p className="mt-3 text-xs text-muted-foreground">Model estimates, not verified outcomes. Red highlights indicate factual claims supported at 20% or lower, or caller reports unresolved issue at 80% or higher, on a complete v4 evaluation. Sentiment reflects transcript text, not vocal tone.</p>
        </>}
        <details className="mt-3 text-xs">
          <summary className="cursor-pointer font-medium">Full evaluation data, including usage</summary>
          <pre className="mt-2 max-h-80 overflow-auto whitespace-pre-wrap break-all rounded-lg bg-muted p-3">{JSON.stringify(evaluation, null, 2)}</pre>
        </details>
      </>}
    </section>
  )
}
