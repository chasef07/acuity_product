import { queryInsurancePlans } from "../api/generated/sdk.gen"
import type { InsuranceCoverage } from "../api/generated/types.gen"
import { useRead } from "../data/queries"

export function useInsurancePlans({
  practiceID,
  locationID,
  coverage,
}: {
  practiceID: string
  locationID: string
  coverage: InsuranceCoverage
}) {
  return useRead({
    name: "insurance-plans",
    identity: { practiceID, locationID, coverage },
    enabled: Boolean(locationID),
    read: (identity, transport) =>
      queryInsurancePlans({
        ...transport,
        body: {
          practiceId: identity.practiceID,
          locationId: identity.locationID,
          coverage: identity.coverage,
        },
      }),
  })
}
