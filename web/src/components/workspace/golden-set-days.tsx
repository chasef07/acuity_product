import { ChevronRightIcon } from "lucide-react"

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import type { OperatorGoldenSetCounts, OperatorGoldenSetDay } from "@/lib/api/generated/types.gen"

const columns = "grid grid-cols-[minmax(7rem,1fr)_minmax(9rem,1fr)_minmax(9rem,1fr)] items-center gap-3 px-4 py-2"

function Counts({ counts }: { counts: OperatorGoldenSetCounts }) {
  return (
    <>
      <span className="text-right font-mono">{counts.falseAlarms} of {counts.judgeNo}</span>
      <span className="text-right font-mono">{counts.misses} of {counts.humanNo}</span>
    </>
  )
}

export function GoldenSetDays({
  days,
  week,
  label,
}: {
  days: OperatorGoldenSetDay[]
  week: OperatorGoldenSetCounts
  label: (question: string) => string
}) {
  return (
    <Card size="sm">
      <CardHeader>
        <CardTitle className="text-sm">By day</CardTitle>
        <CardDescription>
          Reviewed answers for the current judge version. False alarms: jury no answers reviewers marked fine. Misses: real issues reviewers marked no that the jury said yes to.
        </CardDescription>
      </CardHeader>
      <CardContent className="overflow-x-auto px-0 text-xs">
        <div className="min-w-[30rem]">
          <div className={`${columns} bg-muted text-[0.6875rem] text-muted-foreground`}>
            <span>Review day</span>
            <span className="text-right">False alarms of jury no&apos;s</span>
            <span className="text-right">Misses of real issues</span>
          </div>
          {days.length === 0 && <p className="px-4 py-3 text-muted-foreground">No reviews in the last 7 days.</p>}
          {days.map((day) => (
            <details key={day.date} className="group border-b">
              <summary className={`${columns} cursor-pointer list-none hover:bg-muted [&::-webkit-details-marker]:hidden`}>
                <span className="flex items-center gap-1">
                  <ChevronRightIcon className="size-3 transition-transform group-open:rotate-90" aria-hidden="true" />
                  {day.date}
                </span>
                <Counts counts={day} />
              </summary>
              {day.questions.map((question) => (
                <div key={question.question} className={`${columns} text-muted-foreground`}>
                  <span className="pl-4">{label(question.question)}</span>
                  <Counts counts={question} />
                </div>
              ))}
            </details>
          ))}
          <div className={`${columns} font-medium`}>
            <span>Last 7 days</span>
            <Counts counts={week} />
          </div>
        </div>
      </CardContent>
    </Card>
  )
}
