import assert from "node:assert/strict"
import test from "node:test"
import { readCallReviewTags, changeCallTag } from "./call-review-tags.ts"

test("tags are reusable, case-insensitive, idempotent, and retained after removal", () => {
  const first = changeCallTag(readCallReviewTags(null),{callID:"call-1",tag:" get_availability "})
  const second = changeCallTag(first,{callID:"call-2",tag:"GET_AVAILABILITY"})
  assert.deepEqual(second.tags,["get_availability"])
  assert.deepEqual(second.calls["call-2"],["get_availability"])
  assert.deepEqual(changeCallTag(second,{callID:"call-2",tag:"get_availability"}),second)
  const removed = changeCallTag(second,{callID:"call-2",tag:"get_availability",remove:true})
  assert.deepEqual(removed.calls["call-2"],[])
  assert.deepEqual(removed.tags,["get_availability"])
  assert.deepEqual(readCallReviewTags(JSON.stringify(removed)),removed)
})

test("invalid saved data and invalid labels fail instead of replacing saved tags", () => {
  for(const value of ["broken","null",'{"tags":[],"calls":{"id":["missing"]}}','{"tags":[null],"calls":{}}']) assert.throws(()=>readCallReviewTags(value))
  for(const tag of ["   ","x".repeat(81)]) assert.throws(()=>changeCallTag(readCallReviewTags(null),{callID:"id",tag}))
})
