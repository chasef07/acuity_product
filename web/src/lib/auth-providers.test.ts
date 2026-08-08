import assert from "node:assert/strict"
import test from "node:test"

import { googleProviderConfiguration } from "./auth-providers.ts"

test("Google authentication requires the client ID and secret together", () => {
  assert.throws(
    () =>
      googleProviderConfiguration({
        GOOGLE_CLIENT_ID: "google-client-id",
      }),
    /GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET must be configured together/
  )
})
