"use client"

import { useEffect, useEffectEvent, useState } from "react"
import {
  ChevronLeftIcon,
  ChevronRightIcon,
  CircleAlertIcon,
  InboxIcon,
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
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
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
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { ScorecardPanel } from "@/components/workspace/call-review-scorecard"
import {
  formatOffset,
  TimelineItem,
  timelineEntries,
} from "@/components/workspace/operator-analytics-detail"
import {
  ScorecardDefinitionsButton,
  useQuestionCatalog,
} from "@/components/workspace/scorecard-overview"
import type {
  OperatorCallReviewQueueCall,
  OperatorScorecardQuestion,
} from "@/lib/api/generated/types.gen"
import { useAiCallEvidence } from "@/lib/clients/agent-calls"
import {
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
  const state = call.completedAt ? "Reviewed" : "To review"
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
          call.completedAt ? "border-foreground bg-foreground" : "border-muted-foreground"
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

function versionLabel(version: string) {
  if (version === "typesafe-scorecard-v6") return "Current (v6)"
  if (version === "v1") return "Batch 1 (v1)"
  return version || "Unknown"
}

function JudgeAccuracyView({
  practiceID,
  onOpenCall,
}: {
  practiceID: string
  onOpenCall: (interactionID: string) => void
}) {
  const accuracy = useJudgeAccuracy(practiceID)
  const catalog = useQuestionCatalog()
  const [picked, setPicked] = useState("")
  const [goldenOpen, setGoldenOpen] = useState(false)
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
  const versions = Array.from(new Set(data.rows.map((row) => row.judgeVersion))).sort((left, right) => (left === "typesafe-scorecard-v6" ? -1 : right === "typesafe-scorecard-v6" ? 1 : left.localeCompare(right)))
  const version = versions.includes(picked) ? picked : versions[0] ?? ""
  const rows = data.rows.filter((row) => row.judgeVersion === version)
  const disagreements = data.judgeDisagreements.filter((item) => item.judgeVersion === version)
  const label = (key: string) => {
    const question = catalog.get(key)
    return question ? `${question.code} ${question.label}` : key
  }
  const totals = rows.reduce((sum, row) => ({ sample: sum.sample + row.sample, agreed: sum.agreed + row.agreed }), { sample: 0, agreed: 0 })
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="max-w-2xl text-sm text-muted-foreground">
          Can we trust the jury on each question? Daily-queue reviews are weighted by how many calls of each kind happened that day, so the flagged half of the queue does not flatter the jury. A question is trusted when, at 95% confidence, it catches at least 80% of the failures you mark, at most 20% of its no answers are false alarms, it agrees with you nearly as often as you and Chase agree with each other, and the last 14 days hold up.
        </p>
        {versions.length > 1 && (
          <ToggleGroup
            variant="segmented"
            spacing={1}
            value={[version]}
            aria-label="Judge version"
            onValueChange={(values) => values[0] && setPicked(values[0])}
          >
            {versions.map((item) => (
              <ToggleGroupItem key={item} value={item}>{versionLabel(item)}</ToggleGroupItem>
            ))}
          </ToggleGroup>
        )}
      </div>
      <div className="grid grid-cols-2 gap-3 md:grid-cols-3">
        <Stat label="Jury agrees with you" value={totals.sample ? `${Math.round((100 * totals.agreed) / totals.sample)}%` : "—"} note={`${totals.agreed} of ${totals.sample} answers, ${versionLabel(version)}`} />
        <button
          type="button"
          onClick={() => setGoldenOpen(true)}
          className="rounded-xl border bg-card px-4 py-3 text-left outline-none transition-colors hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring/40"
        >
          <span className="text-xs text-muted-foreground">Golden set</span>
          <span className="mt-1 block font-mono text-xl font-semibold tracking-[-0.03em]">{data.reviewedCalls.toLocaleString()} calls</span>
          <span className="mt-1 flex items-center gap-1 text-[0.6875rem] text-muted-foreground">
            {data.goldenAnswers.toLocaleString()} answers · See calls <ChevronRightIcon className="size-3" aria-hidden="true" />
          </span>
        </button>
        <Stat label="You and Chase disagreed" value={data.reviewerDisagreements.length.toLocaleString()} note="Questions to reword" />
      </div>
      <Card size="sm">
        <CardHeader>
          <CardTitle className="text-sm">By question</CardTitle>
          <CardDescription>
            Percentages are weighted estimates; the smaller figure is the 95% bound the bar is checked against.
          </CardDescription>
        </CardHeader>
        <CardContent className="overflow-x-auto px-0">
          <Table className="min-w-[48rem]">
            <TableHeader className="bg-muted text-[0.6875rem] text-muted-foreground">
              <TableRow className="hover:bg-transparent">
                <TableHead className="pl-4">Question</TableHead>
                <TableHead className="text-right">Catches failures</TableHead>
                <TableHead className="text-right">False alarms</TableHead>
                <TableHead className="text-right">Agrees with you</TableHead>
                <TableHead>Each juror</TableHead>
                <TableHead className="text-right">Reviewed</TableHead>
                <TableHead className="pr-4">Status</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.length === 0 && (
                <TableRow>
                  <TableCell colSpan={7} className="py-8 text-center text-sm text-muted-foreground">
                    No reviewed calls yet. Answers from the daily queue land here.
                  </TableCell>
                </TableRow>
              )}
              {rows.map((row) => {
                const trust = row.trust
                const pct = (value: number | null | undefined) => (value == null ? "—" : `${Math.round(100 * value)}%`)
                return (
                  <TableRow key={row.question}>
                    <TableCell className="pl-4 font-medium">{label(row.question)}</TableCell>
                    <TableCell className="text-right tabular-nums">
                      {pct(trust.catch)}
                      {trust.catchLow != null && <span className="block text-[0.6875rem] text-muted-foreground">≥ {pct(trust.catchLow)}</span>}
                    </TableCell>
                    <TableCell className="text-right tabular-nums">
                      {pct(trust.falseAlarms)}
                      {trust.falseAlarmHigh != null && <span className="block text-[0.6875rem] text-muted-foreground">≤ {pct(trust.falseAlarmHigh)}</span>}
                    </TableCell>
                    <TableCell className="text-right tabular-nums">
                      {pct(trust.agreement)}
                      <span className="block text-[0.6875rem] text-muted-foreground">
                        {trust.agreementLow != null ? `≥ ${pct(trust.agreementLow)}` : ""}
                        {trust.humanAgreement != null ? ` · you two ${pct(trust.humanAgreement)}` : ""}
                      </span>
                    </TableCell>
                    <TableCell className="text-xs text-muted-foreground">
                      {row.jurors.map((juror) => `${juror.model.split("/").pop()} ${juror.compared ? Math.round((100 * juror.agreed) / juror.compared) : 0}%`).join(" · ") || "—"}
                    </TableCell>
                    <TableCell className="text-right text-xs tabular-nums text-muted-foreground">
                      {trust.calls} calls
                      <span className="block">{trust.failures} failures · {trust.humanPairs} shared</span>
                    </TableCell>
                    <TableCell className="max-w-56 pr-4">
                      <Badge variant={trust.status === "trusted" ? "secondary" : trust.status === "below" ? "destructive" : "outline"}>
                        {trust.status === "trusted" ? "Trusted" : trust.status === "below" ? "Below bar" : "Collecting"}
                      </Badge>
                      <span className="mt-1 block text-[0.6875rem] leading-4 text-muted-foreground">{trust.reason}</span>
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        </CardContent>
      </Card>
      <Sheet open={goldenOpen} onOpenChange={setGoldenOpen}>
        <SheetContent className="h-full overflow-y-auto data-[side=right]:w-full data-[side=right]:sm:max-w-xl">
          <SheetHeader className="border-b px-5 py-4">
            <SheetTitle>Golden set</SheetTitle>
            <SheetDescription>
              {data.reviewedCalls} calls and {data.goldenAnswers} answers from your reviews. Open a call to see the transcript and the answers.
            </SheetDescription>
          </SheetHeader>
          <ul className="divide-y">
            {data.goldenSet.map((call) => (
              <li key={call.interactionId}>
                <button
                  type="button"
                  className="flex w-full items-start justify-between gap-3 px-5 py-3 text-left text-sm hover:bg-muted"
                  onClick={() => {
                    setGoldenOpen(false)
                    onOpenCall(call.interactionId)
                  }}
                >
                  <span className="min-w-0">
                    <span className="block font-medium">{formatShortDateTime(call.startedAt)} · {call.locationName}</span>
                    <span className="block truncate text-xs text-muted-foreground">{call.reviewers.join(", ")}</span>
                  </span>
                  <span className="shrink-0 text-right text-xs tabular-nums text-muted-foreground">
                    {call.answers} answers
                    {call.disagreements > 0 && <span className="block text-destructive">{call.disagreements} Jev disagreed</span>}
                  </span>
                </button>
              </li>
            ))}
          </ul>
        </SheetContent>
      </Sheet>
      <details className="group rounded-xl border bg-card">
        <summary className="cursor-pointer px-4 py-3 text-sm font-medium">
          Calls where Jev disagreed with you ({disagreements.length})
        </summary>
        <div className="overflow-x-auto border-t">
          <Table className="min-w-[44rem]">
            <TableHeader className="bg-muted text-[0.6875rem] text-muted-foreground">
              <TableRow className="hover:bg-transparent">
                <TableHead className="pl-4">Call</TableHead>
                <TableHead>Question</TableHead>
                <TableHead>You</TableHead>
                <TableHead>Jev</TableHead>
                <TableHead className="pr-4">Note</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {disagreements.map((item) => (
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
                    <span className="block text-[0.6875rem] text-muted-foreground">{item.locationName} · {item.reviewerEmail}</span>
                  </TableCell>
                  <TableCell className="text-xs">{label(item.question)}</TableCell>
                  <TableCell><AnswerBadge value={item.human} /></TableCell>
                  <TableCell><AnswerBadge value={item.judge} /></TableCell>
                  <TableCell className="max-w-80 pr-4 text-xs whitespace-normal text-muted-foreground">{item.note || "—"}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      </details>
      {data.reviewerDisagreements.length > 0 && (
        <details className="rounded-xl border bg-card">
          <summary className="cursor-pointer px-4 py-3 text-sm font-medium">
            Calls where you and Chase disagreed ({data.reviewerDisagreements.length})
          </summary>
          <ul className="divide-y border-t px-4">
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
        </details>
      )}
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
