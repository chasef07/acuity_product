"use client"

import { useState } from "react"
import { BookOpenTextIcon, ChevronRightIcon, CircleAlertIcon } from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import type {
  OperatorAiAnalyticsRange,
  OperatorBookingConversionWeek,
  OperatorScorecardQuestion,
} from "@/lib/api/generated/types.gen"
import { useScorecardQuestions, useScorecardReport, useScorecardResults } from "@/lib/clients/call-review"
import { formatShortDateTime } from "@/lib/format"

export function useQuestionCatalog() {
  const query = useScorecardQuestions()
  const questions = query.status === "ready" ? query.data.questions : []
  return new Map(questions.map((question) => [question.key, question]))
}

export function ScorecardDefinitions({ questions }: { questions: OperatorScorecardQuestion[] }) {
  return (
    <ul className="divide-y rounded-lg border bg-card">
      {questions.filter((question) => question.source === "judge").map((question) => (
        <li key={question.key}>
          <details className="group px-4 py-3">
            <summary className="flex cursor-pointer list-none items-start gap-2 text-sm [&::-webkit-details-marker]:hidden">
              <ChevronRightIcon className="mt-0.5 size-4 shrink-0 text-muted-foreground transition-transform group-open:rotate-90" aria-hidden="true" />
              <span>
                <span className="mr-1.5 font-mono text-xs text-muted-foreground">{question.code}</span>
                <span className="font-medium">{question.prompt}</span>
              </span>
            </summary>
            <div className="mt-2 space-y-2 pl-6 text-sm leading-6">
              <p className="text-muted-foreground">{question.question}</p>
              <dl className="grid gap-1 text-xs leading-5 sm:grid-cols-[2.5rem_1fr]">
                <dt className="font-medium">Yes</dt>
                <dd className="text-muted-foreground">{question.yes}</dd>
                <dt className="font-medium">No</dt>
                <dd className="text-muted-foreground">{question.no}</dd>
              </dl>
            </div>
          </details>
        </li>
      ))}
    </ul>
  )
}

export function ScorecardDefinitionsButton() {
  const [open, setOpen] = useState(false)
  const query = useScorecardQuestions()
  return (
    <>
      <Button size="sm" variant="outline" onClick={() => setOpen(true)}>
        <BookOpenTextIcon aria-hidden="true" />
        Definitions
      </Button>
      <Sheet open={open} onOpenChange={setOpen}>
        <SheetContent className="h-full overflow-y-auto data-[side=right]:w-full data-[side=right]:sm:max-w-xl">
          <SheetHeader className="border-b px-5 py-4">
            <SheetTitle>Question definitions</SheetTitle>
            <SheetDescription>What Jev reads for each question, and what counts as yes or no.</SheetDescription>
          </SheetHeader>
          <div className="px-5 py-5">
            {query.status === "ready" ? (
              <ScorecardDefinitions questions={query.data.questions} />
            ) : query.status === "loading" ? (
              <Skeleton className="h-64 rounded-lg" />
            ) : (
              <p className="text-sm text-destructive">Definitions could not be loaded.</p>
            )}
          </div>
        </SheetContent>
      </Sheet>
    </>
  )
}

function ReportFailure({ title, retry }: { title: string; retry: () => void }) {
  return (
    <Alert variant="destructive">
      <CircleAlertIcon aria-hidden="true" />
      <AlertTitle>{title}</AlertTitle>
      <AlertDescription>
        <Button className="mt-2" size="sm" variant="outline" onClick={retry}>Retry</Button>
      </AlertDescription>
    </Alert>
  )
}

