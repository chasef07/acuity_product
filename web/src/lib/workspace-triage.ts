import type {
  Location,
  StaffTaskCategory,
} from "./api/generated/types.gen.ts"

export type TaskCategoryFilter = "all" | StaffTaskCategory

export function filterTasksByCategory<
  T extends { category?: StaffTaskCategory },
>(tasks: T[], category: TaskCategoryFilter) {
  return category === "all"
    ? tasks
    : tasks.filter((task) => task.category === category)
}

export function outboundLocationPreferenceKey(
  actorSubject: string,
  practiceID: string,
) {
  return `acuity.outboundLocation.${actorSubject}.${practiceID}`
}

export function resolveOutboundLocation(
  locations: Pick<Location, "id">[],
  rememberedLocationID: string,
  authorizedLocationCount = locations.length,
) {
  if (authorizedLocationCount === 1 && locations.length === 1) {
    return locations[0]!.id
  }
  return locations.some((location) => location.id === rememberedLocationID)
    ? rememberedLocationID
    : ""
}

export function recoveryGroupKey(locationID: string, phone: string) {
  return `${locationID}:${phone}`
}
