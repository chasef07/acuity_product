import assert from "node:assert/strict"
import test, { type TestContext } from "node:test"
import { act } from "react"

import { readTask } from "../api/generated/sdk.gen.ts"
import { clearAccessToken } from "../auth-client.ts"
import { portalRequest } from "./portal-request.ts"

const envelope = {
  error: {
    code: "TASK_VERSION_CONFLICT",
    message: "Task changed.",
    correlationId: "synthetic-correlation",
    retryable: false,
  },
}

test("normalizes an ErrorEnvelope into a typed failure", async (t) => {
  fakeBackend(t, () => Response.json(envelope, { status: 409 }))
  const outcome = await portalRequest((transport) =>
    readTask({ ...transport, path: { taskId: "task-1" } }),
  )
  assert.deepEqual(outcome, {
    ok: false,
    failure: {
      kind: "conflict",
      retryable: false,
      status: 409,
      code: "TASK_VERSION_CONFLICT",
      message: "Task changed.",
      correlationId: "synthetic-correlation",
    },
  })
})

test("classifies statuses without an envelope", async (t) => {
  const cases = [
    [401, "unauthenticated", false],
    [403, "unauthorized", false],
    [404, "missing", false],
    [409, "conflict", false],
    [429, "busy", true],
    [422, "rejected", false],
    [503, "unavailable", true],
  ] as const
  let status = 0
  fakeBackend(t, () => new Response("not an envelope", { status }))
  for (const [answer, kind, retryable] of cases) {
    status = answer
    const outcome = await portalRequest((transport) =>
      readTask({ ...transport, path: { taskId: "task-1" } }),
    )
    assert.deepEqual(outcome, { ok: false, failure: { kind, retryable, status } })
  }
})

test("a network failure is unavailable without a status", async (t) => {
  fakeBackend(t, () => {
    throw new TypeError("synthetic network failure")
  })
  const outcome = await portalRequest((transport) =>
    readTask({ ...transport, path: { taskId: "task-1" } }),
  )
  assert.deepEqual(outcome, {
    ok: false,
    failure: { kind: "unavailable", retryable: true },
  })
})

test("a missing session reports signed out and an unavailable token service does not, both sending nothing", async (t) => {
  const cases = [
    [401, { kind: "signedOut", retryable: false }],
    [400, { kind: "unavailable", retryable: true }],
  ] as const
  const backend = fakeBackend(t, () => Response.json({ id: "task-1" }))
  for (const [tokenStatus, failure] of cases) {
    backend.tokenStatus = tokenStatus
    const outcome = await portalRequest((transport) =>
      readTask({ ...transport, path: { taskId: "task-1" } }),
    )
    assert.deepEqual(outcome, { ok: false, failure })
    assert.deepEqual(backend.requests, [])
  }
})

test("an aborted request rejects instead of reporting an outcome", async (t) => {
  const backend = fakeBackend(t, () => Response.json({ id: "task-1" }))
  const gate = deferred()
  backend.gate = gate.promise
  const controller = new AbortController()
  const pending = portalRequest(
    (transport) => readTask({ ...transport, path: { taskId: "task-1" } }),
    controller.signal,
  )
  await waitFor(() => backend.requests.length === 1)
  controller.abort()
  gate.resolve()
  await assert.rejects(pending, { name: "AbortError" })
})

function fakeBackend(t: TestContext, respond: (url: URL) => Response) {
  process.env.NEXT_PUBLIC_PORTAL_API_URL = "http://portal.test"
  clearAccessToken()
  const backend = {
    tokenStatus: 200,
    tokenReads: 0,
    requests: [] as string[],
    aborted: 0,
    gate: undefined as Promise<void> | undefined,
    respond,
  }
  t.mock.method(globalThis, "fetch", async (input: RequestInfo | URL) => {
    if (input === "/api/auth/token") {
      backend.tokenReads += 1
      return Response.json({ token: "synthetic-token" }, { status: backend.tokenStatus })
    }
    const request = input as Request
    const url = new URL(request.url)
    backend.requests.push(
      `${request.method} ${url.pathname} ${request.headers.get("Authorization")}`,
    )
    const gate = backend.gate
    if (gate) {
      await Promise.race([
        gate,
        new Promise((_, reject) =>
          request.signal.addEventListener("abort", () => {
            backend.aborted += 1
            reject(request.signal.reason)
          }),
        ),
      ])
    }
    return backend.respond(url)
  })
  return backend
}

async function waitFor(ready: () => boolean) {
  for (let attempt = 0; attempt < 50 && !ready(); attempt++) {
    await act(async () => new Promise((resolve) => setTimeout(resolve, 0)))
  }
  assert.ok(ready(), "condition was not reached")
}

function deferred() {
  let resolve!: () => void
  const promise = new Promise<void>((done) => {
    resolve = done
  })
  return { promise, resolve }
}
