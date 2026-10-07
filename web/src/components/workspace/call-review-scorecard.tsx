"use client"

import { useState } from "react"
import { CheckIcon, EyeIcon, NotebookPenIcon } from "lucide-react"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Spinner } from "@/components/ui/spinner"
import { Textarea } from "@/components/ui/textarea"
import { questionSummary } from "@/components/workspace/scorecard-overview"
import type {
  OperatorCallReview,
  OperatorScorecardQuestion,
} from "@/lib/api/generated/types.gen"
import { submitCallReview } from "@/lib/clients/call-review"

export function ScorecardPanel({
  review,
  catalog,
  onSaved,
  onNext,
}: {
  review: OperatorCallReview
  catalog: Map<string, OperatorScorecardQuestion>
  onSaved: () => void
  onNext?: () => void
}) {
  const [shown, setShown] = useState(review)
  const [answers, setAnswers] = useState<Record<string, boolean>>(
    () => Object.fromEntries(review.answers.map((answer) => [answer.question, answer.answer])),
  )
  const [notes, setNotes] = useState<Record<string, string>>(
    () => Object.fromEntries(review.answers.filter((answer) => answer.note).map((answer) => [answer.question, answer.note])),
  )
  const [openNotes, setOpenNotes] = useState<Record<string, boolean>>({})
  const [note, setNote] = useState(review.note)
  const [excluded, setExcluded] = useState(review.excluded)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState("")

  const applicable = shown.questions.filter((question) => question !== "time_offered" || answers.booking_requested === true)
  const complete = excluded || applicable.every((question) => answers[question] !== undefined)
  const judge = new Map(shown.judge.map((answer) => [answer.question, answer]))
  const revealed = shown.submitted

  function answer(question: string, value: boolean) {
    setAnswers((current) => {
      const next = { ...current, [question]: value }
      if (question === "booking_requested" && !value) delete next.time_offered
      return next
    })
  }

  async function save() {
    setSaving(true)
    setError("")
    const outcome = await submitCallReview(shown.interactionId, {
      excluded,
      note,
      answers: excluded
        ? []
        : applicable.map((question) => ({ question, answer: answers[question], note: notes[question] ?? "" })),
    })
    setSaving(false)
    if (!outcome.ok) {
      setError(
        outcome.failure.kind === "rejected"
          ? "Answer every question that applies, then save."
          : outcome.failure.kind === "conflict"
            ? "This call was already reviewed. Answers stay as first saved so the golden set stays blind."
            : "The review could not be saved. Try again.",
      )
      return
    }
    setShown(outcome.data)
    onSaved()
  }

  if (!shown.assigned) {
    return (
      <p className="p-4 text-sm text-muted-foreground">This call is not in your queue, so it cannot be reviewed here.</p>
    )
  }

  return (
    <>
      <div className="min-h-0 flex-1 space-y-3 overflow-y-auto p-4">
        <div>
          <h2 className="text-sm font-semibold">Scorecard</h2>
          <p className="mt-0.5 text-xs text-muted-foreground">
            {revealed ? "Saved. Judge answers are shown below each question." : "Answer before the judge is revealed."}
          </p>
        </div>
        <label className="flex items-center gap-2 text-xs text-muted-foreground">
          <input
            type="checkbox"
            className="size-3.5 accent-foreground"
            checked={excluded}
            disabled={revealed}
            onChange={(event) => setExcluded(event.target.checked)}
          />
          Exclude: test or internal call
        </label>
        {!excluded &&
          applicable.map((key) => {
            const question = catalog.get(key)
            const value = answers[key]
            const judged = judge.get(key)
            return (
              <Card key={key} size="sm" className="gap-2">
                <CardHeader className="gap-0.5">
                  <CardTitle className="text-[0.8125rem]">
                    <span className="mr-1.5 font-mono text-xs font-normal text-muted-foreground">{question?.code}</span>
                    {question?.label ?? key}
                  </CardTitle>
                  <CardDescription className="line-clamp-3 text-xs leading-5" title={question?.question}>{questionSummary(question)}</CardDescription>
                </CardHeader>
                <CardContent className="space-y-2">
                  <div role="group" aria-label={`${question?.code ?? key} answer`} className="grid grid-cols-2 gap-1.5">
                    <Button size="sm" variant={value === true ? "default" : "outline"} aria-pressed={value === true} disabled={revealed && value !== true} onClick={() => answer(key, true)}>
                      Yes
                    </Button>
                    <Button size="sm" variant={value === false ? "destructive" : "outline"} aria-pressed={value === false} disabled={revealed && value !== false} onClick={() => answer(key, false)}>
                      No
                    </Button>
                  </div>
                  {question && (
                    <details className="text-[0.6875rem] leading-5 text-muted-foreground">
                      <summary className="cursor-pointer select-none">What counts as yes or no</summary>
                      <p className="mt-1">{question.question}</p>
                      <p className="mt-1"><span className="font-medium text-foreground">Yes:</span> {question.yes}</p>
                      <p><span className="font-medium text-foreground">No:</span> {question.no}</p>
                    </details>
                  )}
                  {openNotes[key] || notes[key] ? (
                    <Textarea
                      aria-label={`${question?.code ?? key} note`}
                      placeholder="Why? Timestamps help."
                      className="min-h-12 text-xs"
                      value={notes[key] ?? ""}
                      readOnly={revealed}
                      onChange={(event) => setNotes((current) => ({ ...current, [key]: event.target.value }))}
                    />
                  ) : revealed ? null : (
                    <Button size="xs" variant="ghost" onClick={() => setOpenNotes((current) => ({ ...current, [key]: true }))}>
                      <NotebookPenIcon aria-hidden="true" /> Add note
                    </Button>
                  )}
                  {revealed && (
                    <JudgeLine question={key} human={answers[key]} judged={judged} />
                  )}
                </CardContent>
              </Card>
            )
          })}
        <div>
          <label htmlFor="review-note" className="text-xs font-medium">Call note</label>
          <Textarea
            id="review-note"
            className="mt-1 text-xs"
            placeholder="What went wrong, with timestamps."
            value={note}
            readOnly={revealed}
            onChange={(event) => setNote(event.target.value)}
          />
        </div>
      </div>
      <footer className="space-y-2 border-t p-4">
        {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
        {revealed ? (
          <Button className="w-full" disabled={!onNext} onClick={onNext}>
            <CheckIcon aria-hidden="true" /> Saved · Next call
          </Button>
        ) : (
          <Button className="w-full" disabled={!complete || saving} onClick={() => void save()}>
            {saving ? <Spinner /> : <EyeIcon aria-hidden="true" />}
            {excluded ? "Save as excluded" : "Save and reveal judge"}
          </Button>
        )}
      </footer>
    </>
  )
}

function JudgeLine({
  question,
  human,
  judged,
}: {
  question: string
  human?: boolean
  judged?: { answer: boolean; probability?: number; version: string }
}) {
  if (!judged) {
    return <p className="text-xs text-muted-foreground">Judge: no answer recorded for this call.</p>
  }
  const agrees = human === judged.answer
  return (
    <p className="flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground">
      Judge:
      <span className="font-semibold text-foreground">{judged.answer ? "Yes" : "No"}</span>
      {judged.probability !== undefined ? (
        <span className="font-mono tabular-nums">({judged.probability.toFixed(2)})</span>
      ) : question === "time_offered" ? (
        <span>(skipped: no availability was returned)</span>
      ) : null}
      <Badge variant={agrees ? "secondary" : "destructive"}>{agrees ? "Agrees" : "Disagrees"}</Badge>
    </p>
  )
}
