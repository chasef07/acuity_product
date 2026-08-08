import assert from "node:assert/strict"
import test from "node:test"

import {
  googleProviderConfiguration,
  portalAuthenticationConfiguration,
} from "./auth-providers.ts"

test("Google authentication requires the client ID and secret together", () => {
  assert.throws(
    () =>
      googleProviderConfiguration({
        GOOGLE_CLIENT_ID: "google-client-id",
      }),
    /GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET must be configured together/
  )
})

test("configured Google authentication is the only human authentication", () => {
  const authentication = portalAuthenticationConfiguration({
    GOOGLE_CLIENT_ID: "google-client-id",
    GOOGLE_CLIENT_SECRET: "google-client-secret",
  })

  assert.ok(authentication.socialProviders.google)
})

test("a runtime without Google is rejected", () => {
  assert.throws(
    () => portalAuthenticationConfiguration({}),
    /Google authentication is required/
  )
})
