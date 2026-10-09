import { useState } from "react"

import {
  getOperatorCallReview,
  getOperatorScorecardQuestions,
  openOperatorCallReviewQueue,
  queryOperatorJudgeAccuracy,
  queryOperatorScorecard,
  queryOperatorScorecardResults,
  submitOperatorCallReview,
} from "../api/generated/sdk.gen"
import type { OperatorCallReviewSubmission } from "../api/generated/types.gen"
import { portalRequest, usePortalQuery } from "./portal-request"

function useBrowserTimeZone() {
  const [timeZone] = useState(
    () => Intl.DateTimeFormat().resolvedOptions().timeZone,
  )
  return timeZone
}

export function useScorecardQuestions() {
  return usePortalQuery("scorecard-questions", (transport) =>
    getOperatorScorecardQuestions(transport),
  )
}

export function useReviewQueue(practiceID: string, date: string, revision: number) {
  const timeZone = useBrowserTimeZone()
  return usePortalQuery(
    practiceID && date ? `review-queue:${practiceID}:${date}:${timeZone}:${revision}` : null,
    (transport) =>
      openOperatorCallReviewQueue({
        ...transport,
        body: { practiceId: practiceID, date, timeZone },
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

export function useJudgeAccuracy(practiceID: string, revision: number) {
  return usePortalQuery(
    practiceID ? `judge-accuracy:${practiceID}:${revision}` : null,
    (transport) =>
      queryOperatorJudgeAccuracy({ ...transport, body: { practiceId: practiceID } }),
  )
}

export function useScorecardReport(practiceID: string, locationID: string, weeks: number) {
  const timeZone = useBrowserTimeZone()
  return usePortalQuery(
    practiceID ? `scorecard-report:${practiceID}:${locationID}:${weeks}:${timeZone}` : null,
    (transport) =>
      queryOperatorScorecard({
        ...transport,
        body: {
          practiceId: practiceID,
          locationId: locationID || undefined,
          weeks,
          timeZone,
        },
      }),
  )
}

export function useScorecardResults(practiceID: string, locationID: string, range: "24h" | "7d" | "30d") {
  return usePortalQuery(
    practiceID ? `scorecard-results:${practiceID}:${locationID}:${range}` : null,
    (transport) =>
      queryOperatorScorecardResults({
        ...transport,
        body: { practiceId: practiceID, locationId: locationID || undefined, range },
      }),
  )
}
