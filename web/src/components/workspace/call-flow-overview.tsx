"use client"

import { ArrowRightIcon } from "lucide-react"
import { ResponsiveContainer, Sankey } from "recharts"
import type { NodeProps, LinkProps } from "recharts/types/chart/Sankey"
import type { CallIntent, CallOutcome, OperatorAiAnalyticsDistributions } from "@/lib/api/generated/types.gen"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card"
import { cn } from "@/lib/utils"
import { intentLabels } from "./call-labels"
import { callFlow, rankProblems, type CallProblem, type FlowNode, type FlowTone } from "./call-paths"

const minProblemIntentCalls = 10
const shownProblems = 8

export type CallsFilter = { intent: CallIntent; outcome?: CallOutcome; failure?: string }
export type FlowSelection = { intent?: CallIntent; outcome?: CallOutcome }

const toneColor: Record<FlowTone, string> = {
  neutral: "var(--muted-foreground)",
  muted: "var(--border)",
  pass: "var(--success)",
  fail: "var(--destructive)",
  gap: "var(--warning)",
}

export function CallFlowOverview({ data, onSelect, onViewCalls }: {
  data: OperatorAiAnalyticsDistributions
  onSelect: (selection: FlowSelection) => void
  onViewCalls: (filter: CallsFilter) => void
}) {
  const labels = Object.fromEntries(data.failures.map((failure) => [failure.key, failure.label]))
  return <div className="mb-5 grid gap-5">
    <Card role="region" aria-label="Call flow" className="hidden md:flex">
      <CardHeader>
        <CardTitle>Call flow</CardTitle>
        <CardDescription>{data.totalCalls.toLocaleString()} calls from intent to outcome, judge result, and failure reason. Select an intent, outcome, or failure to explore it.</CardDescription>
      </CardHeader>
      <CardContent><CallFlowChart flow={callFlow(data.paths, labels)} onSelect={onSelect} /></CardContent>
      <CardFooter><p className="text-[11px] leading-relaxed text-muted-foreground">Amber marks office knowledge gaps; red marks agent mistakes. Gray calls have no label or no judge result and stay in the total.</p></CardFooter>
    </Card>
    <CallProblems problems={rankProblems(data.paths, labels, minProblemIntentCalls)} onViewCalls={onViewCalls} />
  </div>
}

function CallProblems({ problems, onViewCalls }: { problems: CallProblem[]; onViewCalls: (filter: CallsFilter) => void }) {
  const shown = problems.slice(0, shownProblems)
  return <Card role="region" aria-label="Problems to fix">
    <CardHeader>
      <CardTitle>Problems to fix</CardTitle>
      <CardDescription>Intent and failure pairs ranked by calls, with each rate against every intent.</CardDescription>
    </CardHeader>
    <CardContent>
      {shown.length ? <ol className="flex flex-col divide-y">
        {shown.map((problem) => <li key={problem.key} className={cn("flex flex-wrap items-center gap-x-4 gap-y-2 py-3 first:pt-0 last:pb-0", problem.tooFew && "text-muted-foreground")}>
          <div className="min-w-0 flex-1 basis-full sm:basis-0">
            <p className="text-sm font-medium">{intentLabels[problem.intent]}</p>
            <p className="mt-1 flex flex-wrap items-center gap-2 text-xs">
              <Badge variant={problem.outcome ? "outline" : problem.kbGap ? "secondary" : "destructive"} className={cn(problem.kbGap && "bg-warning/10 text-warning")}>
                {problem.outcome ? problem.label : `Failed: ${problem.label}`}
              </Badge>
              {problem.tooFew && <span>Too few calls to compare</span>}
            </p>
          </div>
          <dl className="flex shrink-0 gap-4 text-xs tabular-nums">
            <div><dt className="sr-only">Calls</dt><dd className="font-medium">{problem.calls.toLocaleString()} {problem.calls === 1 ? "call" : "calls"}</dd></div>
            <div><dt className="sr-only">Rate</dt><dd>{percent(problem.rate)} of {problem.intentCalls.toLocaleString()}</dd></div>
            <div><dt className="sr-only">Average</dt><dd className="text-muted-foreground">{percent(problem.averageRate)} avg</dd></div>
          </dl>
          <Button variant="outline" size="sm" aria-label={`View calls: ${intentLabels[problem.intent]}, ${problem.label}`} onClick={() => onViewCalls({ intent: problem.intent, outcome: problem.outcome, failure: problem.failure })}>
            View calls<ArrowRightIcon data-icon="inline-end" aria-hidden="true" />
          </Button>
        </li>)}
      </ol> : <p className="text-sm text-muted-foreground">No abandoned or failed labeled calls in this selection.</p>}
    </CardContent>
    <CardFooter><p className="text-[11px] leading-relaxed text-muted-foreground">
      Abandoned rates use calls with an outcome; judge rates use judged calls. Intents with fewer than {minProblemIntentCalls} such calls are listed last.
      {problems.length > shown.length && ` ${problems.length - shown.length} smaller problems not shown.`}
    </p></CardFooter>
  </Card>
}

