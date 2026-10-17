import type { ReactNode } from "react"
import { ArrowRightIcon, CheckIcon, ChevronRightIcon, CircleHelpIcon, XIcon } from "lucide-react"

import { CallLabel } from "./call-labels"
import type { CallIntent, CallOutcome, CallLabelingStatus } from "@/lib/api/generated/types.gen"
import { flowResult, type CheckState } from "@/lib/scorecard-flow"

export type FlowCall = { intent?: CallIntent; outcome?: CallOutcome; labelingStatus?: CallLabelingStatus }

export type FlowCheck = { key: string; label: string; state: CheckState }

export const checkLabels: Record<CheckState, string> = {
  pass: "Passed", fail: "Failed", unknown: "Unknown", not_applicable: "Not applicable", context: "Context",
}

export function ScorecardFlow({ call = {}, checks, complete = true, children }: {
  call?: FlowCall
  checks: FlowCheck[]
  complete?: boolean
  children: ReactNode
}) {
  const result = flowResult(checks.map((check) => check.state), complete)
  const failures = checks.filter((check) => check.state === "fail")
  const unknown = checks.filter((check) => check.state === "unknown")
  return (
    <div aria-label="Intent to outcome scorecard" className="mt-3 text-xs">
      <div className="@container rounded-xl border bg-muted/20 p-3">
        <CallFlow call={call} result={result} />
        <div className="mt-3 border-t pt-3">
          <p className={`font-medium ${result === "pass" ? "text-success" : result === "fail" ? "text-destructive" : "text-warning"}`}>
            {result === "pass" ? "All applicable checks passed" : result === "fail" ? `${failures.length} failed ${failures.length === 1 ? "check" : "checks"}` : "Checks incomplete"}
          </p>
          {failures.length > 0 && <ul className="mt-2 space-y-1.5 text-destructive">{failures.map((check) => <li key={check.key} className="flex items-start gap-2"><XIcon aria-hidden="true" className="mt-0.5 size-3 shrink-0" /><span>{check.label}</span></li>)}</ul>}
          {(unknown.length > 0 || !complete || checks.length === 0) && <p className="mt-2 text-muted-foreground">{unknown.length > 0 ? `${unknown.length} ${unknown.length === 1 ? "check needs" : "checks need"} an answer or more evidence.` : "Check evidence is not complete."}</p>}
          {result === "unknown" && complete && unknown.length === 0 && checks.length > 0 && <p className="mt-2 text-muted-foreground">No applicable graded checks were recorded.</p>}
        </div>
      </div>
      <p className="mt-2 text-[0.6875rem] leading-4 text-muted-foreground">Recorded outcome and check results are assessed separately. Resolved can include an accepted next step.</p>
      <details aria-label="Flow checks" className="group mt-4 rounded-lg border bg-card">
        <summary aria-label="Review judge checks" className="flex cursor-pointer list-none items-center gap-2 rounded-lg px-3 py-3 font-medium outline-none transition-colors hover:bg-muted/40 focus-visible:ring-2 focus-visible:ring-ring [&::-webkit-details-marker]:hidden">
          <ChevronRightIcon className="size-3.5 shrink-0 text-muted-foreground transition-transform group-open:rotate-90" aria-hidden="true" />
          <span className="flex-1">Review judge checks</span>
          <span className="font-normal tabular-nums text-muted-foreground">{checks.length} {checks.length === 1 ? "check" : "checks"}</span>
        </summary>
        <div className="border-t">
          {children}
        </div>
      </details>
    </div>
  )
}

export function CallFlow({ call, result = "unknown" }: { call: FlowCall; result?: "pass" | "fail" | "unknown" }) {
  return (
    <div className="grid grid-cols-1 @min-[24rem]:grid-cols-[minmax(0,1fr)_1rem_minmax(0,1fr)_1rem_minmax(0,1fr)] items-center gap-1.5 [&_[data-slot=badge]]:h-auto [&_[data-slot=badge]]:max-w-full [&_[data-slot=badge]]:whitespace-normal [&_[data-slot=badge]]:wrap-anywhere [&_[data-slot=badge]]:bg-transparent [&_[data-slot=badge]]:px-0 [&_[data-slot=badge]]:text-left [&_[data-slot=badge]]:leading-4">
      <div className="min-w-0 self-stretch rounded-lg border bg-card p-2.5">
        <p className="mb-2 text-[0.625rem] font-medium uppercase tracking-wider text-muted-foreground">Intent</p>
        <CallLabel call={call} kind="intent" />
      </div>
      <ArrowRightIcon aria-hidden="true" className="size-4 justify-self-center rotate-90 text-muted-foreground @min-[24rem]:rotate-0" />
      <div className="min-w-0 self-stretch rounded-lg border bg-card p-2.5">
        <p className="mb-2 text-[0.625rem] font-medium uppercase tracking-wider text-muted-foreground">Outcome</p>
        <CallLabel call={call} kind="outcome" />
      </div>
      <ArrowRightIcon aria-hidden="true" className="size-4 justify-self-center rotate-90 text-muted-foreground @min-[24rem]:rotate-0" />
      <div aria-label="Check result" aria-live="polite" className={`min-w-0 self-stretch rounded-lg border p-2.5 ${result === "pass" ? "border-success/40 bg-success/10 text-success" : result === "fail" ? "border-destructive/40 bg-destructive/10 text-destructive" : "border-warning/40 bg-warning/10 text-warning"}`}>
        <p className="mb-2 text-[0.625rem] font-medium uppercase tracking-wider">Checks</p>
        <p className={`flex flex-wrap items-center gap-1.5 font-semibold ${result === "unknown" ? "text-xs" : "text-sm"}`}>
          {result === "pass" ? <CheckIcon className="size-4 shrink-0" aria-hidden="true" /> : result === "fail" ? <XIcon className="size-4 shrink-0" aria-hidden="true" /> : <CircleHelpIcon className="size-4 shrink-0" aria-hidden="true" />}
          <span className="min-w-0 break-words">{result === "pass" ? "Pass" : result === "fail" ? "Fail" : "Incomplete"}</span>
        </p>
      </div>
    </div>
  )
}
