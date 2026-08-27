type CallingScope = {
  practiceId: string
  locationId: string
}

type AuthorizedCallingScopes = {
  practices: ReadonlyArray<{
    id: string
    locations: ReadonlyArray<{ id: string }>
  }>
}

export function workspaceScopeForCall(
  authority: AuthorizedCallingScopes,
  selectedPracticeID: string,
  selectedLocationScopeID: string,
  call: CallingScope,
) {
  const practice = authority.practices.find(
    (candidate) => candidate.id === call.practiceId,
  )
  if (!practice?.locations.some((location) => location.id === call.locationId)) {
    return undefined
  }
  if (
    selectedPracticeID === call.practiceId &&
    (!selectedLocationScopeID || selectedLocationScopeID === call.locationId)
  ) {
    return undefined
  }
  return { practiceID: call.practiceId, locationID: call.locationId }
}
