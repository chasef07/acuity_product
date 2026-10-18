"use client"

import { useState } from "react"
import { ChevronRightIcon, CircleAlertIcon } from "lucide-react"
import type { OperatorAiAnalyticsSummary } from "@/lib/api/generated/types.gen"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { OptionSelect } from "./manage-agent-page"

type JudgeReport = NonNullable<OperatorAiAnalyticsSummary["judges"]>

function percent(value: number, total: number) {
  return total ? (value / total).toLocaleString(undefined, { style: "percent", maximumFractionDigits: 1 }) : "—"
}

export function OperatorJudges({ report, onRetry }: { report?: JudgeReport; onRetry: () => void }) {
  const [question, setQuestion] = useState("all")
  if (!report) return <Alert><CircleAlertIcon aria-hidden="true" /><AlertTitle>Judges unavailable</AlertTitle><AlertDescription><p>Judge evidence could not be loaded.</p><Button variant="outline" size="sm" onClick={onRetry}>Retry</Button></AlertDescription></Alert>
  const selected = report.questions.find((item) => item.key === question)
  const questions = selected ? [selected] : report.questions
  const pairs = selected ? selected.pairs : report.pairs
  const quorumMet = questions.reduce((sum, item) => sum + item.quorumMet, 0)
  const noQuorum = questions.reduce((sum, item) => sum + item.noQuorum, 0)
  const unavailable = questions.reduce((sum, item) => sum + item.unavailable, 0)
  const skipped = questions.reduce((sum, item) => sum + item.unknown + item.notApplicable, 0)
  return (
    <div className="flex flex-col gap-5">
      <Card role="region" aria-label="Observed jury">
        <CardHeader>
          <CardTitle>Observed jury</CardTitle>
          <CardDescription>{report.version} · {report.reportedCalls.toLocaleString()} of {report.totalCalls.toLocaleString()} calls have a usable current-version report. {report.unavailableCalls.toLocaleString()} missing, malformed, or other-version reports.</CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          {report.lineups.length ? report.lineups.map((lineup) => <div key={lineup.models.join(",")} className="flex flex-wrap items-center justify-between gap-3">
            <div className="flex flex-wrap gap-2">{lineup.models.map((model) => <Badge key={model} variant="secondary">{model}</Badge>)}</div>
            <p className="text-xs text-muted-foreground">{lineup.requiredVotes} of {lineup.models.length} valid votes required · {lineup.calls.toLocaleString()} calls</p>
          </div>) : <p className="text-sm text-muted-foreground">No model lineup recorded in this selection.</p>}
          <p className="text-xs leading-relaxed text-muted-foreground">Each model votes Yes above {report.yesAbove.toLocaleString(undefined, { style: "percent" })}; otherwise No. Quorum requires a majority of the recorded lineup to respond. The majority of valid votes decides the verdict; ties use the mean probability at the same threshold. Model names reflect these recorded calls.</p>
        </CardContent>
      </Card>
      <Card role="region" aria-label="Model agreement">
        <CardHeader className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
          <div className="flex min-w-0 flex-col gap-1"><CardTitle>Who agrees with whom</CardTitle><CardDescription>Compare Yes / No votes on the same call and judge. Missing votes are excluded.</CardDescription></div>
          <CardAction className="w-full min-w-0 sm:w-auto [&_[data-slot=select-trigger]]:w-full [&_[data-slot=select-trigger]]:min-w-0"><OptionSelect label="Judge" value={question} items={[{ value: "all", label: "All judges" }, ...report.questions.map((item) => ({ value: item.key, label: item.label }))]} onChange={setQuestion} /></CardAction>
        </CardHeader>
        <CardContent className="flex flex-col gap-5">
          <dl className="grid grid-cols-2 gap-4 sm:grid-cols-4">
            {[{ label: "Quorum reached", value: quorumMet }, { label: "No quorum", value: noQuorum }, { label: "Unknown / not applicable", value: skipped }, { label: "Unavailable", value: unavailable }].map((item) => <div key={item.label}><dt className="text-xs text-muted-foreground">{item.label}</dt><dd className="mt-1 text-xl font-medium tabular-nums">{item.value.toLocaleString()}</dd></div>)}
          </dl>
          <p className="text-[11px] text-muted-foreground">Counts are call–judge judgments, not unique calls.</p>
          {pairs.length ? <Table>
            <TableHeader><TableRow><TableHead>Model</TableHead><TableHead>Compared with</TableHead><TableHead className="text-right">Shared judgments</TableHead><TableHead className="text-right">Agreed</TableHead><TableHead className="text-right">Disagreed</TableHead></TableRow></TableHeader>
            <TableBody>{pairs.map((pair) => <TableRow key={`${pair.left}:${pair.right}`}>
              <TableCell className="whitespace-normal">{pair.left}</TableCell><TableCell className="whitespace-normal">{pair.right}</TableCell>
              <TableCell className="text-right tabular-nums">{pair.compared.toLocaleString()}</TableCell>
              <TableCell className="text-right tabular-nums">{pair.agreed.toLocaleString()} <span className="text-muted-foreground">· {percent(pair.agreed, pair.compared)}</span></TableCell>
              <TableCell className="text-right tabular-nums">{pair.disagreed.toLocaleString()} <span className="text-muted-foreground">· {percent(pair.disagreed, pair.compared)}</span></TableCell>
            </TableRow>)}</TableBody>
          </Table> : <Empty><EmptyHeader><EmptyTitle>No paired votes yet</EmptyTitle><EmptyDescription>Two models need valid votes on the same call and judge to compare them.</EmptyDescription></EmptyHeader></Empty>}
          <p className="text-[11px] text-muted-foreground">Agreement measures consistency, not correctness. Compare against human reviews in Review. Sentiment scores are excluded from this Yes / No comparison.</p>
        </CardContent>
      </Card>
      <div className="grid items-start gap-5 xl:grid-cols-[minmax(0,1.2fr)_minmax(0,0.8fr)]">
        <Card role="region" aria-label="Judge criteria">
          <CardHeader><CardTitle>Criteria</CardTitle><CardDescription>What each judge checks and what Yes or No means.</CardDescription></CardHeader>
          <CardContent><div className="divide-y">{questions.map((item) => <details key={item.key} className="group py-3" open={Boolean(selected)}>
            <summary className="flex cursor-pointer list-none items-center gap-2 font-medium [&::-webkit-details-marker]:hidden"><ChevronRightIcon className="size-3.5 shrink-0 transition-transform group-open:rotate-90" aria-hidden="true" />{item.label}</summary>
            <div className="mt-3 flex flex-col gap-3 pl-5 text-xs leading-relaxed">
              <p>{item.question || item.prompt}</p>
              <dl className="grid grid-cols-[2rem_minmax(0,1fr)] gap-x-3 gap-y-2"><dt className="font-medium">Yes</dt><dd className="text-muted-foreground">{item.yes}</dd><dt className="font-medium">No</dt><dd className="text-muted-foreground">{item.no}</dd></dl>
            </div>
          </details>)}</div></CardContent>
        </Card>
        <Card role="region" aria-label="Model vote coverage">
          <CardHeader><CardTitle>Vote coverage</CardTitle><CardDescription>Across all boolean judges in this selection.</CardDescription></CardHeader>
          <CardContent>{report.models.length ? <Table>
            <TableHeader><TableRow><TableHead>Model</TableHead><TableHead className="text-right">Valid</TableHead><TableHead className="text-right">Missing / invalid</TableHead></TableRow></TableHeader>
            <TableBody>{report.models.map((model) => <TableRow key={model.model}><TableCell className="whitespace-normal">{model.model}</TableCell><TableCell className="text-right tabular-nums">{model.votes.toLocaleString()}</TableCell><TableCell className="text-right tabular-nums">{model.missingVotes.toLocaleString()}</TableCell></TableRow>)}</TableBody>
          </Table> : <p className="text-sm text-muted-foreground">No model votes recorded.</p>}</CardContent>
        </Card>
      </div>
    </div>
  )
}
