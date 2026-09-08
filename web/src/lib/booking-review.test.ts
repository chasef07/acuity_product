import assert from "node:assert/strict"
import test from "node:test"
import {
  parseBookingReviewNotes,
  scenarioMarkdown,
  type BookingReviewNotes,
} from "./booking-review.ts"

test("scenario export includes only reviewed cohort and never evidence observations or call IDs", () => {
  const notes: BookingReviewNotes = {
    "private-call-id": {
      bucket: "tool",
      observation: "PRIVATE EVIDENCE",
      scenario: "A fictional caller accepts a slot.",
      expected: "Recover a tool error without claiming success.",
      savedAt: "2026-09-05",
    },
    "outside-cohort": {
      bucket: "insurance",
      observation: "",
      scenario: "OUTSIDE COHORT",
      expected: "OUTSIDE EXPECTATION",
      savedAt: "2026-09-05",
    },
  }
  const output = scenarioMarkdown(notes, ["private-call-id", "unreviewed"])
  assert.match(output, /fictional caller/)
  assert.match(output, /Recover a tool error/)
  for (const text of [
    "PRIVATE EVIDENCE",
    "private-call-id",
    "OUTSIDE COHORT",
    "OUTSIDE EXPECTATION",
  ])
    assert.ok(!output.includes(text))
  assert.deepEqual(parseBookingReviewNotes(JSON.stringify(notes)), notes)
})

test("missing or corrupt local reviews are distinguished; incomplete scenarios are not exported", () => {
  assert.deepEqual(parseBookingReviewNotes(null), {})
  for (const raw of [
    "invalid",
    "[]",
    '{"id":{"bucket":"invented"}}',
    '{"id":{"bucket":"tool","observation":4}}',
  ])
    assert.throws(() => parseBookingReviewNotes(raw))
  assert.ok(
    !scenarioMarkdown(
      {
        id: {
          bucket: "tool",
          observation: "",
          scenario: "",
          expected: "",
          savedAt: "",
        },
      },
      ["id"],
    ).includes("## Tool"),
  )
})
