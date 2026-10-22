import {
  publishLocationKnowledge as publishLocationKnowledgeRequest,
  queryLocationKnowledge,
  queryLocationKnowledgeHistory,
  readLocationKnowledgeRevision,
} from "../api/generated/sdk.gen"
import type { KnowledgePublication } from "../api/generated/types.gen"
import { useRead } from "../data/queries"
import { portalRequest } from "./portal-request"

type LocationScope = {
  practiceID: string
  locationID: string
}

export function useLocationKnowledge({ practiceID, locationID }: LocationScope) {
  return useRead({
    name: "location-knowledge",
    identity: { practiceID, locationID },
    enabled: Boolean(locationID),
    read: (identity, transport) =>
      queryLocationKnowledge({
        ...transport,
        body: { practiceId: identity.practiceID, locationId: identity.locationID },
      }),
  })
}

export function useLocationKnowledgeHistory({ practiceID, locationID }: LocationScope) {
  return useRead({
    name: "location-knowledge-history",
    identity: { practiceID, locationID },
    read: (identity, transport) =>
      queryLocationKnowledgeHistory({
        ...transport,
        body: { practiceId: identity.practiceID, locationId: identity.locationID },
      }),
  })
}

export function useLocationKnowledgeRevision({
  practiceID,
  locationID,
  revisionID,
}: LocationScope & { revisionID: string | undefined }) {
  return useRead({
    name: "location-knowledge-revision",
    identity: { practiceID, locationID, revisionID },
    enabled: Boolean(revisionID),
    read: (identity, transport) =>
      readLocationKnowledgeRevision({
        ...transport,
        body: {
          practiceId: identity.practiceID,
          locationId: identity.locationID,
          revisionId: identity.revisionID ?? "",
        },
      }),
  })
}

export function publishLocationKnowledge(publication: KnowledgePublication) {
  return portalRequest((transport) =>
    publishLocationKnowledgeRequest({ ...transport, body: publication }),
  )
}
