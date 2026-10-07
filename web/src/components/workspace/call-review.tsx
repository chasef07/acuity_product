"use client"

import { useEffect, useEffectEvent, useState } from "react"
import {
  CheckIcon,
  ChevronLeftIcon,
  ChevronRightIcon,
  CircleAlertIcon,
  EyeIcon,
  InboxIcon,
  NotebookPenIcon,
} from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import { Input } from "@/components/ui/input"
import {
  MessageScroller,
  MessageScrollerContent,
  MessageScrollerProvider,
  MessageScrollerViewport,
} from "@/components/ui/message-scroller"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Textarea } from "@/components/ui/textarea"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import {
  formatOffset,
  TimelineItem,
  timelineEntries,
} from "@/components/workspace/operator-analytics-detail"
import {
  questionSummary,
  ScorecardDefinitionsButton,
  useQuestionCatalog,
} from "@/components/workspace/scorecard-overview"
import type {
  OperatorCallReview,
  OperatorCallReviewQueueCall,
  OperatorScorecardQuestion,
} from "@/lib/api/generated/types.gen"
import { useAiCallEvidence } from "@/lib/clients/agent-calls"
import {
  submitCallReview,
  useCallReview,
  useJudgeAccuracy,
  useReviewQueue,
} from "@/lib/clients/call-review"
import { formatShortDateTime } from "@/lib/format"

const silenceSeconds = 8

type ReviewView = "queue" | "accuracy"

export function CallReviewWorkspace({
  practiceID,
  onOpenCall,
}: {
  practiceID: string
  onOpenCall: (interactionID: string) => void
}) {
  const [view, setView] = useState<ReviewView>("queue")
  const [date, setDate] = useState(yesterday)
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <ToggleGroup
          variant="segmented"
          spacing={1}
          value={[view]}
          aria-label="Review view"
          onValueChange={(values) => {
            if (values[0] === "queue" || values[0] === "accuracy") setView(values[0])
          }}
        >
          <ToggleGroupItem value="queue">Daily queue</ToggleGroupItem>
          <ToggleGroupItem value="accuracy">Judge accuracy</ToggleGroupItem>
        </ToggleGroup>
        <div className="flex items-center gap-2">
          {view === "queue" && (
            <label className="flex items-center gap-2 text-xs text-muted-foreground">
              Calls from
              <Input
                type="date"
                className="h-8 w-40"
                value={date}
                max={yesterday()}
                onChange={(event) => event.target.value && setDate(event.target.value)}
              />
            </label>
          )}
          <ScorecardDefinitionsButton />
        </div>
      </div>
      {view === "queue" ? (
        <ReviewQueue key={`${practiceID}:${date}`} practiceID={practiceID} date={date} />
      ) : (
        <JudgeAccuracyView practiceID={practiceID} onOpenCall={onOpenCall} />
      )}
    </div>
  )
}

function yesterday() {
  const value = new Date()
  value.setDate(value.getDate() - 1)
  return `${value.getFullYear()}-${String(value.getMonth() + 1).padStart(2, "0")}-${String(value.getDate()).padStart(2, "0")}`
}

