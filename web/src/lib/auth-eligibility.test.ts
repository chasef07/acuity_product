import assert from "node:assert/strict"
import test from "node:test"

import { createUserEligibilityGate } from "./auth-eligibility.ts"

test("Better Auth user creation is allowed only when Access approves the email", async () => {
  for (const [status, eligible] of [
    [204, true],
    [403, false],
    [429, false],
    [503, false],
  ] as const) {
    const requests: Array<{ body: string; url: string }> = []
    const gate = createUserEligibilityGate({
      portalAPIURL: "https://portal.example",
      request: async (url, init) => {
        requests.push({ body: String(init?.body), url: String(url) })
        return new Response(null, { status })
      },
    })

    assert.equal(await gate({ email: "staff@example.test" }), eligible, String(status))
    assert.deepEqual(requests, [
      {
        body: JSON.stringify({ email: "staff@example.test" }),
        url: "https://portal.example/v1/access/sign-up-eligibility",
      },
    ])
  }
})
