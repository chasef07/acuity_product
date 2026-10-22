import type { AgentCallDetail } from "./api/generated/types.gen.ts"

export type CheckState = "pass" | "fail" | "unknown" | "not_applicable" | "context"
export type FlowResult = NonNullable<AgentCallDetail["checkResult"]>

export function checkState(
  result: Record<string, unknown>,
  answer: "yes" | "no" | undefined,
  failure: boolean | undefined,
  error = false,
): CheckState {
  if (error) return "unknown"
  if (result.status === "not_applicable") return "not_applicable"
  if (result.status === "unknown" || answer === undefined || failure === undefined) return "unknown"
  if (!failure) return "context"
  return answer === "yes" ? "pass" : "fail"
}

export function flowResult(states: CheckState[], complete = true): FlowResult {
  if (states.includes("fail")) return "fail"
  if (!complete || states.includes("unknown") || !states.includes("pass")) return "unknown"
  return "pass"
}
