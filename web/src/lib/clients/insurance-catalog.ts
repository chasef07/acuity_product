import {
  publishInsuranceCatalog as publishInsuranceCatalogRequest,
  queryInsuranceCatalog,
  queryInsuranceHistory,
  readInsuranceRevision,
} from "../api/generated/sdk.gen"
import type { InsurancePublication } from "../api/generated/types.gen"
import { useRead } from "../data/queries"
import { portalRequest } from "./portal-request"

type LocationScope = {
  practiceID: string
  locationID: string
}

export function useInsuranceCatalog({ practiceID, locationID }: LocationScope) {
  return useRead({
    name: "insurance-catalog",
    identity: { practiceID, locationID },
    enabled: Boolean(locationID),
    read: (identity, transport) =>
      queryInsuranceCatalog({
        ...transport,
        body: { practiceId: identity.practiceID, locationId: identity.locationID },
      }),
  })
}

export function readInsuranceCatalog({ practiceID, locationID }: LocationScope) {
  return portalRequest((transport) =>
    queryInsuranceCatalog({ ...transport, body: { practiceId: practiceID, locationId: locationID } }),
  )
}

export function useInsuranceHistory({ practiceID, locationID }: LocationScope) {
  return useRead({
    name: "insurance-history",
    identity: { practiceID, locationID },
    read: (identity, transport) =>
      queryInsuranceHistory({
        ...transport,
        body: { practiceId: identity.practiceID, locationId: identity.locationID },
      }),
  })
}

export function useInsuranceRevision({ practiceID, locationID, revisionID }: LocationScope & { revisionID: string }) {
  return useRead({
    name: "insurance-revision",
    identity: { practiceID, locationID, revisionID },
    read: (identity, transport) =>
      readInsuranceRevision({
        ...transport,
        body: { practiceId: identity.practiceID, locationId: identity.locationID, revisionId: identity.revisionID },
      }),
  })
}

export function publishInsuranceCatalog(publication: InsurancePublication) {
  return portalRequest((transport) =>
    publishInsuranceCatalogRequest({ ...transport, body: publication }),
  )
}
