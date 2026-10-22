"use client"

import { ArrowRightIcon, CircleAlertIcon, XIcon } from "lucide-react"
import { ResponsiveContainer, Sankey } from "recharts"
import type { LinkProps, NodeProps } from "recharts/types/chart/Sankey"
import type { OperatorAiAnalyticsDistributions } from "@/lib/api/generated/types.gen"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardFooter, CardHeader, CardTitle } from "@/components/ui/card"
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { cn } from "@/lib/utils"
import { intentLabels, outcomeLabels } from "@/lib/vocabulary"
import { breakdownLists, callFlow, judgeLabels, matchesSelection, mergeSelection, rankProblems, type BreakdownRow, type CallSelection, type FlowLink, type FlowNode, type FlowTone } from "./call-paths"

const minGapIntentCalls = 10

const toneColor: Record<FlowTone, string> = {
  neutral: "var(--muted-foreground)",
  muted: "var(--border)",
  pass: "var(--success)",
  fail: "var(--destructive)",
  gap: "var(--warning)",
}

export type FlowMode = "flow" | "list"

export function CallBreakdowns({ data, selection, onSelection, mode, onMode, onRetry }: {
  data?: OperatorAiAnalyticsDistributions
  selection: CallSelection
  onSelection: (selection: CallSelection) => void
  mode: FlowMode
  onMode: (mode: FlowMode) => void
  onRetry: () => void
}) {
  if (!data || data.totalCalls === undefined) return (
    <Alert>
      <CircleAlertIcon aria-hidden="true" />
      <AlertTitle>Breakdowns unavailable</AlertTitle>
      <AlertDescription><p>Distribution data could not be loaded.</p><Button size="sm" variant="outline" onClick={onRetry}>Retry</Button></AlertDescription>
    </Alert>
  )
  const labels = Object.fromEntries(data.failures.map((failure) => [failure.key, failure.label]))
  const selected = data.paths.filter((path) => matchesSelection(path, selection))
  const chips = <SelectionChips selection={selection} labels={labels} calls={selected.reduce((sum, path) => sum + path.calls, 0)} onSelection={onSelection} />
  if (!data.totalCalls) return <div className="grid gap-4">{chips}<Empty><EmptyHeader><EmptyTitle>No calls in this date range</EmptyTitle><EmptyDescription>Change the date range or office.</EmptyDescription></EmptyHeader></Empty></div>
  const gaps = rankProblems(data.paths, labels, minGapIntentCalls).slice(0, 3)
  return <div className="grid min-w-0 grid-cols-[minmax(0,1fr)] gap-4">
    {gaps.length > 0 && <div role="group" aria-label="Biggest gaps" className="flex flex-wrap items-center gap-2">
      <span className="text-xs text-muted-foreground">Biggest gaps</span>
      {gaps.map((gap) => <Button key={gap.key} variant="outline" size="sm" className="h-auto max-w-full py-1.5 text-left whitespace-normal" onClick={() => onSelection(gap.selection)}>
        {intentLabels[gap.intent]} · {gap.outcome ? gap.label : `Failed: ${gap.label}`} · {Math.round(gap.rate * 100)}%
      </Button>)}
    </div>}
    {chips}
    <Card role="region" aria-label="Call flow">
      <CardHeader className="flex flex-row items-center justify-between gap-3">
        <CardTitle>Call flow</CardTitle>
        <ToggleGroup aria-label="Call flow view" variant="segmented" size="sm" className="hidden md:flex" value={[mode]} onValueChange={(values) => values[0] && onMode(values[0] as FlowMode)}>
          <ToggleGroupItem value="flow">Flow</ToggleGroupItem>
          <ToggleGroupItem value="list">List</ToggleGroupItem>
        </ToggleGroup>
      </CardHeader>
      <CardContent>
        {mode === "flow" && <div className="hidden md:block">
          <CallFlowChart flow={callFlow(data.paths, labels, selection)} highlight={Object.keys(selection).length > 0} onSelection={onSelection} />
        </div>}
        <div className={cn(mode === "flow" && "md:hidden")}>
          <BreakdownLists lists={breakdownLists(selected, labels)} onRow={(row) => onSelection(mergeSelection(selection, row))} />
        </div>
      </CardContent>
      <CardFooter><p className="text-[11px] leading-relaxed text-muted-foreground">Select a box, band, or row to see its calls below. Amber marks office knowledge gaps; red marks agent mistakes. Gray calls have no label or no judge result and stay in the total.</p></CardFooter>
    </Card>
  </div>
}

