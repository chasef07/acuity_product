import { queryLocationKnowledge } from "../api/generated/sdk.gen"
import { usePortalQuery } from "./portal-request"

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
