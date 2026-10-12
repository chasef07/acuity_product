import type { KnowledgeSection, LocationKnowledge } from "./api/generated/types.gen.ts"

export type KnowledgeDraftEntry = KnowledgeSection & { key: string }

export type KnowledgeDraft = {
  base: LocationKnowledge
  entries: KnowledgeDraftEntry[]
  reason: string
}

const drafts = new Map<string, KnowledgeDraft>()

export function readKnowledgeDraft(key: string) {
  return drafts.get(key)
}

export function writeKnowledgeDraft(key: string, draft: KnowledgeDraft | undefined) {
  if (draft) drafts.set(key, draft)
  else drafts.delete(key)
  warnBeforeUnload(drafts.size > 0)
}

export function clearKnowledgeDrafts() {
  drafts.clear()
  warnBeforeUnload(false)
}

function warnBeforeUnload(active: boolean) {
  if (active) window.addEventListener("beforeunload", warnUnpublished)
  else window.removeEventListener("beforeunload", warnUnpublished)
}

function warnUnpublished(event: BeforeUnloadEvent) {
  event.preventDefault()
}
