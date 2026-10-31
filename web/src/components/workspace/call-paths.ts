import type { CallIntent, CallOutcome, OperatorAiPathCount } from "@/lib/api/generated/types.gen"
import { intentLabels, outcomeLabels } from "@/lib/vocabulary"

const gapKeys = new Set(["knowledge_available", "insurance_listed"])

export type CallSelection = { intent?: CallIntent; outcome?: CallOutcome; judge?: "pass" | "fail" | "unjudged"; failure?: string }

function judgeOf(path: OperatorAiPathCount) {
  return !path.evaluated ? "unjudged" : path.failures.length ? "fail" : "pass"
}

export function matchesSelection(path: OperatorAiPathCount, selection: CallSelection) {
  const judge = judgeOf(path)
  return (!selection.intent || path.intent === selection.intent) &&
    (!selection.outcome || path.outcome === selection.outcome) &&
    (!selection.judge || judge === selection.judge) &&
    (!selection.failure || path.failures.includes(selection.failure))
}

export function mergeSelection(current: CallSelection, next: CallSelection): CallSelection {
  const merged = { ...current, ...next }
  if (next.failure && merged.judge !== "fail") delete merged.judge
  if (next.judge && next.judge !== "fail") delete merged.failure
  return merged
}

export type BreakdownRow = { label: string; calls: number; selection?: CallSelection }

export function breakdownLists(paths: OperatorAiPathCount[], failureLabels: Record<string, string>) {
  const total = paths.reduce((sum, path) => sum + path.calls, 0)
  const list = (title: string, key: (path: OperatorAiPathCount) => string[], row: (key: string) => Omit<BreakdownRow, "calls">) => {
    const counts = new Map<string, number>()
    for (const path of paths) for (const value of key(path)) counts.set(value, (counts.get(value) ?? 0) + path.calls)
    const rows = [...counts].map(([value, calls]) => ({ ...row(value), calls }))
    return { title, total, rows: rows.sort((a, b) => Number(!a.selection) - Number(!b.selection) || b.calls - a.calls || a.label.localeCompare(b.label)) }
  }
  const unlabeled = { label: "Unlabeled" }
  return [
    list("Intents", (path) => [path.intent ?? ""], (intent) => intent ? { label: intentLabels[intent as CallIntent], selection: { intent: intent as CallIntent } } : unlabeled),
    list("Outcomes", (path) => [path.outcome ?? ""], (outcome) => outcome ? { label: outcomeLabels[outcome as CallOutcome], selection: { outcome: outcome as CallOutcome } } : unlabeled),
    list("Judge results", (path) => [judgeOf(path)], (judge) => ({ label: judgeLabels[judge as keyof typeof judgeLabels], selection: { judge: judge as CallSelection["judge"] } })),
    list("Failure reasons", (path) => path.failures, (failure) => ({ label: failureLabels[failure] ?? failure, selection: { failure } })),
  ]
}

export const judgeLabels = { pass: "Pass", fail: "Fail", unjudged: "Not judged" }

export type CallProblem = {
  key: string
  intent: CallIntent
  outcome?: CallOutcome
  failure?: string
  label: string
  calls: number
  rate: number
  selection: CallSelection
}

export function rankProblems(paths: OperatorAiPathCount[], failureLabels: Record<string, string>, minIntentCalls: number): CallProblem[] {
  const labeled = paths.filter((path) => path.intent)
  const sum = (rows: OperatorAiPathCount[]) => rows.reduce((total, path) => total + path.calls, 0)
  const problems: CallProblem[] = []
  const add = (intent: CallIntent, base: OperatorAiPathCount[], hit: (path: OperatorAiPathCount) => boolean, row: Pick<CallProblem, "key" | "outcome" | "failure" | "label">) => {
    const selection = row.outcome ? { intent, outcome: row.outcome } : { intent, failure: row.failure }
    const own = base.filter((path) => path.intent === intent)
    const calls = sum(own.filter(hit))
    const intentCalls = sum(own)
    if (calls && intentCalls >= minIntentCalls) problems.push({ ...row, intent, calls, rate: calls / intentCalls, selection })
  }
  const intents = [...new Set(labeled.map((path) => path.intent!))]
  const withOutcome = labeled.filter((path) => path.outcome)
  for (const intent of intents) {
    add(intent, withOutcome, (path) => path.outcome === "abandoned", { key: `${intent}:abandoned`, outcome: "abandoned", label: outcomeLabels.abandoned })
  }
  const judged = labeled.filter((path) => path.evaluated)
  for (const failure of [...new Set(judged.flatMap((path) => path.failures))].sort()) {
    for (const intent of intents) {
      add(intent, judged, (path) => path.failures.includes(failure), { key: `${intent}:${failure}`, failure, label: failureLabels[failure] ?? failure })
    }
  }
  return problems.sort((a, b) => b.calls - a.calls || Number(Boolean(a.failure)) - Number(Boolean(b.failure)) || a.key.localeCompare(b.key))
}

