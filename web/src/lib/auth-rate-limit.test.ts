import assert from "node:assert/strict"
import test from "node:test"

import { authRateLimitOptions } from "./auth-rate-limit.ts"

test("the token limit permits ten simultaneous requests for 30 office users", () => {
  assert.deepEqual(authRateLimitOptions(false), {
    enabled: true,
    customRules: {
      "/token": { window: 10, max: 300 },
    },
  })
})
