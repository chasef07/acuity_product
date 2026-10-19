import type { CallIntent, CallOutcome, OperatorAiPathCount } from "@/lib/api/generated/types.gen"
import { intentLabels, outcomeLabels } from "./call-labels"

const knowledgeGapKey = "knowledge_available"

export type CallProblem = {
  key: string
  intent: CallIntent
  outcome?: CallOutcome
  failure?: string
  label: string
  kbGap: boolean
  calls: number
  intentCalls: number
  rate: number
  averageRate: number
  tooFew: boolean
}

export function rankProblems(paths: OperatorAiPathCount[], failureLabels: Record<string, string>, minIntentCalls: number): CallProblem[] {
  const labeled = paths.filter((path) => path.intent)
  const sum = (rows: OperatorAiPathCount[]) => rows.reduce((total, path) => total + path.calls, 0)
  const withOutcome = labeled.filter((path) => path.outcome)
  const judged = labeled.filter((path) => path.evaluated)
  const problems: CallProblem[] = []
  const add = (intent: CallIntent, base: OperatorAiPathCount[], hit: (path: OperatorAiPathCount) => boolean, average: number, row: Pick<CallProblem, "key" | "outcome" | "failure" | "label" | "kbGap">) => {
    const own = base.filter((path) => path.intent === intent)
    const calls = sum(own.filter(hit))
    const intentCalls = sum(own)
    if (calls) problems.push({ ...row, intent, calls, intentCalls, rate: calls / intentCalls, averageRate: average, tooFew: intentCalls < minIntentCalls })
  }
  const intents = [...new Set(labeled.map((path) => path.intent!))]
  const abandoned = (path: OperatorAiPathCount) => path.outcome === "abandoned"
  const abandonRate = sum(withOutcome.filter(abandoned)) / (sum(withOutcome) || 1)
  for (const intent of intents) {
    add(intent, withOutcome, abandoned, abandonRate, { key: `${intent}:abandoned`, outcome: "abandoned", label: outcomeLabels.abandoned, kbGap: false })
  }
  const failures = [...new Set(judged.flatMap((path) => path.failures))].sort()
  for (const failure of failures) {
    const failed = (path: OperatorAiPathCount) => path.failures.includes(failure)
    const failRate = sum(judged.filter(failed)) / (sum(judged) || 1)
    for (const intent of intents) {
      add(intent, judged, failed, failRate, { key: `${intent}:${failure}`, failure, label: failureLabels[failure] ?? failure, kbGap: failure === knowledgeGapKey })
    }
  }
  return problems.sort((a, b) => Number(a.tooFew) - Number(b.tooFew) || b.calls - a.calls || Number(Boolean(a.failure)) - Number(Boolean(b.failure)) || a.key.localeCompare(b.key))
}

export type FlowTone = "neutral" | "muted" | "pass" | "fail" | "gap"
export type FlowNode = { name: string; column: number; tone: FlowTone; intent?: CallIntent; outcome?: CallOutcome }
export type FlowLink = { source: number; target: number; value: number }

const maxIntents = 7
const restIntents = "rest"
const outcomeOrder: Array<CallOutcome | undefined> = ["resolved", "abandoned", "transferred", undefined]

export function callFlow(paths: OperatorAiPathCount[], failureLabels: Record<string, string>) {
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
        : { name: intentLabels[key as CallIntent], column: 1, tone: "neutral", intent: key as CallIntent })
  }
  for (const outcome of outcomeOrder) {
    if (paths.some((path) => path.outcome === outcome)) node(`outcome:${outcome ?? ""}`, outcome ? { name: outcomeLabels[outcome], column: 2, tone: "neutral", outcome } : { name: "No outcome", column: 2, tone: "muted" })
  }
  const judge = (path: OperatorAiPathCount) => !path.evaluated ? "Not judged" : path.failures.length ? "Fail" : "Pass"
  for (const name of ["Pass", "Fail", "Not judged"]) {
    if (paths.some((path) => judge(path) === name)) node(`judge:${name}`, { name, column: 3, tone: name === "Pass" ? "pass" : name === "Fail" ? "fail" : "muted" })
  }
  const reason = (path: OperatorAiPathCount) => path.failures.length > 1 ? "multiple" : path.failures[0]
  const reasonCalls = new Map<string, number>()
  for (const path of paths) if (path.evaluated && path.failures.length) reasonCalls.set(reason(path), (reasonCalls.get(reason(path)) ?? 0) + path.calls)
  for (const [key] of [...reasonCalls].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))) {
    node(`reason:${key}`, key === "multiple"
      ? { name: "Multiple issues", column: 4, tone: "fail" }
      : { name: failureLabels[key] ?? key, column: 4, tone: key === knowledgeGapKey ? "gap" : "fail" })
  }
  const values = new Map<string, number>()
  const link = (source: number, target: number, calls: number) => values.set(`${source}:${target}`, (values.get(`${source}:${target}`) ?? 0) + calls)
  for (const path of paths) {
    const route = [0, index.get(`intent:${bucket(path.intent) ?? ""}`)!, index.get(`outcome:${path.outcome ?? ""}`)!, index.get(`judge:${judge(path)}`)!]
    if (judge(path) === "Fail") route.push(index.get(`reason:${reason(path)}`)!)
    for (let step = 1; step < route.length; step++) link(route[step - 1], route[step], path.calls)
  }
  const links: FlowLink[] = [...values].map(([key, value]) => {
    const [source, target] = key.split(":").map(Number)
    return { source, target, value }
  }).sort((a, b) => a.source - b.source || a.target - b.target)
  return { nodes, links }
}
