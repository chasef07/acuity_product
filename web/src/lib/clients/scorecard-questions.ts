import { getOperatorScorecardQuestions } from "../api/generated/sdk.gen"
import { useRead } from "../data/queries"
import { isJuryScorecard } from "../scorecard-flow"

export function useScorecardQuestions(version?: string) {
  const supportedVersion = isJuryScorecard(version) ? version : undefined
  return useRead({
    name: "scorecard-questions",
    identity: { scorecard: supportedVersion },
    immutable: supportedVersion !== undefined,
    read: (identity, transport) =>
      getOperatorScorecardQuestions({ ...transport, query: { version: identity.scorecard } }),
  })
}
