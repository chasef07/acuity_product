import assert from "node:assert/strict"
import test, { type TestContext } from "node:test"

import { clearAccessToken } from "../auth-client.ts"
import { createSoftphoneHTTPAdapter } from "./softphone-http-adapter.ts"

function backendAnswers(t: TestContext, code: string) {
  process.env.NEXT_PUBLIC_PORTAL_API_URL = "http://portal.test"
  clearAccessToken()
  t.mock.method(globalThis, "fetch", async (input: RequestInfo | URL) => {
    if (input === "/api/auth/token") return Response.json({ token: "synthetic-token" })
    return Response.json(
      { error: { code, message: "synthetic", retryable: false } },
      { status: 409 },
    )
  })
}

const start = {
  sessionID: "browser-a",
  idempotencyKey: "synthetic-key",
  taskId: "00000000-0000-4000-8000-000000000001",
} as never

for (const [code, message] of [
  ["CALL_OCCUPIED", "Another Call is pending or active."],
  ["CALL_CONFLICT", "This Task or Call changed. Refresh and try again."],
] as const) {
  test(`an outbound start refused with ${code} says why`, async (t) => {
    backendAnswers(t, code)
    await assert.rejects(
      createSoftphoneHTTPAdapter().startOutbound(start),
      (error: Error & { kind?: string }) => {
        assert.equal(error.message, message)
        assert.equal(error.kind, "conflict")
        return true
      },
    )
  })
}

test("a wrap-up sends its closures, and leaves the field out when nothing is ticked", async (t) => {
  process.env.NEXT_PUBLIC_PORTAL_API_URL = "http://portal.test"
  clearAccessToken()
  const bodies: unknown[] = []
  t.mock.method(globalThis, "fetch", async (input: RequestInfo | URL) => {
    if (input === "/api/auth/token") return Response.json({ token: "synthetic-token" })
    const request = input as Request
    assert.equal(new URL(request.url).pathname, "/v1/calling/calls/call-1/disposition")
    bodies.push(await request.json())
    return Response.json({ call: { id: "call-1" }, closedTaskIds: [], alreadyClosedTaskIds: [] })
  })
  const adapter = createSoftphoneHTTPAdapter()
  const wrapUp = { callID: "call-1", sessionID: "browser-a", outcome: "RESOLVED" as const }
  await adapter.dispose({ ...wrapUp, closures: [] })
  await adapter.dispose({ ...wrapUp, closures: [{ taskId: "task-1", reason: "NOT_NEEDED" }] })
  assert.deepEqual(bodies, [
    { sessionId: "browser-a", outcome: "RESOLVED" },
    {
      sessionId: "browser-a",
      outcome: "RESOLVED",
      closures: [{ taskId: "task-1", reason: "NOT_NEEDED" }],
    },
  ])
})
