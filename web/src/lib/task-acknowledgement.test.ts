import assert from "node:assert/strict"
import test from "node:test"

import { automaticAcknowledgementLabel } from "./task-acknowledgement.ts"

test("missing Messaging configuration stays visible without an invented sender", () => {
  assert.equal(
    automaticAcknowledgementLabel({
		state: "PENDING",
      safeFailureCode: "SENDER_CONFIGURATION_UNAVAILABLE",
      updatedAt: "2026-08-20T09:00:00Z",
    }),
		"Waiting to retry · Messaging is not configured",
  )
})

test("queued acknowledgement routes delivery evidence to its linked Message", () => {
  assert.equal(
    automaticAcknowledgementLabel({
      state: "MESSAGE_QUEUED",
      messageId: "00000000-0000-0000-0000-000000000001",
      updatedAt: "2026-08-20T09:00:00Z",
    }),
    "Automatic text created · see Messages for delivery evidence",
  )
})
