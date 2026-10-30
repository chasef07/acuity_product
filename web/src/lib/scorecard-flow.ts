import type { AgentCallDetail, GetOperatorScorecardQuestionsData } from "./api/generated/types.gen.ts"

type JuryScorecardVersion = NonNullable<NonNullable<GetOperatorScorecardQuestionsData["query"]>["version"]>

const currentScorecardVersion = "typesafe-scorecard-v10" satisfies JuryScorecardVersion
const juryScorecards: Record<JuryScorecardVersion, true> = {
  [currentScorecardVersion]: true,
  "typesafe-scorecard-v9": true,
  "typesafe-scorecard-v8": true,
  "typesafe-scorecard-v7": true,
  "typesafe-scorecard-v6": true,
}
export const juryScorecardVersions = Object.keys(juryScorecards) as readonly JuryScorecardVersion[]

export function isJuryScorecard(version: unknown): version is JuryScorecardVersion {
  return typeof version === "string" && Object.hasOwn(juryScorecards, version)
}

const failureJudgeScorecards = new Set<unknown>([currentScorecardVersion, "typesafe-scorecard-v9", "typesafe-scorecard-v8"] satisfies JuryScorecardVersion[])

export function hasFailureJudges(version: unknown): boolean {
  return failureJudgeScorecards.has(version)
}

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
