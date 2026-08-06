import assert from "node:assert/strict"
import test from "node:test"

import {
  mediaAttachmentAfterState,
  routeIncomingMedia,
} from "./dock-media-state.ts"

test("inbound media recovery keeps its attachment across reconnect", () => {
  const attached = {
    providerLegID: "inbound-leg",
    mediaToken: "a".repeat(43),
  }
  const reconnecting = mediaAttachmentAfterState("reconnecting", attached)

  assert.equal(reconnecting, attached)
  assert.equal(
    routeIncomingMedia(
      reconnecting,
      { ...attached, recovery: true },
      true,
      "ready",
      "inbound-call-id",
    ),
    "RECOVER_ATTACHED",
  )
})

test("unavailable media discards its stale attachment", () => {
  assert.equal(
    mediaAttachmentAfterState("unavailable", {
      providerLegID: "stale-leg",
      mediaToken: "b".repeat(43),
    }),
    null,
  )
})
