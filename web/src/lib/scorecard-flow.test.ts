import assert from "node:assert/strict"
import test from "node:test"
import { checkState } from "./scorecard-flow.ts"

test("unknown, inapplicable and errored judges cannot turn into passes from stale verdicts", () => {
  assert.equal(checkState({ status: "unknown" }, "yes", true), "unknown")
  assert.equal(checkState({ status: "not_applicable" }, "no", true), "not_applicable")
  assert.equal(checkState({}, "yes", true, true), "unknown")
  assert.equal(checkState({}, undefined, true), "unknown")
  assert.equal(checkState({}, "no", false), "context")
  assert.equal(checkState({}, "no", undefined), "unknown")
  assert.equal(checkState({}, "yes", true), "pass")
  assert.equal(checkState({}, "no", true), "fail")
})
