import type { KnowledgeSection, LocationKnowledge } from "./api/generated/types.gen.ts"
import type { PortalFailure } from "./clients/portal-request.ts"

export type KnowledgeDraftEntry = KnowledgeSection & { key: string }

export type KnowledgeDraft = {
  base: LocationKnowledge
  entries: KnowledgeDraftEntry[]
  reason: string
  publish: { status: "idle" } | { status: "publishing" } | { status: "failed"; failure: PortalFailure }
  attempt?: { payload: string; revisionID: string }
}

const drafts = new Map<string, KnowledgeDraft>()
const listeners = new Set<() => void>()

export function knowledgeDraftKey(actorSubject: string, practiceID: string, locationID: string) {
  return `${actorSubject}:${practiceID}:${locationID}`
}

export function subscribeKnowledgeDrafts(listener: () => void) {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}

export function readKnowledgeDraft(key: string) {
  return drafts.get(key)
}

export function writeKnowledgeDraft(key: string, draft: KnowledgeDraft | undefined) {
  if (draft) drafts.set(key, draft)
  else drafts.delete(key)
  changedDrafts()
}

export function clearKnowledgeDrafts() {
  drafts.clear()
  changedDrafts()
}

function changedDrafts() {
  if (drafts.size > 0) window.addEventListener("beforeunload", warnUnpublished)
  else window.removeEventListener("beforeunload", warnUnpublished)
  for (const listener of listeners) listener()
}

function warnUnpublished(event: BeforeUnloadEvent) {
  event.preventDefault()
}