function percent(value: number) {
  return `${Math.round(value * 100)}%`
}

function CallFlowChart({ flow, onSelect }: { flow: ReturnType<typeof callFlow>; onSelect: (selection: FlowSelection) => void }) {
  return <div className="h-[520px] w-full">
    <ResponsiveContainer width="100%" height="100%">
      <Sankey data={flow} iterations={32} sort={false} align="left" nodeWidth={10} nodePadding={12} margin={{ top: 8, right: 240, bottom: 8, left: 0 }}
        link={(props: LinkProps) => <FlowLinkPath {...props} />}
        node={(props: NodeProps) => <FlowNodeBox {...props} onExplore={onSelect} />} />
    </ResponsiveContainer>
  </div>
}

function FlowLinkPath({ sourceX, sourceY, sourceControlX, targetX, targetY, targetControlX, linkWidth, payload }: LinkProps) {
  const target = payload.target as unknown as FlowNode & { value: number }
  const source = payload.source as unknown as FlowNode
  const colored = target.tone !== "neutral" && target.tone !== "muted"
  return <path d={`M${sourceX},${sourceY} C${sourceControlX},${sourceY} ${targetControlX},${targetY} ${targetX},${targetY}`}
    fill="none" stroke={toneColor[target.tone]} strokeOpacity={colored ? 0.35 : 0.18} strokeWidth={Math.max(1, linkWidth)}>
    <title>{`${source.name} → ${target.name}: ${(payload as unknown as { value: number }).value.toLocaleString()} calls`}</title>
  </path>
}

function selectionFor(node: FlowNode): FlowSelection | undefined {
  if (node.intent) return { intent: node.intent }
  if (node.outcome) return { outcome: node.outcome }
  if (node.tone === "fail" || node.tone === "gap") return {}
}

function FlowNodeBox({ x, y, width, height, payload, onExplore }: NodeProps & { onExplore: (selection: FlowSelection) => void }) {
  const node = payload as unknown as FlowNode & { value: number }
  const selection = selectionFor(node)
  const clickable = Boolean(selection)
  const label = `${node.name} ${node.value.toLocaleString()}`
  return <g className={cn(clickable && "cursor-pointer")} onClick={selection ? () => onExplore(selection) : undefined}
    role={clickable ? "button" : undefined} aria-label={clickable ? `Explore ${node.name}` : undefined} tabIndex={clickable ? 0 : undefined}
    onKeyDown={selection ? (event) => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); onExplore(selection) } } : undefined}>
    <rect x={x} y={y} width={width} height={Math.max(1, height)} rx={2} fill={toneColor[node.tone]} />
    <text x={x + width + 6} y={y + height / 2} dy="0.35em" fontSize={11} fill="var(--foreground)"
      stroke="var(--card)" strokeWidth={3} paintOrder="stroke">{label}</text>
    <title>{label} calls</title>
  </g>
}
