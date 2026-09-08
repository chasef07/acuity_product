export type CallReviewTags = { tags: string[]; calls: Record<string, string[]> }
export type TagChange = { callID: string; tag: string; remove?: boolean }

export function readCallReviewTags(raw: string | null): CallReviewTags {
  if (!raw) return { tags: [], calls: {} }
  const value = JSON.parse(raw) as CallReviewTags
  if (
    !value ||
    !Array.isArray(value.tags) ||
    !value.tags.every(
      (tag) => typeof tag === "string" && tag.trim() && tag.length <= 80,
    ) ||
    !value.calls ||
    typeof value.calls !== "object" ||
    Array.isArray(value.calls)
  )
    throw new Error("Invalid saved tags")
  for (const tags of Object.values(value.calls)) {
    if (
      !Array.isArray(tags) ||
      !tags.every((tag) => typeof tag === "string" && value.tags.includes(tag))
    )
      throw new Error("Invalid call tags")
  }
  return value
}

export function changeCallTag(
  state: CallReviewTags,
  change: TagChange,
): CallReviewTags {
  const name = change.tag.trim().replace(/\s+/g, " ")
  if (!name || name.length > 80)
    throw new Error("Use a tag between 1 and 80 characters")
  const tag =
    state.tags.find(
      (existing) => existing.toLowerCase() === name.toLowerCase(),
    ) ?? name
  const assigned = state.calls[change.callID] ?? []
  return {
    tags:
      state.tags.includes(tag) || change.remove
        ? state.tags
        : [...state.tags, tag],
    calls: {
      ...state.calls,
      [change.callID]: change.remove
        ? assigned.filter((existing) => existing !== tag)
        : [...new Set([...assigned, tag])],
    },
  }
}
