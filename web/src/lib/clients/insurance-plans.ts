import { queryInsurancePlans } from "../api/generated/sdk.gen"
import type { InsuranceCoverage } from "../api/generated/types.gen"
import { usePortalQuery } from "./portal-request"

export function useInsurancePlans({
  practiceID,
  locationID,
  coverage,
}: {
  practiceID: string
  locationID: string
  coverage: InsuranceCoverage
}) {
  return usePortalQuery(
    locationID
      ? JSON.stringify(["insurance-plans", practiceID, locationID, coverage])
      : null,
    (transport) =>
      queryInsurancePlans({
        ...transport,
        body: { practiceId: practiceID, locationId: locationID, coverage },
      }),
  )
}
