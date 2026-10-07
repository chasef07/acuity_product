"use client"

import { useState } from "react"
import { BookOpenTextIcon, CircleAlertIcon } from "lucide-react"

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
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import type {
  OperatorBookingConversionWeek,
  OperatorScorecardQuestion,
} from "@/lib/api/generated/types.gen"
import { useScorecardQuestions, useScorecardReport } from "@/lib/clients/call-review"

export function useQuestionCatalog() {
  const query = useScorecardQuestions()
  const questions = query.status === "ready" ? query.data.questions : []
  return new Map(questions.map((question) => [question.key, question]))
}

export function questionSummary(question?: OperatorScorecardQuestion) {
  if (!question) return ""
  const [first] = question.question.split(/(?<=[?.])\s/)
  return first
}

export function ScorecardDefinitions({ questions }: { questions: OperatorScorecardQuestion[] }) {
  const groups = Array.from(new Set(questions.map((question) => question.group)))
  return (
    <div className="space-y-6">
      {groups.map((group) => (
        <section key={group} aria-label={group}>
          <h3 className="text-[0.6875rem] font-medium tracking-wide text-muted-foreground uppercase">{group}</h3>
          <dl className="mt-2 divide-y rounded-lg border bg-card">
            {questions.filter((question) => question.group === group).map((question) => (
              <div key={question.key} className="space-y-2 px-4 py-3">
                <dt className="flex flex-wrap items-center gap-2">
                  <span className="font-mono text-xs text-muted-foreground">{question.code}</span>
                  <span className="text-sm font-semibold">{question.label}</span>
                  <Badge variant={question.source === "judge" ? "secondary" : "outline"}>
                    {question.source === "judge" ? "Judge" : "Code check"}
                  </Badge>
                </dt>
                <dd className="text-sm leading-6">{question.question}</dd>
                <dd className="grid gap-1 text-xs leading-5 sm:grid-cols-[3rem_1fr]">
                  <span className="font-medium text-foreground">Yes</span>
                  <span className="text-muted-foreground">{question.yes}</span>
                  <span className="font-medium text-foreground">No</span>
                  <span className="text-muted-foreground">{question.no}</span>
                  <span className="font-medium text-foreground">Applies</span>
                  <span className="text-muted-foreground">{question.appliesWhen}</span>
                </dd>
              </div>
            ))}
          </dl>
        </section>
      ))}
    </div>
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
            <SheetTitle>Scorecard definitions</SheetTitle>
            <SheetDescription>
              Every answer is yes or no. The judge wording below is exactly what the judge reads.
            </SheetDescription>
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

const weekOptions = [4, 8, 12] as const

export function ScorecardOverview({ practiceID, locationID }: { practiceID: string; locationID: string }) {
  const [weeks, setWeeks] = useState<(typeof weekOptions)[number]>(4)
  const report = useScorecardReport(practiceID, locationID, weeks)
  const questions = useScorecardQuestions()
  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="max-w-2xl text-xs leading-5 text-muted-foreground">
          Conversion is converted ÷ (booking calls − blocked). A booking call is one where the judge says the caller asked to
          book, reschedule, or cancel (B1), so weeks before the judge ran show no booking calls.
        </p>
        <ToggleGroup
          variant="segmented"
          spacing={1}
          value={[String(weeks)]}
          aria-label="Weeks shown"
          onValueChange={(values) => {
            const value = Number(values[0])
            if (weekOptions.some((option) => option === value)) setWeeks(value as (typeof weekOptions)[number])
          }}
        >
          {weekOptions.map((option) => (
            <ToggleGroupItem key={option} value={String(option)}>{option} weeks</ToggleGroupItem>
          ))}
        </ToggleGroup>
      </div>

      {report.status === "loading" && <Skeleton className="h-72 rounded-xl" />}
      {report.status === "failed" && (
        <Alert variant="destructive" className="max-w-2xl">
          <CircleAlertIcon aria-hidden="true" />
          <AlertTitle>Scorecard unavailable</AlertTitle>
          <AlertDescription>
            <p>Booking conversion could not be loaded.</p>
            <Button className="mt-3" size="sm" variant="outline" onClick={report.retry}>Retry</Button>
          </AlertDescription>
        </Alert>
      )}
      {report.status === "ready" && (
        <>
          <Card size="sm">
            <CardHeader>
              <CardTitle className="text-sm">Booking conversion by office and week</CardTitle>
              <CardDescription>
                {report.data.from} to {report.data.through} · weeks start Monday ({report.data.timeZone})
              </CardDescription>
            </CardHeader>
            <CardContent className="overflow-x-auto px-0">
              <Table className="min-w-[46rem]">
                <TableHeader className="bg-muted text-[0.6875rem] text-muted-foreground">
                  <TableRow className="hover:bg-transparent">
                    <TableHead className="pl-4">Week of</TableHead>
                    <TableHead>Office</TableHead>
                    <TableHead className="text-right">Booking calls</TableHead>
                    <TableHead className="text-right">Converted</TableHead>
                    <TableHead className="text-right">Missed</TableHead>
                    <TableHead className="text-right">Blocked</TableHead>
                    <TableHead className="text-right">Offered, not booked</TableHead>
                    <TableHead className="text-right">Conversion</TableHead>
                    <TableHead className="pr-4 text-right">Judged calls</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {report.data.weeks.length === 0 && (
                    <TableRow>
                      <TableCell colSpan={9} className="py-8 text-center text-sm text-muted-foreground">
                        No scored calls in this range.
                      </TableCell>
                    </TableRow>
                  )}
                  {report.data.weeks.map((week) => (
                    <ConversionRow key={`${week.weekStart}:${week.locationId}`} week={week} />
                  ))}
                </TableBody>
              </Table>
            </CardContent>
          </Card>

          <Card size="sm">
            <CardHeader>
              <CardTitle className="text-sm">Insurance plans the agent couldn&apos;t verify</CardTitle>
              <CardDescription>
                The last insurance check ended blocked or still needed information (I1). Not an agent failure: use these to update
                each office&apos;s insurance rules.
              </CardDescription>
            </CardHeader>
            <CardContent className="overflow-x-auto px-0">
              <Table className="min-w-[40rem]">
                <TableHeader className="bg-muted text-[0.6875rem] text-muted-foreground">
                  <TableRow className="hover:bg-transparent">
                    <TableHead className="pl-4">Week of</TableHead>
                    <TableHead>Office</TableHead>
                    <TableHead>Plan as the caller said it</TableHead>
                    <TableHead>What the insurance check said</TableHead>
                    <TableHead className="pr-4 text-right">Calls</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {report.data.unverifiedInsurance.length === 0 && (
                    <TableRow>
                      <TableCell colSpan={5} className="py-8 text-center text-sm text-muted-foreground">
                        Every insurance check in this range was verified.
                      </TableCell>
                    </TableRow>
                  )}
                  {report.data.unverifiedInsurance.map((plan) => (
                    <TableRow key={`${plan.weekStart}:${plan.locationId}:${plan.plan}:${plan.result}`}>
                      <TableCell className="pl-4 tabular-nums">{plan.weekStart}</TableCell>
                      <TableCell>{plan.locationName}</TableCell>
                      <TableCell className="font-medium">{plan.plan}</TableCell>
                      <TableCell className="max-w-96 text-xs whitespace-normal text-muted-foreground">{plan.result || "—"}</TableCell>
                      <TableCell className="pr-4 text-right tabular-nums">{plan.calls}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </CardContent>
          </Card>
        </>
      )}

      <Card size="sm">
        <CardHeader>
          <CardTitle className="text-sm">Question definitions</CardTitle>
          <CardDescription>What counts as yes and no for every scorecard question.</CardDescription>
        </CardHeader>
        <CardContent>
          {questions.status === "ready" ? (
            <ScorecardDefinitions questions={questions.data.questions} />
          ) : (
            <Skeleton className="h-40 rounded-lg" />
          )}
        </CardContent>
      </Card>
    </div>
  )
}

function ConversionRow({ week }: { week: OperatorBookingConversionWeek }) {
  const notJudged = week.judgedCalls === 0
  return (
    <TableRow>
      <TableCell className="pl-4 tabular-nums">{week.weekStart}</TableCell>
      <TableCell>{week.locationName}</TableCell>
      <TableCell className="text-right tabular-nums">{notJudged ? "—" : week.bookingCalls}</TableCell>
      <TableCell className="text-right tabular-nums">{notJudged ? "—" : week.converted}</TableCell>
      <TableCell className={`text-right tabular-nums ${week.missed > 0 ? "text-destructive" : ""}`}>{notJudged ? "—" : week.missed}</TableCell>
      <TableCell className="text-right tabular-nums">{notJudged ? "—" : week.blocked}</TableCell>
      <TableCell className="text-right tabular-nums">{notJudged ? "—" : week.attempted}</TableCell>
      <TableCell className="text-right font-mono font-semibold tabular-nums">
        {week.conversion == null ? "—" : `${Math.round(week.conversion * 100)}%`}
      </TableCell>
      <TableCell className="pr-4 text-right text-muted-foreground tabular-nums">
        {week.judgedCalls} / {week.calls}
      </TableCell>
    </TableRow>
  )
}