function ReviewQueue({ practiceID, date }: { practiceID: string; date: string }) {
  const [revision, setRevision] = useState(0)
  const queue = useReviewQueue(practiceID, date, revision)
  const [selected, setSelected] = useState("")
  const calls = queue.status === "ready" || (queue.status === "failed" && queue.data) ? (queue.data?.calls ?? []) : []
  const selectedID = selected || calls.find((call) => !call.completedAt)?.interactionId || calls[0]?.interactionId || ""
  const index = calls.findIndex((call) => call.interactionId === selectedID)

  const onKey = useEffectEvent((event: KeyboardEvent) => {
    const target = event.target as HTMLElement | null
    if (target && (target.tagName === "TEXTAREA" || target.tagName === "INPUT" || target.isContentEditable)) return
    if (event.key === "j" && index < calls.length - 1) setSelected(calls[index + 1].interactionId)
    if (event.key === "k" && index > 0) setSelected(calls[index - 1].interactionId)
  })
  useEffect(() => {
    const listener = (event: KeyboardEvent) => onKey(event)
    window.addEventListener("keydown", listener)
    return () => window.removeEventListener("keydown", listener)
  }, [])

  if (queue.status === "loading") {
    return <Skeleton className="h-[32rem] rounded-xl" />
  }
  if (queue.status === "failed" && !queue.data) {
    return (
      <Alert variant="destructive" className="max-w-2xl">
        <CircleAlertIcon aria-hidden="true" />
        <AlertTitle>Review queue unavailable</AlertTitle>
        <AlertDescription>
          <p>{queue.failure.kind === "unauthorized" ? "Only Platform Operators review calls." : "The day's calls could not be assigned."}</p>
          <Button className="mt-3" size="sm" variant="outline" onClick={queue.retry}>Retry</Button>
        </AlertDescription>
      </Alert>
    )
  }
  if (calls.length === 0) {
    return (
      <Empty className="min-h-72 border bg-card">
        <EmptyHeader>
          <EmptyMedia variant="icon"><InboxIcon aria-hidden="true" /></EmptyMedia>
          <EmptyTitle>No calls to review for this day</EmptyTitle>
          <EmptyDescription>
            The queue samples calls of 30 seconds or longer. Pick another day.
          </EmptyDescription>
        </EmptyHeader>
      </Empty>
    )
  }
  const done = calls.filter((call) => call.completedAt).length
  const selectedCall = calls[index]
  return (
    <div className="grid h-[calc(100dvh-18rem)] min-h-[34rem] grid-cols-1 overflow-hidden rounded-xl border bg-card lg:grid-cols-[15rem_minmax(0,1fr)_22rem]">
      <aside aria-label="Calls to review" className="flex min-h-0 flex-col border-b lg:border-r lg:border-b-0">
        <div className="border-b px-4 py-3">
          <p className="text-sm font-semibold">{done} of {calls.length} reviewed</p>
          <div className="mt-2 h-1 overflow-hidden rounded-full bg-muted">
            <div className="h-1 rounded-full bg-foreground transition-all" style={{ width: `${(100 * done) / calls.length}%` }} />
          </div>
          <p className="mt-2 text-[0.6875rem] text-muted-foreground">
            <kbd className="rounded border px-1">j</kbd> / <kbd className="rounded border px-1">k</kbd> next and previous call
          </p>
        </div>
        <ol className="min-h-0 flex-1 overflow-y-auto py-1">
          {calls.map((call, position) => (
            <li key={call.interactionId}>
              <QueueRow call={call} position={position + 1} active={call.interactionId === selectedID} onSelect={() => setSelected(call.interactionId)} />
            </li>
          ))}
        </ol>
      </aside>
      {selectedCall && (
        <ReviewCall
          key={selectedCall.interactionId}
          call={selectedCall}
          position={index + 1}
          onPrevious={index > 0 ? () => setSelected(calls[index - 1].interactionId) : undefined}
          onNext={index < calls.length - 1 ? () => setSelected(calls[index + 1].interactionId) : undefined}
          onSaved={() => {
            setSelected(selectedCall.interactionId)
            setRevision((value) => value + 1)
          }}
        />
      )}
    </div>
  )
}

function QueueRow({
  call,
  position,
  active,
  onSelect,
}: {
  call: OperatorCallReviewQueueCall
  position: number
  active: boolean
  onSelect: () => void
}) {
  const state = call.excluded ? "Excluded" : call.completedAt ? "Reviewed" : "To review"
  return (
    <button
      type="button"
      aria-current={active || undefined}
      aria-label={`Call ${position}, ${call.locationName}, ${formatShortDateTime(call.startedAt)}, ${state}`}
      onClick={onSelect}
      className="flex w-full items-start gap-2.5 border-l-2 border-transparent px-4 py-2 text-left outline-none transition-colors hover:bg-muted focus-visible:bg-muted aria-[current]:border-foreground aria-[current]:bg-muted"
    >
      <span
        aria-hidden="true"
        className={`mt-1.5 size-2 shrink-0 rounded-full border ${
          call.excluded ? "border-muted-foreground bg-muted-foreground" : call.completedAt ? "border-foreground bg-foreground" : "border-muted-foreground"
        }`}
      />
      <span className="min-w-0 flex-1">
        <span className="block truncate text-xs font-medium">
          #{position} · {call.locationName}
        </span>
        <span className="block text-[0.6875rem] text-muted-foreground tabular-nums">
          {formatShortDateTime(call.startedAt)} · {formatDuration(call.durationSeconds)}
        </span>
        <span className="mt-1 flex flex-wrap gap-1">
          {call.sample === "flagged" && <Badge variant="destructive">Flagged</Badge>}
          {call.overlap && <Badge variant="outline">Shared</Badge>}
          {call.excluded && <Badge variant="secondary">Excluded</Badge>}
        </span>
      </span>
    </button>
  )
}

