import assert from "node:assert/strict"
import test from "node:test"

import { createBrowserCallRecoveryStore } from "./browser-call-recovery.ts"

test("Call recovery receipts are isolated by authenticated Staff identity", () => {
  const values = new Map<string, string>()
  const storage = {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => values.set(key, value),
    removeItem: (key: string) => values.delete(key),
  }
  const staffA = createBrowserCallRecoveryStore(storage, "staff-a")
  const staffB = createBrowserCallRecoveryStore(storage, "staff-b")

  staffA.persist("call-a-unanswered")

  assert.equal(staffA.load(), "call-a-unanswered")
  assert.equal(staffB.load(), undefined)
  staffB.persist("call-b-missed")
  assert.equal(staffA.load(), "call-a-unanswered")
  assert.equal(staffB.load(), "call-b-missed")

  staffA.persist(undefined)
  assert.equal(staffA.load(), undefined)
  assert.equal(staffB.load(), "call-b-missed")
})
