"use client"

import { useState } from "react"
import { CheckIcon, EyeIcon, NotebookPenIcon } from "lucide-react"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import { Textarea } from "@/components/ui/textarea"
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
  const [questionIdea, setQuestionIdea] = useState(review.questionIdea)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState("")

  const applicable = shown.questions.filter((question) => question !== "time_offered" || answers.booking_requested === true)
  const complete = applicable.every((question) => answers[question] !== undefined)
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
      note,
      questionIdea,
      answers: applicable.map((question) => ({ question, answer: answers[question], note: notes[question] ?? "" })),
    })
    setSaving(false)
    if (!outcome.ok) {
      setError(
        outcome.failure.kind === "rejected"
          ? "Answer every question, then save."
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
      <div className="min-h-0 flex-1 overflow-y-auto px-4 py-3">
        <div className="flex items-baseline justify-between gap-2">
          <h2 className="text-sm font-semibold">Scorecard</h2>
          <p className="text-xs text-muted-foreground">
            {revealed ? "Judge revealed" : `${applicable.filter((key) => answers[key] !== undefined).length} of ${applicable.length} answered`}
          </p>
        </div>
        <ol className="mt-1 divide-y">
          {applicable.map((key) => {
            const question = catalog.get(key)
            const value = answers[key]
            const noteOpen = openNotes[key] || Boolean(notes[key])
            return (
              <li key={key} className="space-y-2 py-3">
                <p className="text-[0.8125rem] leading-5" title={question?.question}>
                  <span className="mr-1.5 font-mono text-[0.6875rem] text-muted-foreground">{question?.code}</span>
                  {question?.prompt ?? key}
                </p>
                <div className="flex items-center gap-1.5">
                  <div role="group" aria-label={`${question?.code ?? key} answer`} className="flex gap-1">
                    <Button size="xs" className="h-7 w-14" variant={value === true ? "default" : "outline"} aria-pressed={value === true} disabled={revealed && value !== true} onClick={() => answer(key, true)}>
                      Yes
                    </Button>
                    <Button size="xs" className="h-7 w-14" variant={value === false ? "destructive" : "outline"} aria-pressed={value === false} disabled={revealed && value !== false} onClick={() => answer(key, false)}>
                      No
                    </Button>
                  </div>
                  {revealed ? (
                    <JudgeLine question={key} human={value} judged={judge.get(key)} />
                  ) : (
                    !noteOpen && (
                      <Button size="icon-xs" variant="ghost" className="ml-auto" aria-label={`Add a note to ${question?.code ?? key}`} title="Add note" onClick={() => setOpenNotes((current) => ({ ...current, [key]: true }))}>
                        <NotebookPenIcon aria-hidden="true" />
                      </Button>
                    )
                  )}
                </div>
                {noteOpen && (
                  <Textarea
                    aria-label={`${question?.code ?? key} note`}
                    placeholder="Why? Timestamps help."
                    className="min-h-10 text-xs"
                    value={notes[key] ?? ""}
                    readOnly={revealed}
                    onChange={(event) => setNotes((current) => ({ ...current, [key]: event.target.value }))}
                  />
                )}
              </li>
            )
          })}
        </ol>
        <div className="border-t pt-3">
          <label htmlFor="review-note" className="text-xs font-medium">Call note</label>
          <Textarea
            id="review-note"
            className="mt-1 min-h-12 text-xs"
            placeholder="What went wrong, with timestamps."
            value={note}
            readOnly={revealed}
            onChange={(event) => setNote(event.target.value)}
          />
          <label htmlFor="review-question-idea" className="mt-3 block text-xs font-medium">Missing question?</label>
          <p className="text-[0.6875rem] text-muted-foreground">Something went wrong that no question asks about. The nightly judge review collects these.</p>
          <Textarea
            id="review-question-idea"
            className="mt-1 min-h-10 text-xs"
            placeholder="e.g. The agent quoted a price without looking it up."
            value={questionIdea}
            readOnly={revealed}
            onChange={(event) => setQuestionIdea(event.target.value)}
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
            Save and reveal judge
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
    return <span className="ml-auto text-[0.6875rem] text-muted-foreground">Judge: no answer</span>
  }
  const agrees = human === judged.answer
  const detail = judged.probability !== undefined
    ? judged.probability.toFixed(2)
    : question === "time_offered" ? "no availability returned" : ""
  return (
    <span className="ml-auto flex items-center gap-1.5 text-[0.6875rem] text-muted-foreground" title={detail}>
      Judge {judged.answer ? "Yes" : "No"}
      <Badge variant={agrees ? "secondary" : "destructive"}>{agrees ? "Agrees" : "Disagrees"}</Badge>
    </span>
  )
}
