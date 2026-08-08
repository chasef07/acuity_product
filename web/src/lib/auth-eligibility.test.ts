import assert from "node:assert/strict"
import test from "node:test"

import {
  createUserEligibilityGate,
  isSignUpEligible,
} from "./auth-eligibility.ts"

test("an approved email may create a Better Auth user", async () => {
  const requests: Array<{ body: string; url: string }> = []
  const eligible = await isSignUpEligible({
    email: "chase@acuityhealth.io",
    portalAPIURL: "https://portal.example",
    request: async (url, init) => {
      requests.push({ body: String(init?.body), url: String(url) })
      return new Response(null, { status: 204 })
    },
  })

  assert.equal(eligible, true)
  assert.deepEqual(requests, [
    {
      body: JSON.stringify({ email: "chase@acuityhealth.io" }),
      url: "https://portal.example/v1/access/sign-up-eligibility",
    },
  ])
})

test("Better Auth user creation fails closed when Access denies the email", async () => {
  const gate = createUserEligibilityGate({
    portalAPIURL: "https://portal.example",
    request: async () => new Response(null, { status: 403 }),
  })

  assert.equal(
    await gate(
      { email: "unknown@example.com" },
      { headers: new Headers() }
    ),
    false
  )
})