function ReviewCall({
  call,
  position,
  onPrevious,
  onNext,
  onSaved,
}: {
  call: OperatorCallReviewQueueCall
  position: number
  onPrevious?: () => void
  onNext?: () => void
  onSaved: () => void
}) {
  const review = useCallReview(call.interactionId)
  const catalog = useQuestionCatalog()
  return (
    <>
      <section aria-label="Call transcript" className="flex min-h-0 flex-col border-b lg:border-r lg:border-b-0">
        <header className="border-b px-5 py-3">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <h2 className="text-sm font-semibold">
              #{position} · {call.locationName} · {formatShortDateTime(call.startedAt)} · {formatDuration(call.durationSeconds)}
            </h2>
            <nav aria-label="Call navigation" className="flex gap-1">
              <Button size="icon-sm" variant="ghost" aria-label="Previous call" disabled={!onPrevious} onClick={onPrevious}>
                <ChevronLeftIcon aria-hidden="true" />
              </Button>
              <Button size="icon-sm" variant="ghost" aria-label="Next call" disabled={!onNext} onClick={onNext}>
                <ChevronRightIcon aria-hidden="true" />
              </Button>
            </nav>
          </div>
          {review.status === "ready" && review.data.facts.length > 0 && (
            <ul aria-label="Code-check facts" className="mt-2 flex flex-wrap gap-1.5">
              {review.data.facts.map((fact) => (
                <li key={fact.question}>
                  <FactChip question={catalog.get(fact.question)} answer={fact.answer} detail={fact.detail} />
                </li>
              ))}
            </ul>
          )}
        </header>
        <Transcript interactionID={call.interactionId} />
      </section>
      <aside aria-label="Scorecard" className="flex min-h-0 flex-col">
        {review.status === "loading" && (
          <div className="flex flex-1 items-center justify-center gap-2 text-sm text-muted-foreground">
            <Spinner /> Loading scorecard
          </div>
        )}
        {review.status === "failed" && (
          <div className="p-4">
            <Alert variant="destructive">
              <CircleAlertIcon aria-hidden="true" />
              <AlertTitle>Scorecard unavailable</AlertTitle>
              <AlertDescription>
                <Button className="mt-2" size="sm" variant="outline" onClick={review.retry}>Retry</Button>
              </AlertDescription>
            </Alert>
          </div>
        )}
        {review.status === "ready" && (
          <ScorecardPanel review={review.data} catalog={catalog} onSaved={onSaved} onNext={onNext} />
        )}
      </aside>
    </>
  )
}

function FactChip({
  question,
  answer,
  detail,
}: {
  question?: OperatorScorecardQuestion
  answer: boolean
  detail?: Record<string, unknown>
}) {
  const extra = [detail?.reason, detail?.plan].filter((value): value is string => typeof value === "string" && value !== "")
  const failing = !answer && question?.key !== "scheduling_tool_called" && question?.key !== "booking_blocked"
  return (
    <span
      title={question?.question}
      className={`inline-flex items-center gap-1 rounded-full bg-muted px-2 py-0.5 text-[0.6875rem] text-muted-foreground ${failing ? "ring-1 ring-destructive/30" : ""}`}
    >
      <span className="font-mono">{question?.code}</span>
      {question?.label}:
      <span className={`font-semibold ${failing ? "text-destructive" : "text-foreground"}`}>{answer ? "Yes" : "No"}</span>
      {extra.length > 0 && <span>· {extra.join(" · ")}</span>}
    </span>
  )
}

