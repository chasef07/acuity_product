import type { KnowledgeSection, LocationKnowledge } from "./api/generated/types.gen.ts"
import type { PortalFailure } from "./clients/portal-request.ts"
import { draftStore } from "./drafts.ts"

export type KnowledgeDraftEntry = KnowledgeSection & { key: string }

export type KnowledgeDraft = {
  base: LocationKnowledge
  entries: KnowledgeDraftEntry[]
  reason: string
  publish: { status: "idle" } | { status: "publishing" } | { status: "failed"; failure: PortalFailure }
  attempt?: { payload: string; revisionID: string }
}

export const knowledgeDrafts = draftStore<KnowledgeDraft>("knowledge")