function SelectionChips({ selection, labels, calls, onSelection }: { selection: CallSelection; labels: Record<string, string>; calls: number; onSelection: (selection: CallSelection) => void }) {
  const chips = [
    selection.intent && { key: "intent", label: `Intent: ${intentLabels[selection.intent]}` },
    selection.outcome && { key: "outcome", label: `Outcome: ${outcomeLabels[selection.outcome]}` },
    selection.judge && { key: "judge", label: `Judge: ${judgeLabels[selection.judge]}` },
    selection.failure && { key: "failure", label: `Failed: ${labels[selection.failure] ?? selection.failure}` },
  ].filter((chip): chip is { key: keyof CallSelection; label: string } => Boolean(chip))
  return <div role="group" aria-label="Selection" className="flex min-h-8 flex-wrap items-center gap-2">
    <span className="text-xs text-muted-foreground">Showing</span>
    {chips.length ? <>
      {chips.map((chip) => <span key={chip.key} className="inline-flex min-h-7 max-w-full items-center gap-1 rounded-md border bg-muted/40 pl-2.5 text-xs">
        {chip.label}
        <Button variant="ghost" size="icon-xs" aria-label={`Remove ${chip.label}`} onClick={() => {
          const next = { ...selection }
          delete next[chip.key]
          onSelection(next)
        }}><XIcon aria-hidden="true" /></Button>
      </span>)}
      <span className="text-xs tabular-nums text-muted-foreground">{calls.toLocaleString()} {calls === 1 ? "call" : "calls"}</span>
      <Button variant="ghost" size="sm" onClick={() => onSelection({})}>Clear</Button>
    </> : <span className="text-xs">All calls</span>}
  </div>
}

function BreakdownLists({ lists, onRow }: { lists: ReturnType<typeof breakdownLists>; onRow: (selection: CallSelection) => void }) {
  return <div className="grid gap-x-10 gap-y-6 lg:grid-cols-2">
    {lists.map((list) => <section key={list.title} aria-label={list.title}>
      <h3 className="mb-3 text-xs font-medium text-muted-foreground">{list.title}</h3>
      {list.rows.length ? <dl className="flex flex-col gap-4">
        {list.rows.map((row) => <BreakdownBar key={row.label} row={row} total={list.total} onRow={onRow} />)}
      </dl> : <p className="text-xs text-muted-foreground">None in this selection.</p>}
    </section>)}
  </div>
}

function BreakdownBar({ row, total, onRow }: { row: BreakdownRow; total: number; onRow: (selection: CallSelection) => void }) {
  const share = total ? row.calls / total : 0
  return <div>
    <div className="flex items-baseline justify-between gap-4 text-xs">
      <dt className={cn(!row.selection && "text-muted-foreground")}>{row.selection
        ? <Button variant="link" size="sm" className="h-auto max-w-full justify-start p-0 text-left whitespace-normal" aria-label={`Select ${row.label}`} onClick={() => onRow(row.selection!)}>{row.label}<ArrowRightIcon data-icon="inline-end" /></Button>
        : row.label}</dt>
      <dd className="flex shrink-0 gap-3 tabular-nums"><span className="text-muted-foreground">{row.calls.toLocaleString()} {row.calls === 1 ? "call" : "calls"}</span><span className="w-11 text-right font-medium">{share.toLocaleString(undefined, { style: "percent", maximumFractionDigits: 1 })}</span></dd>
    </div>
    <div aria-hidden="true" className="mt-2 h-1.5 overflow-hidden rounded-full bg-muted"><div className={cn("h-full rounded-full", row.selection ? "bg-foreground/75" : "bg-muted-foreground/30")} style={{ width: `${share * 100}%` }} /></div>
  </div>
}