function Transcript({ interactionID }: { interactionID: string }) {
  const evidence = useAiCallEvidence(interactionID)
  if (evidence.status === "loading") {
    return (
      <div className="flex flex-1 items-center justify-center gap-2 text-sm text-muted-foreground">
        <Spinner /> Loading transcript
      </div>
    )
  }
  if (evidence.status === "failed") {
    return (
      <div className="p-5">
        <Alert variant="destructive">
          <CircleAlertIcon aria-hidden="true" />
          <AlertTitle>Transcript unavailable</AlertTitle>
          <AlertDescription>
            <Button className="mt-2" size="sm" variant="outline" onClick={evidence.retry}>Retry</Button>
          </AlertDescription>
        </Alert>
      </div>
    )
  }
  const detail = evidence.data
  const entries = timelineEntries(detail.timeline)
  const executions = new Map(detail.toolExecutions.map((execution) => [execution.callId, execution]))
  return (
    <MessageScrollerProvider autoScroll={false} defaultScrollPosition="start">
      <MessageScroller className="min-h-0 flex-1">
        <MessageScrollerViewport aria-label="Scrollable transcript">
          <MessageScrollerContent className="gap-0" aria-live="off">
            <div role="list" aria-label="Conversation" className="flex flex-col gap-4 px-5 py-5">
              {entries.length === 0 && (
                <p className="rounded-lg border border-dashed px-4 py-8 text-center text-sm text-muted-foreground">
                  No transcript was recorded for this call.
                </p>
              )}
              {entries.map(({ item, result }, position) => {
                const previous = entries[position - 1]
                const previousAt = previous ? new Date((previous.result ?? previous.item).occurredAt).getTime() : undefined
                const gap = previousAt === undefined ? 0 : (new Date(item.occurredAt).getTime() - previousAt) / 1000
                return (
                  <div key={`${item.occurredAt}-${item.kind}-${item.callId ?? position}`} className="contents">
                    {gap >= silenceSeconds && (
                      <p role="listitem" className="flex items-center gap-2 text-[0.6875rem] font-medium text-destructive">
                        <span className="h-px flex-1 bg-destructive/20" />
                        {Math.round(gap)}s silence · {formatOffset(item.occurredAt, detail.startedAt)}
                        <span className="h-px flex-1 bg-destructive/20" />
                      </p>
                    )}
                    <TimelineItem
                      item={item}
                      result={result}
                      selected={false}
                      showTiming
                      callStartedAt={detail.startedAt}
                      middlewareRequests={executions.get(item.callId ?? "")?.middlewareRequests}
                    />
                  </div>
                )
              })}
            </div>
          </MessageScrollerContent>
        </MessageScrollerViewport>
      </MessageScroller>
    </MessageScrollerProvider>
  )
}

function ScorecardPanel({
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
  const [dirty, setDirty] = useState(false)

  const applicable = shown.questions.filter((question) => question !== "time_offered" || answers.booking_requested === true)
  const complete = excluded || applicable.every((question) => answers[question] !== undefined)
  const judge = new Map(shown.judge.map((answer) => [answer.question, answer]))
  const revealed = shown.submitted && !dirty

  function answer(question: string, value: boolean) {
    setAnswers((current) => {
      const next = { ...current, [question]: value }
      if (question === "booking_requested" && !value) delete next.time_offered
      return next
    })
    setDirty(true)
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
      setError(outcome.failure.kind === "rejected" ? "Answer every question that applies, then save." : "The review could not be saved. Try again.")
      return
    }
    setShown(outcome.data)
    setDirty(false)
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
            onChange={(event) => {
              setExcluded(event.target.checked)
              setDirty(true)
            }}
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
                    <Button size="sm" variant={value === true ? "default" : "outline"} aria-pressed={value === true} onClick={() => answer(key, true)}>
                      Yes
                    </Button>
                    <Button size="sm" variant={value === false ? "destructive" : "outline"} aria-pressed={value === false} onClick={() => answer(key, false)}>
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
                      onChange={(event) => {
                        setNotes((current) => ({ ...current, [key]: event.target.value }))
                        setDirty(true)
                      }}
                    />
                  ) : (
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
            onChange={(event) => {
              setNote(event.target.value)
              setDirty(true)
            }}
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
            {excluded ? "Save as excluded" : shown.submitted ? "Save changes" : "Save and reveal judge"}
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
    return (
      <p className="text-xs text-muted-foreground">
        {question === "time_offered"
          ? "Judge didn't run: no availability was returned, so this counts as No."
          : "Judge: no answer recorded for this call."}
      </p>
    )
  }
  const agrees = human === judged.answer
  return (
    <p className="flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground">
      Judge:
      <span className="font-semibold text-foreground">{judged.answer ? "Yes" : "No"}</span>
      {judged.probability !== undefined && <span className="font-mono tabular-nums">({judged.probability.toFixed(2)})</span>}
      <Badge variant={agrees ? "secondary" : "destructive"}>{agrees ? "Agrees" : "Disagrees"}</Badge>
    </p>
  )
}

