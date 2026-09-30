import assert from "node:assert/strict"
import test, { type TestContext } from "node:test"
import { act } from "react"
import { createRoot } from "react-dom/client"
import { JSDOM } from "jsdom"

import { readTask } from "../api/generated/sdk.gen.ts"
import { clearAccessToken } from "../auth-client.ts"
import {
  type PortalQuery,
  portalRequest,
  usePortalQuery,
} from "./portal-request.ts"

const envelope = {
  error: {
    code: "TASK_VERSION_CONFLICT",
    message: "Task changed.",
    correlationId: "synthetic-correlation",
    retryable: false,
  },
}

test("sends with the access token and resolves the response data", async (t) => {
  const backend = fakeBackend(t, () => Response.json({ id: "task-1" }))
  const outcome = await portalRequest((transport) =>
    readTask({ ...transport, path: { taskId: "task-1" } }),
  )
  assert.deepEqual(outcome, { ok: true, data: { id: "task-1" } })
  assert.deepEqual(backend.requests, ["GET /v1/tasks/task-1 Bearer synthetic-token"])
})

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

test("a missing session sends nothing and reports signed out", async (t) => {
  const backend = fakeBackend(t, () => Response.json({ id: "task-1" }))
  backend.tokenStatus = 401
  const outcome = await portalRequest((transport) =>
    readTask({ ...transport, path: { taskId: "task-1" } }),
  )
  assert.deepEqual(outcome, {
    ok: false,
    failure: { kind: "signedOut", retryable: false },
  })
  assert.deepEqual(backend.requests, [])
})

test("an unavailable token service is not reported as signed out", async (t) => {
  const backend = fakeBackend(t, () => Response.json({ id: "task-1" }))
  backend.tokenStatus = 400
  const outcome = await portalRequest((transport) =>
    readTask({ ...transport, path: { taskId: "task-1" } }),
  )
  assert.deepEqual(outcome, {
    ok: false,
    failure: { kind: "unavailable", retryable: true },
  })
  assert.deepEqual(backend.requests, [])
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

test("usePortalQuery shows loading per key, aborts superseded reads, and retries", async (t) => {
  const backend = fakeBackend(t, (url) =>
    Response.json({ id: url.pathname.split("/").at(-1) }),
  )
  const view = mountQuery(t)
  const first = deferred()
  backend.gate = first.promise
  await view.render("task-1")
  assert.deepEqual(view.latest(), { status: "loading", refreshing: false })
  await waitFor(() => backend.requests.length === 1)

  backend.gate = undefined
  await view.render("task-2")
  await waitFor(() => view.latest().status !== "loading")
  assert.deepEqual(view.first("task-2"), { status: "loading", refreshing: false })
  assert.deepEqual(view.latest(), { status: "ready", data: { id: "task-2" }, refreshing: false })
  assert.equal(backend.aborted, 1)

  // The superseded response never replaces the current key.
  first.resolve()
  await act(async () => new Promise((resolve) => setTimeout(resolve, 0)))
  assert.deepEqual(view.latest(), { status: "ready", data: { id: "task-2" }, refreshing: false })

  backend.respond = () => Response.json(envelope, { status: 409 })
  await act(async () => view.retry())
  await waitFor(() => view.latest().status === "failed")
  assert.equal(view.latest().status, "failed")
  assert.deepEqual(backend.requests.slice(-1), ["GET /v1/tasks/task-2 Bearer synthetic-token"])

  const pending = deferred()
  backend.gate = pending.promise
  await act(async () => view.retry())
  assert.deepEqual(view.latest(), { status: "loading", refreshing: false })
  await waitFor(() => backend.requests.length === 4)
  await act(async () => view.root.unmount())
  assert.equal(backend.aborted, 2)
  pending.resolve()
})

test("usePortalQuery with a null key reads no token and sends nothing", async (t) => {
  const backend = fakeBackend(t, (url) =>
    Response.json({ id: url.pathname.split("/").at(-1) }),
  )
  const view = mountQuery(t)
  await view.render(null)
  await act(async () => new Promise((resolve) => setTimeout(resolve, 0)))
  assert.deepEqual(view.latest(), { status: "loading", refreshing: false })
  assert.equal(backend.tokenReads, 0)
  assert.deepEqual(backend.requests, [])

  await view.render("task-1")
  await waitFor(() => view.latest().status === "ready")
  assert.equal(backend.tokenReads, 1)
})

test("usePortalQuery keepPrevious refreshes without blanking and keeps ready data through a failure", async (t) => {
  const backend = fakeBackend(t, (url) =>
    Response.json({ id: url.pathname.split("/").at(-1) }),
  )
  const view = mountQuery(t, { keepPrevious: true })
  const first = deferred()
  backend.gate = first.promise
  await view.render("task-1")
  assert.deepEqual(view.latest(), { status: "loading", refreshing: false })
  first.resolve()
  await waitFor(() => view.latest().status === "ready")

  const refresh = deferred()
  backend.gate = refresh.promise
  await view.render("task-2")
  assert.deepEqual(view.latest(), { status: "ready", data: { id: "task-1" }, refreshing: true })
  backend.respond = () => new Response("", { status: 503 })
  refresh.resolve()
  await waitFor(() => view.latest().status === "failed")
  assert.deepEqual(view.latest(), {
    status: "failed",
    failure: { kind: "unavailable", retryable: true, status: 503 },
    data: { id: "task-1" },
    refreshing: false,
  })

  // A disabled read keeps the last answer and is not refreshing.
  await view.render(null)
  assert.equal(view.latest().status, "failed")
  assert.equal(view.latest().refreshing, false)
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

function mountQuery(t: TestContext, options?: { keepPrevious: boolean }) {
  const dom = new JSDOM("<!doctype html><html><body></body></html>")
  Object.defineProperties(globalThis, {
    window: { configurable: true, value: dom.window },
    document: { configurable: true, value: dom.window.document },
    IS_REACT_ACT_ENVIRONMENT: { configurable: true, value: true },
  })
  t.after(() => dom.window.close())
  const root = createRoot(dom.window.document.createElement("div"))
  const seen: {
    taskID: string | null
    query: PortalQuery<{ id: string }> & { retry: () => void; refreshing: boolean }
  }[] = []
  function Probe({ taskID }: { taskID: string | null }) {
    const query = usePortalQuery(
      taskID === null ? null : `task:${taskID}`,
      (transport) => readTask({ ...transport, path: { taskId: taskID ?? "" } }),
      options,
    )
    seen.push({ taskID, query })
    return null
  }
  const withoutRetry = (query: object) =>
    Object.fromEntries(Object.entries(query).filter(([key]) => key !== "retry"))
  return {
    root,
    render: (taskID: string | null) =>
      act(async () => root.render(<Probe taskID={taskID} />)),
    latest: () => withoutRetry(seen.at(-1)!.query),
    first: (taskID: string) =>
      withoutRetry(seen.find((entry) => entry.taskID === taskID)!.query),
    retry: () => seen.at(-1)!.query.retry(),
  }
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
