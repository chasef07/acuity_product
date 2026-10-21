import {
  getOperatorCallReview,
  getOperatorScorecardQuestions,
  openOperatorCallReviewQueue,
  queryOperatorJudgeAccuracy,
  submitOperatorCallReview,
} from "../api/generated/sdk.gen"
import type { OperatorCallReviewSubmission } from "../api/generated/types.gen"
import { portalRequest, usePortalQuery } from "./portal-request"

export function useScorecardQuestions(version?: string) {
  const supportedVersion = version === "typesafe-scorecard-v6" || version === "typesafe-scorecard-v7" || version === "typesafe-scorecard-v8" ? version : undefined
  return usePortalQuery(`scorecard-questions:${supportedVersion ?? "current"}`, (transport) =>
    getOperatorScorecardQuestions({ ...transport, query: { version: supportedVersion } }),
  )
}

export function useReviewQueue(practiceID: string, date: string, revision: number) {
  return usePortalQuery(
    practiceID && date ? `review-queue:${practiceID}:${date}:${revision}` : null,
    (transport) =>
      openOperatorCallReviewQueue({
        ...transport,
        body: { practiceId: practiceID, date },
      }),
    { keepPrevious: true },
  )
}

export function useCallReview(interactionID: string) {
  return usePortalQuery(
    interactionID ? `call-review:${interactionID}` : null,
    (transport) =>
      getOperatorCallReview({ ...transport, path: { interactionId: interactionID } }),
  )
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
  return usePortalQuery(
    practiceID ? `judge-accuracy:${practiceID}` : null,
    (transport) =>
      queryOperatorJudgeAccuracy({ ...transport, body: { practiceId: practiceID } }),
  )
}
