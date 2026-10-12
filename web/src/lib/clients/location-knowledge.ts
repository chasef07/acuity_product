import {
  publishLocationKnowledge as publishLocationKnowledgeRequest,
  queryLocationKnowledge,
} from "../api/generated/sdk.gen"
import type { KnowledgePublication } from "../api/generated/types.gen"
import { portalRequest, usePortalQuery } from "./portal-request"

export function useLocationKnowledge({
  practiceID,
  locationID,
}: {
  practiceID: string
  locationID: string
}) {
  return usePortalQuery(
    locationID
      ? JSON.stringify(["location-knowledge", practiceID, locationID])
      : null,
    (transport) =>
      queryLocationKnowledge({
        ...transport,
        body: { practiceId: practiceID, locationId: locationID },
      }),
  )
}

export function publishLocationKnowledge(publication: KnowledgePublication) {
  return portalRequest((transport) =>
    publishLocationKnowledgeRequest({ ...transport, body: publication }),
  )
}
