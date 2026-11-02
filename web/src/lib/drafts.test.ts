import assert from "node:assert/strict"
import test from "node:test"

import { nextAttempt } from "./drafts.ts"

test("a publish attempt keeps its revision ID until the publication changes", () => {
  const first = nextAttempt(undefined, { reason: "Hours", sections: [] })
  assert.equal(nextAttempt(first, { reason: "Hours", sections: [] }), first)
  const changed = nextAttempt(first, { reason: "Parking", sections: [] })
  assert.notEqual(changed.revisionID, first.revisionID)
  assert.equal(changed.payload, JSON.stringify({ reason: "Parking", sections: [] }))
})