export function ScorecardHighlights({ practiceID, locationID }: { practiceID: string; locationID: string }) {
  const report = useScorecardReport(practiceID, locationID, 4)
  const [insuranceOpen, setInsuranceOpen] = useState(false)
  if (report.status === "loading") return <Skeleton className="h-56 rounded-xl" />
  if (report.status === "failed") return <ReportFailure title="Booking conversion unavailable" retry={report.retry} />
  const { weeks, unverifiedInsurance } = report.data
  const plans = new Set(unverifiedInsurance.map((plan) => plan.plan.toLowerCase())).size
  const insuranceCalls = unverifiedInsurance.reduce((sum, plan) => sum + plan.calls, 0)
  return (
    <div className="grid gap-3 lg:grid-cols-[minmax(0,2fr)_minmax(16rem,1fr)]">
      <Card size="sm" className="min-w-0">
        <CardHeader>
          <CardTitle className="text-sm">Booking conversion</CardTitle>
          <CardDescription>
            Converted ÷ (booking calls − blocked), by office and week. Weeks start Monday.
          </CardDescription>
        </CardHeader>
        <CardContent className="overflow-x-auto px-0">
          <Table className="min-w-[36rem]">
            <TableHeader className="bg-muted text-[0.6875rem] text-muted-foreground">
              <TableRow className="hover:bg-transparent">
                <TableHead className="pl-4">Week of</TableHead>
                <TableHead>Office</TableHead>
                <TableHead className="text-right">Booking calls</TableHead>
                <TableHead className="text-right">Converted</TableHead>
                <TableHead className="text-right">Missed</TableHead>
                <TableHead className="text-right">Blocked</TableHead>
                <TableHead className="pr-4 text-right">Conversion</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {weeks.length === 0 && (
                <TableRow>
                  <TableCell colSpan={7} className="py-8 text-center text-sm text-muted-foreground">
                    No scored calls in the last 4 weeks.
                  </TableCell>
                </TableRow>
              )}
              {weeks.map((week) => <ConversionRow key={`${week.weekStart}:${week.locationId}`} week={week} />)}
            </TableBody>
          </Table>
        </CardContent>
      </Card>
      <button
        type="button"
        onClick={() => setInsuranceOpen(true)}
        className="rounded-xl border bg-card p-4 text-left outline-none transition-colors hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring/40"
      >
        <span className="text-sm font-medium">Insurance plans we couldn&apos;t verify</span>
        <span className="mt-2 block font-mono text-2xl font-semibold tracking-[-0.03em]">{plans}</span>
        <span className="mt-1 block text-xs text-muted-foreground">
          {plans === 1 ? "plan" : "plans"} across {insuranceCalls} {insuranceCalls === 1 ? "call" : "calls"}, last 4 weeks
        </span>
        <span className="mt-3 inline-flex items-center gap-1 text-xs font-medium">
          See plans <ChevronRightIcon className="size-3.5" aria-hidden="true" />
        </span>
      </button>
      <Sheet open={insuranceOpen} onOpenChange={setInsuranceOpen}>
        <SheetContent className="h-full overflow-y-auto data-[side=right]:w-full data-[side=right]:sm:max-w-2xl">
          <SheetHeader className="border-b px-5 py-4">
            <SheetTitle>Insurance plans we couldn&apos;t verify</SheetTitle>
            <SheetDescription>
              The last insurance check ended blocked or still needed details. Use these to update each office&apos;s insurance rules.
            </SheetDescription>
          </SheetHeader>
          <div className="overflow-x-auto">
            <Table className="min-w-[36rem]">
              <TableHeader className="bg-muted text-[0.6875rem] text-muted-foreground">
                <TableRow className="hover:bg-transparent">
                  <TableHead className="pl-5">Plan as the caller said it</TableHead>
                  <TableHead>Office</TableHead>
                  <TableHead>What the check said</TableHead>
                  <TableHead className="pr-5 text-right">Calls</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {unverifiedInsurance.length === 0 && (
                  <TableRow>
                    <TableCell colSpan={4} className="py-8 text-center text-sm text-muted-foreground">Every insurance check was verified.</TableCell>
                  </TableRow>
                )}
                {unverifiedInsurance.map((plan) => (
                  <TableRow key={`${plan.weekStart}:${plan.locationId}:${plan.plan}:${plan.result}`}>
                    <TableCell className="pl-5 font-medium">
                      {plan.plan}
                      <span className="block text-[0.6875rem] font-normal text-muted-foreground">Week of {plan.weekStart}</span>
                    </TableCell>
                    <TableCell>{plan.locationName}</TableCell>
                    <TableCell className="max-w-72 text-xs whitespace-normal text-muted-foreground">{plan.result || "—"}</TableCell>
                    <TableCell className="pr-5 text-right tabular-nums">{plan.calls}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        </SheetContent>
      </Sheet>
    </div>
  )
}

function ConversionRow({ week }: { week: OperatorBookingConversionWeek }) {
  const notJudged = week.judgedCalls === 0
  const dash = (value: number) => (notJudged ? "—" : value)
  return (
    <TableRow>
      <TableCell className="pl-4 tabular-nums">{week.weekStart}</TableCell>
      <TableCell>{week.locationName}</TableCell>
      <TableCell className="text-right tabular-nums">{dash(week.bookingCalls)}</TableCell>
      <TableCell className="text-right tabular-nums">{dash(week.converted)}</TableCell>
      <TableCell className={`text-right tabular-nums ${week.missed > 0 ? "text-destructive" : ""}`}>{dash(week.missed)}</TableCell>
      <TableCell className="text-right tabular-nums">{dash(week.blocked)}</TableCell>
      <TableCell className="pr-4 text-right font-mono font-semibold tabular-nums">
        {week.conversion == null ? "—" : `${Math.round(week.conversion * 100)}%`}
      </TableCell>
    </TableRow>
  )
}

export function ScorecardResultsCard({
  practiceID,
  locationID,
  range,
  onOpenCall,
}: {
  practiceID: string
  locationID: string
  range: OperatorAiAnalyticsRange
  onOpenCall: (interactionID: string) => void
}) {
  const results = useScorecardResults(practiceID, locationID, range)
  const catalog = useQuestionCatalog()
  const [open, setOpen] = useState("")
  if (results.status === "loading") return <Skeleton className="h-64 rounded-xl" />
  if (results.status === "failed") return <ReportFailure title="Scores unavailable" retry={results.retry} />
  const { calls, rows, noCalls, truncated } = results.data
  return (
    <Card size="sm" className="min-w-0">
      <CardHeader>
        <CardTitle className="text-sm">Scores on every call</CardTitle>
        <CardDescription>
          How many of {calls.toLocaleString()} scored calls got a no on each question. Click a question to see those calls.
        </CardDescription>
      </CardHeader>
      <CardContent className="px-0">
        <ul className="divide-y border-t">
          {rows.length === 0 && <li className="px-4 py-8 text-center text-sm text-muted-foreground">No scored calls in this range.</li>}
          {rows.map((row) => {
            const question = catalog.get(row.question)
            const share = row.answered ? row.no / row.answered : 0
            const expanded = open === row.question
            const list = noCalls.filter((call) => call.question === row.question)
            return (
              <li key={row.question}>
                <button
                  type="button"
                  aria-expanded={expanded}
                  disabled={row.no === 0}
                  onClick={() => setOpen(expanded ? "" : row.question)}
                  className="grid w-full grid-cols-[1fr_auto] items-center gap-3 px-4 py-2.5 text-left text-sm outline-none hover:bg-muted focus-visible:bg-muted disabled:hover:bg-transparent"
                >
                  <span className="min-w-0">
                    <span className="mr-1.5 font-mono text-xs text-muted-foreground">{question?.code}</span>
                    {question?.prompt ?? row.question}
                    <span className="ml-2 inline-block h-1.5 w-24 overflow-hidden rounded-full bg-muted align-middle">
                      <span className="block h-full rounded-full bg-destructive/70" style={{ width: `${Math.round(share * 100)}%` }} />
                    </span>
                  </span>
                  <span className="flex items-center gap-2 text-xs tabular-nums text-muted-foreground">
                    <span className={row.no > 0 ? "font-semibold text-foreground" : ""}>{row.no} no</span>
                    <span>of {row.answered}</span>
                    {row.no > 0 && <ChevronRightIcon className={`size-3.5 transition-transform ${expanded ? "rotate-90" : ""}`} aria-hidden="true" />}
                  </span>
                </button>
                {expanded && (
                  <ul className="bg-muted/40 px-4 py-2">
                    {list.map((call) => (
                      <li key={call.interactionId}>
                        <button type="button" className="flex w-full justify-between gap-3 rounded-sm py-1.5 text-left text-xs hover:underline" onClick={() => onOpenCall(call.interactionId)}>
                          <span>{formatShortDateTime(call.startedAt)}</span>
                          <span className="text-muted-foreground">{call.locationName}</span>
                        </button>
                      </li>
                    ))}
                    {truncated && list.length < row.no && (
                      <li className="py-1.5 text-xs text-muted-foreground">Showing the first {list.length}. Narrow the range or office to see the rest.</li>
                    )}
                  </ul>
                )}
              </li>
            )
          })}
        </ul>
      </CardContent>
    </Card>
  )
}