function JudgeAccuracyView({
  practiceID,
  onOpenCall,
}: {
  practiceID: string
  onOpenCall: (interactionID: string) => void
}) {
  const accuracy = useJudgeAccuracy(practiceID, 0)
  const catalog = useQuestionCatalog()
  if (accuracy.status === "loading") return <Skeleton className="h-72 rounded-xl" />
  if (accuracy.status === "failed") {
    return (
      <Alert variant="destructive" className="max-w-2xl">
        <CircleAlertIcon aria-hidden="true" />
        <AlertTitle>Judge accuracy unavailable</AlertTitle>
        <AlertDescription>
          <Button className="mt-2" size="sm" variant="outline" onClick={accuracy.retry}>Retry</Button>
        </AlertDescription>
      </Alert>
    )
  }
  const data = accuracy.data
  const label = (key: string) => {
    const question = catalog.get(key)
    return question ? `${question.code} ${question.label}` : key
  }
  const totals = data.rows.reduce((sum, row) => ({ sample: sum.sample + row.sample, agreed: sum.agreed + row.agreed }), { sample: 0, agreed: 0 })
  return (
    <div className="space-y-4">
      <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
        <Stat label="Reviewed calls" value={data.reviewedCalls.toLocaleString()} note="Excluding test calls" />
        <Stat label="Golden-set answers" value={totals.sample.toLocaleString()} note="With a judge answer to compare" />
        <Stat label="Agreement" value={totals.sample ? `${Math.round((100 * totals.agreed) / totals.sample)}%` : "—"} note={`${totals.agreed} of ${totals.sample}`} />
        <Stat label="Reviewer disagreements" value={data.reviewerDisagreements.length.toLocaleString()} note="Questions to reword" />
      </div>
      <Card size="sm">
        <CardHeader>
          <CardTitle className="text-sm">Per question and judge version</CardTitle>
          <CardDescription>
            Failures caught: the human said no and the judge said no. False alarm: the judge said no when the human said yes.
            {data.unjudged > 0 && ` ${data.unjudged} golden-set ${data.unjudged === 1 ? "answer has" : "answers have"} no judge answer to compare and ${data.unjudged === 1 ? "is" : "are"} left out.`}
          </CardDescription>
        </CardHeader>
        <CardContent className="overflow-x-auto px-0">
          <Table className="min-w-[44rem]">
            <TableHeader className="bg-muted text-[0.6875rem] text-muted-foreground">
              <TableRow className="hover:bg-transparent">
                <TableHead className="pl-4">Question</TableHead>
                <TableHead>Judge version</TableHead>
                <TableHead className="text-right">Agreement</TableHead>
                <TableHead className="text-right">Failures caught</TableHead>
                <TableHead className="text-right">False alarms</TableHead>
                <TableHead className="pr-4 text-right">Sample</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {data.rows.length === 0 && (
                <TableRow>
                  <TableCell colSpan={6} className="py-8 text-center text-sm text-muted-foreground">
                    No reviewed calls yet. Answers from the daily queue land here.
                  </TableCell>
                </TableRow>
              )}
              {data.rows.map((row) => (
                <TableRow key={`${row.question}:${row.judgeVersion}`}>
                  <TableCell className="pl-4 font-medium">{label(row.question)}</TableCell>
                  <TableCell className="font-mono text-xs">{row.judgeVersion || "—"}</TableCell>
                  <TableCell className="text-right tabular-nums">
                    {row.sample ? `${row.agreed}/${row.sample} · ${Math.round((100 * row.agreed) / row.sample)}%` : "—"}
                  </TableCell>
                  <TableCell className="text-right tabular-nums">{row.humanNo ? `${row.failuresCaught}/${row.humanNo}` : "–"}</TableCell>
                  <TableCell className={`text-right tabular-nums ${row.falseAlarms ? "text-destructive" : ""}`}>{row.falseAlarms}</TableCell>
                  <TableCell className="pr-4 text-right tabular-nums">{row.sample}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </CardContent>
      </Card>
      <Card size="sm">
        <CardHeader>
          <CardTitle className="text-sm">Human vs judge disagreements</CardTitle>
          <CardDescription>Each one is either a missed failure or a false alarm. Open a call to see why.</CardDescription>
        </CardHeader>
        <CardContent className="overflow-x-auto px-0">
          <Table className="min-w-[48rem]">
            <TableHeader className="bg-muted text-[0.6875rem] text-muted-foreground">
              <TableRow className="hover:bg-transparent">
                <TableHead className="pl-4">Call</TableHead>
                <TableHead>Question</TableHead>
                <TableHead>Human</TableHead>
                <TableHead>Judge</TableHead>
                <TableHead>Reviewer</TableHead>
                <TableHead className="pr-4">Note</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {data.judgeDisagreements.length === 0 && (
                <TableRow>
                  <TableCell colSpan={6} className="py-6 text-center text-sm text-muted-foreground">No disagreements.</TableCell>
                </TableRow>
              )}
              {data.judgeDisagreements.map((item) => (
                <TableRow key={`${item.interactionId}:${item.question}:${item.reviewerEmail}`} className="cursor-pointer hover:bg-muted" onClick={() => onOpenCall(item.interactionId)}>
                  <TableCell className="pl-4">
                    <button
                      type="button"
                      className="rounded-sm text-left text-xs font-medium outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
                      onClick={(event) => {
                        event.stopPropagation()
                        onOpenCall(item.interactionId)
                      }}
                    >
                      {formatShortDateTime(item.startedAt)}
                    </button>
                    <span className="block text-[0.6875rem] text-muted-foreground">{item.locationName}</span>
                  </TableCell>
                  <TableCell className="text-xs">{label(item.question)}</TableCell>
                  <TableCell><AnswerBadge value={item.human} /></TableCell>
                  <TableCell>
                    <AnswerBadge value={item.judge} />
                    {item.judgeProbability !== undefined && (
                      <span className="ml-1 font-mono text-[0.6875rem] text-muted-foreground">{item.judgeProbability.toFixed(2)}</span>
                    )}
                    <span className="block font-mono text-[0.625rem] text-muted-foreground">{item.judgeVersion}</span>
                  </TableCell>
                  <TableCell className="text-xs">{item.reviewerEmail}</TableCell>
                  <TableCell className="max-w-80 pr-4 text-xs whitespace-normal text-muted-foreground">{item.note || "—"}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </CardContent>
      </Card>
      <Card size="sm">
        <CardHeader>
          <CardTitle className="text-sm">Reviewer disagreements</CardTitle>
          <CardDescription>Two reviewers answered the same question differently: the question is unclear, so reword it.</CardDescription>
        </CardHeader>
        <CardContent>
          {data.reviewerDisagreements.length === 0 ? (
            <p className="text-sm text-muted-foreground">No disagreements between reviewers yet. Shared calls in the daily queue feed this.</p>
          ) : (
            <ul className="divide-y">
              {data.reviewerDisagreements.map((item) => (
                <li key={`${item.interactionId}:${item.question}`} className="flex flex-wrap items-start justify-between gap-3 py-2.5">
                  <button type="button" className="text-left text-xs" onClick={() => onOpenCall(item.interactionId)}>
                    <span className="font-medium">{label(item.question)}</span>
                    <span className="block text-muted-foreground">{formatShortDateTime(item.startedAt)} · {item.locationName}</span>
                  </button>
                  <span className="flex flex-wrap gap-2 text-xs">
                    {item.answers.map((answer) => (
                      <span key={answer.reviewerEmail} title={answer.note} className="flex items-center gap-1">
                        {answer.reviewerEmail} <AnswerBadge value={answer.answer} />
                      </span>
                    ))}
                  </span>
                </li>
              ))}
            </ul>
          )}
        </CardContent>
      </Card>
    </div>
  )
}

function AnswerBadge({ value }: { value: boolean }) {
  return <Badge variant={value ? "secondary" : "destructive"}>{value ? "Yes" : "No"}</Badge>
}

function Stat({ label, value, note }: { label: string; value: string; note: string }) {
  return (
    <Card size="sm" className="min-w-0">
      <CardHeader>
        <CardDescription>{label}</CardDescription>
        <CardTitle className="mt-1 font-mono text-xl font-semibold tracking-[-0.03em]">{value}</CardTitle>
      </CardHeader>
      <CardContent className="truncate text-[0.6875rem] text-muted-foreground">{note}</CardContent>
    </Card>
  )
}

function formatDuration(seconds: number) {
  return `${Math.floor(seconds / 60)}m ${Math.max(0, Math.round(seconds)) % 60}s`
}
