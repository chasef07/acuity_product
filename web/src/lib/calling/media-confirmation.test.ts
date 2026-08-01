import assert from "node:assert/strict"
import test from "node:test"

import { mediaConfirmationDecision } from "./media-confirmation.ts"

const mediaToken = "media-token"

test("media confirmation retries only while the attempt can still advance", () => {
  assert.equal(
    mediaConfirmationDecision(
      { expectedMediaToken: mediaToken, mediaReady: false, state: "PREPARING" },
      409,
      mediaToken,
    ),
    "retry",
  )
  assert.equal(
    mediaConfirmationDecision(
      { expectedMediaToken: mediaToken, mediaReady: false, state: "RECONCILING" },
      409,
      mediaToken,
    ),
    "retry",
  )
})

test("an advanced outbound call requires an authoritative server confirmation", () => {
  for (const state of ["RINGING", "CONNECTING", "CONNECTED"] as const) {
    assert.equal(
      mediaConfirmationDecision(
        { expectedMediaToken: mediaToken, mediaReady: true, state },
        409,
        mediaToken,
      ),
      "stop",
    )
  }
})

test("terminal calls and replaced media attempts stop confirmation", () => {
  assert.equal(
    mediaConfirmationDecision(
      {
        expectedMediaToken: mediaToken,
        mediaReady: true,
        state: "NEEDS_DISPOSITION",
      },
      409,
      mediaToken,
    ),
    "stop",
  )
  assert.equal(
    mediaConfirmationDecision(
      {
        expectedMediaToken: "replacement-token",
        mediaReady: false,
        state: "PREPARING",
      },
      409,
      mediaToken,
    ),
    "stop",
  )
})

test("post-readiness reconciliation never retries destination confirmation", () => {
  assert.equal(
    mediaConfirmationDecision(
      { expectedMediaToken: mediaToken, mediaReady: true, state: "RECONCILING" },
      409,
      mediaToken,
    ),
    "stop",
  )

  assert.equal(
    mediaConfirmationDecision(
      { expectedMediaToken: mediaToken, mediaReady: true, state: "PREPARING" },
      409,
      mediaToken,
    ),
    "stop",
  )
})

test("a transient refresh failure retries but definitive access failures stop", () => {
  assert.equal(mediaConfirmationDecision(undefined, undefined, mediaToken), "retry")
  assert.equal(mediaConfirmationDecision(undefined, 500, mediaToken), "retry")
  for (const status of [400, 401, 403, 404]) {
    assert.equal(mediaConfirmationDecision(undefined, status, mediaToken), "stop")
  }
})
