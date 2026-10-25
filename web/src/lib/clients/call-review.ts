import {
  getOperatorCallReview,
  getOperatorScorecardQuestions,
  openOperatorCallReviewQueue,
  queryOperatorJudgeAccuracy,
  submitOperatorCallReview,
} from "../api/generated/sdk.gen"
import type { OperatorCallReviewSubmission } from "../api/generated/types.gen"
import { useRead } from "../data/queries"
import { isJuryScorecard } from "../scorecard-flow"
import { portalRequest } from "./portal-request"

export function useScorecardQuestions(version?: string) {
  const supportedVersion = isJuryScorecard(version) ? version : undefined
  return useRead({
    name: "scorecard-questions",
    identity: { scorecard: supportedVersion },
    read: (identity, transport) =>
      getOperatorScorecardQuestions({ ...transport, query: { version: identity.scorecard } }),
  })
}

export function useReviewQueue(practiceID: string, date: string) {
  return useRead({
    name: "review-queue",
    identity: { practiceID, date },
    enabled: Boolean(practiceID && date),
    read: (identity, transport) =>
      openOperatorCallReviewQueue({
        ...transport,
        body: { practiceId: identity.practiceID, date: identity.date },
      }),
  })
}

export function useCallReview(interactionID: string) {
  return useRead({
    name: "call-review",
    identity: { interactionID },
    enabled: Boolean(interactionID),
    read: (identity, transport) =>
      getOperatorCallReview({ ...transport, path: { interactionId: identity.interactionID } }),
  })
}

export function submitCallReview(interactionID: string, body: OperatorCallReviewSubmission) {
  return portalRequest((transport) =>
    submitOperatorCallReview({
      ...transport,
      path: { interactionId: interactionID },
      body,
    }),
  )
}

export function useJudgeAccuracy(practiceID: string) {
  return useRead({
    name: "judge-accuracy",
    identity: { practiceID },
    enabled: Boolean(practiceID),
    read: (identity, transport) =>
      queryOperatorJudgeAccuracy({ ...transport, body: { practiceId: identity.practiceID } }),
  })
}