function bandSelection(source: FlowNode, target: FlowNode, fromAll: boolean): CallSelection | undefined {
  if (fromAll) return target.selection
  if (source.selection && target.selection) return { ...source.selection, ...target.selection }
}

export type FlowTone = "neutral" | "muted" | "pass" | "fail" | "gap"
export type FlowNode = { name: string; column: number; tone: FlowTone; selection?: CallSelection }
export type FlowLink = { source: number; target: number; value: number; selected: number; selection?: CallSelection }

const maxIntents = 7
const restIntents = "rest"
const outcomeOrder: Array<CallOutcome | undefined> = ["resolved", "abandoned", "transferred", undefined]

export function callFlow(paths: OperatorAiPathCount[], failureLabels: Record<string, string>, selection: CallSelection = {}) {
  const nodes: FlowNode[] = [{ name: "All calls", column: 0, tone: "neutral" }]
  const index = new Map<string, number>()
  const node = (key: string, value: FlowNode) => {
    if (!index.has(key)) index.set(key, nodes.push(value) - 1)
    return index.get(key)!
  }
  const intentCalls = new Map<CallIntent, number>()
  for (const path of paths) if (path.intent) intentCalls.set(path.intent, (intentCalls.get(path.intent) ?? 0) + path.calls)
  const shown = new Set([...intentCalls].sort((a, b) => b[1] - a[1]).slice(0, maxIntents).map(([intent]) => intent))
  const bucket = (intent?: CallIntent) => !intent ? undefined : shown.has(intent) ? intent : restIntents
  const position = new Map<string, [number, number]>()
  for (const path of paths) {
    const key = bucket(path.intent) ?? ""
    const [weighted, calls] = position.get(key) ?? [0, 0]
    position.set(key, [weighted + outcomeOrder.indexOf(path.outcome) * path.calls, calls + path.calls])
  }
  const mean = (key: string) => key ? position.get(key)![0] / position.get(key)![1] : Infinity
  for (const key of [...position.keys()].sort((a, b) => mean(a) - mean(b))) {
    node(`intent:${key}`, !key ? { name: "No intent", column: 1, tone: "muted" }
      : key === restIntents ? { name: "Other intents", column: 1, tone: "neutral" }
        : { name: intentLabels[key as CallIntent], column: 1, tone: "neutral", selection: { intent: key as CallIntent } })
  }
  for (const outcome of outcomeOrder) {
    if (paths.some((path) => path.outcome === outcome)) node(`outcome:${outcome ?? ""}`, outcome ? { name: outcomeLabels[outcome], column: 2, tone: "neutral", selection: { outcome } } : { name: "No outcome", column: 2, tone: "muted" })
  }
  for (const judge of ["pass", "fail", "unjudged"] as const) {
    if (paths.some((path) => judgeOf(path) === judge)) node(`judge:${judge}`, { name: judgeLabels[judge], column: 3, tone: judge === "unjudged" ? "muted" : judge, selection: { judge } })
  }
  const reason = (path: OperatorAiPathCount) => path.failures.length > 1 ? "multiple" : path.failures[0]
  const reasonCalls = new Map<string, number>()
  for (const path of paths) if (path.evaluated && path.failures.length) reasonCalls.set(reason(path), (reasonCalls.get(reason(path)) ?? 0) + path.calls)
  for (const [key] of [...reasonCalls].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))) {
    node(`reason:${key}`, key === "multiple"
      ? { name: "Multiple issues", column: 4, tone: "fail" }
      : { name: failureLabels[key] ?? key, column: 4, tone: gapKeys.has(key) ? "gap" : "fail", selection: { failure: key } })
  }
  const values = new Map<string, FlowLink>()
  for (const path of paths) {
    const route = [0, index.get(`intent:${bucket(path.intent) ?? ""}`)!, index.get(`outcome:${path.outcome ?? ""}`)!, index.get(`judge:${judgeOf(path)}`)!]
    if (judgeOf(path) === "fail") route.push(index.get(`reason:${reason(path)}`)!)
    const selected = matchesSelection(path, selection) ? path.calls : 0
    for (let step = 1; step < route.length; step++) {
      const [source, target] = [route[step - 1], route[step]]
      const link = values.get(`${source}:${target}`) ?? { source, target, value: 0, selected: 0, selection: bandSelection(nodes[source], nodes[target], source === 0) }
      link.value += path.calls
      link.selected += selected
      values.set(`${source}:${target}`, link)
    }
  }
  const links = [...values.values()].sort((a, b) => a.source - b.source || a.target - b.target)
  return { nodes, links }
}
