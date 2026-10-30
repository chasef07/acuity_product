import type { InsuranceCatalog, InsuranceRevision, InsuranceValidationError } from "./api/generated/types.gen.ts"
import type { PortalFailure } from "./clients/portal-request.ts"
import { draftStore } from "./drafts.ts"
import type { InsuranceEdit } from "./insurance-catalog.ts"

type InsurancePublish =
  | { status: "idle" }
  | { status: "publishing" }
  | { status: "rejected"; errors: InsuranceValidationError[] }
  | { status: "conflict" }
  | { status: "failed"; failure: PortalFailure }

export type InsuranceDraft = {
  base: { revision: InsuranceRevision; catalog: InsuranceCatalog }
  edits: InsuranceEdit[]
  reason: string
  publish: InsurancePublish
  attempt?: { payload: string; revisionID: string }
}

export const insuranceDrafts = draftStore<InsuranceDraft>("insurance")
