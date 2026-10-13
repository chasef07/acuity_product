import {
  publishLocationKnowledge as publishLocationKnowledgeRequest,
  queryLocationKnowledge,
  queryLocationKnowledgeHistory,
  readLocationKnowledgeRevision,
} from "../api/generated/sdk.gen"
import type { KnowledgePublication } from "../api/generated/types.gen"
import { portalRequest, usePortalQuery } from "./portal-request"

type LocationScope = {
  practiceID: string
  locationID: string
}

export function useLocationKnowledge({ practiceID, locationID }: LocationScope) {
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

export function useLocationKnowledgeHistory({ practiceID, locationID }: LocationScope) {
  return usePortalQuery(
    JSON.stringify(["location-knowledge-history", practiceID, locationID]),
    (transport) =>
      queryLocationKnowledgeHistory({
        ...transport,
        body: { practiceId: practiceID, locationId: locationID },
      }),
  )
}

export function useLocationKnowledgeRevision({
  practiceID,
  locationID,
  revisionID,
}: LocationScope & { revisionID: string | undefined }) {
  return usePortalQuery(
    revisionID
      ? JSON.stringify(["location-knowledge-revision", practiceID, locationID, revisionID])
      : null,
    (transport) =>
      readLocationKnowledgeRevision({
        ...transport,
        body: { practiceId: practiceID, locationId: locationID, revisionId: revisionID! },
      }),
  )
}

export function publishLocationKnowledge(publication: KnowledgePublication) {
  return portalRequest((transport) =>
    publishLocationKnowledgeRequest({ ...transport, body: publication }),
  )
}