type Flow = ReturnType<typeof callFlow>

function CallFlowChart({ flow, highlight, onSelection }: { flow: Flow; highlight: boolean; onSelection: (selection: CallSelection) => void }) {
  const litNodes = new Set(flow.links.filter((link) => link.selected).flatMap((link) => [link.source, link.target]))
  return <div className="h-[520px] w-full">
    <ResponsiveContainer width="100%" height="100%">
      <Sankey data={flow} iterations={32} sort={false} align="left" nodeWidth={10} nodePadding={12} margin={{ top: 8, right: 240, bottom: 8, left: 0 }}
        link={(props: LinkProps) => <FlowLinkPath {...props} highlight={highlight} onExplore={onSelection} />}
        node={(props: NodeProps) => <FlowNodeBox {...props} dim={highlight && !litNodes.has(props.index)} onExplore={onSelection} />} />
    </ResponsiveContainer>
  </div>
}

function FlowLinkPath({ sourceX, sourceY, sourceControlX, targetX, targetY, targetControlX, linkWidth, payload, highlight, onExplore }: LinkProps & { highlight: boolean; onExplore: (selection: CallSelection) => void }) {
  const target = payload.target as unknown as FlowNode
  const source = payload.source as unknown as FlowNode
  const { selection, selected } = payload as unknown as FlowLink
  const colored = target.tone !== "neutral" && target.tone !== "muted"
  const opacity = !highlight ? (colored ? 0.35 : 0.18) : selected ? 0.55 : 0.05
  return <path d={`M${sourceX},${sourceY} C${sourceControlX},${sourceY} ${targetControlX},${targetY} ${targetX},${targetY}`}
    fill="none" stroke={toneColor[target.tone]} strokeOpacity={opacity} strokeWidth={Math.max(1, linkWidth)}
    className={cn(selection && "cursor-pointer")} onClick={selection ? () => onExplore(selection) : undefined}
    role={selection ? "button" : undefined} aria-label={selection ? `Select ${source.name} to ${target.name}` : undefined} tabIndex={selection ? 0 : undefined}
    onKeyDown={selection ? (event) => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); onExplore(selection) } } : undefined}>
    <title>{`${source.name} → ${target.name}: ${(payload as unknown as { value: number }).value.toLocaleString()} calls`}</title>
  </path>
}

function FlowNodeBox({ x, y, width, height, payload, dim, onExplore }: NodeProps & { dim: boolean; onExplore: (selection: CallSelection) => void }) {
  const node = payload as unknown as FlowNode & { value: number }
  const selection = node.selection
  const label = `${node.name} ${node.value.toLocaleString()}`
  return <g className={cn(selection && "cursor-pointer")} opacity={dim ? 0.3 : 1} onClick={selection ? () => onExplore(selection) : undefined}
    role={selection ? "button" : undefined} aria-label={selection ? `Select ${node.name}` : undefined} tabIndex={selection ? 0 : undefined}
    onKeyDown={selection ? (event) => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); onExplore(selection) } } : undefined}>
    <rect x={x} y={y} width={width} height={Math.max(1, height)} rx={2} fill={toneColor[node.tone]} />
    <text x={x + width + 6} y={y + height / 2} dy="0.35em" fontSize={11} fill="var(--foreground)"
      stroke="var(--card)" strokeWidth={3} paintOrder="stroke">{label}</text>
    <title>{label} calls</title>
  </g>
}
