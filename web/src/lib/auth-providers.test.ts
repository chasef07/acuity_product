import assert from "node:assert/strict"
import test from "node:test"
import { google } from "better-auth/social-providers"

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

test("Google authorization reuses the Google session without forcing account selection", async () => {
  const configuration = portalAuthenticationConfiguration({
    GOOGLE_CLIENT_ID: "google-client-id",
    GOOGLE_CLIENT_SECRET: "google-client-secret",
  })
  const url = await google(configuration.socialProviders.google).createAuthorizationURL({
    state: "test-state",
    codeVerifier: "test-code-verifier",
    redirectURI: "http://localhost:13000/api/auth/callback/google",
  })
  assert.equal(url.searchParams.has("prompt"), false)
  assert.equal(url.searchParams.get("state"), "test-state")
  assert.equal(url.searchParams.get("code_challenge_method"), "S256")
})
