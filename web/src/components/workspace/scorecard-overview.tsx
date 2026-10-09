"use client"

import { useState } from "react"
import { BookOpenTextIcon, ChevronRightIcon, CircleAlertIcon, TrendingUpIcon } from "lucide-react"
import { CartesianGrid, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts"

import styles from "@/components/analytics/ai-diagnostics.module.css"
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
import type {
  OperatorAiAnalyticsRange,
  OperatorScorecardQuestion,
} from "@/lib/api/generated/types.gen"
import { useScorecardQuestions, useScorecardResults } from "@/lib/clients/call-review"

export function useQuestionCatalog() {
  const query = useScorecardQuestions()
  const questions = query.status === "ready" ? query.data.questions : []
  return new Map(questions.map((question) => [question.key, question]))
}

function ScorecardDefinitions({ questions }: { questions: OperatorScorecardQuestion[] }) {
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

export function BookingConversionKpi({ practiceID, range }: { practiceID: string; range: OperatorAiAnalyticsRange }) {
  const results = useScorecardResults(practiceID, "", range)
  const data = results.status === "ready" ? results.data : undefined
  return (
    <Card size="sm" className="min-w-0">
      <CardHeader className="grid-cols-[1fr_auto]">
        <div>
          <CardDescription>Booking conversion</CardDescription>
          <CardTitle className="mt-1 font-mono text-xl font-semibold tracking-[-0.03em] sm:text-2xl">
            {results.status === "loading" ? <Skeleton className="h-7 w-14" /> : data?.conversion == null ? "—" : `${Math.round(data.conversion * 100)}%`}
          </CardTitle>
        </div>
        <span className="flex size-7 items-center justify-center rounded-md bg-muted text-muted-foreground">
          <TrendingUpIcon className="size-3.5" aria-hidden="true" />
        </span>
      </CardHeader>
      <CardContent className="truncate text-[0.6875rem] text-muted-foreground">
        {results.status === "failed"
          ? "Unavailable"
          : data
            ? `${data.converted} of ${data.bookingCalls - data.blocked} booking calls · ${data.missed} missed`
            : "Loading"}
      </CardContent>
    </Card>
  )
}

export function ImprovementTrend({ practiceID, locationID, range }: { practiceID: string; locationID: string; range: OperatorAiAnalyticsRange }) {
  const results = useScorecardResults(practiceID, locationID, range)
  if (results.status === "loading") return <Skeleton className="h-72 rounded-xl" />
  if (results.status === "failed") {
    return (
      <Alert variant="destructive">
        <CircleAlertIcon aria-hidden="true" />
        <AlertTitle>Trend unavailable</AlertTitle>
        <AlertDescription>
          <Button className="mt-2" size="sm" variant="outline" onClick={results.retry}>Retry</Button>
        </AlertDescription>
      </Alert>
    )
  }
  const data = results.data
  const share = data.calls ? data.problemCalls / data.calls : undefined
  return (
    <section aria-label="Are we improving?" className={styles.callTrend}>
      <div className={styles.sectionHeading}>
        <h2>Are we improving?</h2>
        <span>Share of scored calls with a problem, per day. Lower is better.</span>
      </div>
      <strong className={styles.secondaryHeadline}>{share === undefined ? "—" : `${(share * 100).toFixed(1)}%`}</strong>
      <p className={styles.caption}>
        {data.problemCalls.toLocaleString()} of {data.calls.toLocaleString()} calls had a no on a failure question or a missed booking
      </p>
      <div className={styles.trend} role="img" aria-label="Calls with a problem per day, UTC">
        <ResponsiveContainer width="100%" height="100%">
          <LineChart data={data.daily} margin={{ top: 24, right: 12, bottom: 0, left: 0 }}>
            <CartesianGrid vertical={false} stroke="var(--border)" strokeDasharray="3 4" />
            <XAxis dataKey="date" tickFormatter={(date: string) => date.slice(5).replace("-", "/")} tickLine={false} axisLine={false} minTickGap={30} tick={{ fontSize: 10, fill: "var(--muted-foreground)" }} />
            <YAxis domain={[0, 1]} tickFormatter={(value: number) => `${Math.round(value * 100)}%`} width={44} tickLine={false} axisLine={false} tick={{ fontSize: 10, fill: "var(--muted-foreground)" }} />
            <Tooltip
              content={({ active, payload }) => {
                const day = payload?.[0]?.payload as { date: string; calls: number; problemCalls: number; problemRate: number | null } | undefined
                if (!active || !day) return null
                return (
                  <div className={styles.trendTooltip}>
                    <strong>{day.date} · UTC</strong>
                    <p>{day.calls ? `${day.problemCalls} of ${day.calls} calls · ${((day.problemRate ?? 0) * 100).toFixed(1)}%` : "No scored calls"}</p>
                  </div>
                )
              }}
            />
            <Line dataKey="problemRate" name="Calls with a problem" type="linear" stroke="var(--chart-3)" strokeWidth={2} dot={{ r: 3 }} activeDot={{ r: 5 }} connectNulls={false} isAnimationActive={false} />
          </LineChart>
        </ResponsiveContainer>
      </div>
      <p className={styles.footnote}>UTC call date · First and last days may be partial. Gaps mean no scored calls.</p>
    </section>
  )
}
