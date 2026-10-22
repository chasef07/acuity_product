import assert from "node:assert/strict"
import test, { type TestContext } from "node:test"
import { act } from "react"
import { createRoot } from "react-dom/client"
import { JSDOM } from "jsdom"

import { readTask } from "../api/generated/sdk.gen.ts"
import { clearAccessToken } from "../auth-client.ts"
import { DataProvider } from "./provider.tsx"
import { useLiveRefresh, usePages, useRead } from "./queries.ts"

test("a read shows loading per identity, aborts a superseded read, keeps data through a failed retry, and retries a failed load", async (t) => {
  const backend = fakeBackend(t, (url) => Response.json({ id: url.pathname.split("/").at(-1) }))
  const view = mountTask(t)
  const first = deferred()
  backend.gate = first.promise
  await view.render({ taskID: "task-1" })
  assert.deepEqual(view.latest(), { status: "loading" })
  await waitFor(() => backend.requests.length === 1)

  backend.gate = undefined
  await view.render({ taskID: "task-2" })
  assert.deepEqual(view.latest(), { status: "loading" })
  await waitFor(() => view.latest().status === "ready")
  assert.deepEqual(view.latest(), { status: "ready", data: { id: "task-2" }, refreshing: false })
  assert.equal(backend.aborted, 1)

  first.resolve()
  await settle()
  assert.deepEqual(view.latest(), { status: "ready", data: { id: "task-2" }, refreshing: false })

  backend.respond = () => Response.json({ error: { code: "TASK_VERSION_CONFLICT", message: "Task changed.", correlationId: "synthetic-correlation", retryable: false } }, { status: 409 })
  await act(async () => void view.retry())
  await waitFor(() => view.latest().refreshFailure !== undefined)
  assert.deepEqual(view.latest(), {
    status: "ready",
    data: { id: "task-2" },
    refreshing: false,
    refreshFailure: {
      kind: "conflict",
      retryable: false,
      status: 409,
      code: "TASK_VERSION_CONFLICT",
      message: "Task changed.",
      correlationId: "synthetic-correlation",
    },
  })

  backend.respond = () => new Response("", { status: 503 })
  await view.render({ taskID: "task-3" })
  await waitFor(() => view.latest().status === "failed")
  assert.deepEqual(view.latest(), { status: "failed", failure: { kind: "unavailable", retryable: true, status: 503 } })
  const pending = deferred()
  backend.gate = pending.promise
  await act(async () => void view.retry())
  await waitFor(() => view.latest().status === "loading")
  assert.deepEqual(backend.requests.slice(-2), ["GET /v1/tasks/task-3", "GET /v1/tasks/task-3"])
  await view.unmount()
  assert.equal(backend.aborted, 2)
  pending.resolve()
})

test("a disabled read reads no token and sends nothing, and retrying it sends nothing", async (t) => {
  const backend = fakeBackend(t, (url) => Response.json({ id: url.pathname.split("/").at(-1) }))
  const view = mountTask(t)
  await view.render({ taskID: "task-1", enabled: false })
  await act(async () => void view.retry())
  await settle()
  assert.deepEqual(view.latest(), { status: "loading" })
  assert.equal(backend.tokenReads, 0)
  assert.deepEqual(backend.requests, [])

  await view.render({ taskID: "task-1" })
  await waitFor(() => view.latest().status === "ready")
  assert.equal(backend.tokenReads, 1)
})

test("a live refresh keeps the data shown, reports a failed refresh beside it, and lost access hides it", async (t) => {
  const backend = fakeBackend(t, (url) => Response.json({ id: url.pathname.split("/").at(-1) }))
  const view = mountTask(t)
  await view.render({ taskID: "task-1" })
  await waitFor(() => view.latest().status === "ready")

  const refresh = deferred()
  backend.gate = refresh.promise
  backend.respond = () => new Response("", { status: 503 })
  await view.render({ taskID: "task-1" }, 1)
  await waitFor(() => backend.requests.length === 2)
  assert.deepEqual(view.latest(), { status: "ready", data: { id: "task-1" }, refreshing: true })
  refresh.resolve()
  await waitFor(() => view.latest().status === "ready" && !view.latest().refreshing)
  assert.deepEqual(view.latest(), {
    status: "ready",
    data: { id: "task-1" },
    refreshing: false,
    refreshFailure: { kind: "unavailable", retryable: true, status: 503 },
  })

  backend.gate = undefined
  backend.respond = () => new Response("", { status: 401 })
  await view.render({ taskID: "task-1" }, 2)
  await waitFor(() => view.latest().status === "failed")
  assert.deepEqual(view.latest(), {
    status: "failed",
    failure: { kind: "unauthenticated", retryable: false, status: 401 },
  })
})

test("a query identity carrying a refresh field does not compile", () => {
  assert.equal(typeof useRefreshFieldsAsIdentity, "function")
})

function useRefreshFieldsAsIdentity(revision: number, detailRevision: number) {
  useRead({
    name: "task",
    // @ts-expect-error a refresh revision is not query identity
    identity: { taskID: "task-1", revision },
    read: (identity, transport) => readTask({ ...transport, path: { taskId: identity.taskID } }),
  })
  usePages({
    name: "tasks",
    // @ts-expect-error refresh counters, hints, and versions are not query identity
    identity: { practiceID: "practice-1", detailRevision, callingHint: 1, scopeGeneration: 1, version: 1 },
    read: () => Promise.resolve({ data: { nextCursor: "" } }),
    combine: (pages) => pages,
  })
}

function fakeBackend(t: TestContext, respond: (url: URL) => Response) {
  process.env.NEXT_PUBLIC_PORTAL_API_URL = "http://portal.test"
  clearAccessToken()
  const backend = {
    tokenReads: 0,
    requests: [] as string[],
    aborted: 0,
    gate: undefined as Promise<void> | undefined,
    respond,
  }
  t.mock.method(globalThis, "fetch", async (input: RequestInfo | URL) => {
    if (input === "/api/auth/token") {
      backend.tokenReads += 1
      return Response.json({ token: "synthetic-token" })
    }
    const request = input as Request
    const url = new URL(request.url)
    backend.requests.push(`${request.method} ${url.pathname}`)
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

function mountTask(t: TestContext) {
  const dom = new JSDOM("<!doctype html><html><body></body></html>")
  Object.defineProperties(globalThis, {
    window: { configurable: true, value: dom.window },
    document: { configurable: true, value: dom.window.document },
    IS_REACT_ACT_ENVIRONMENT: { configurable: true, value: true },
  })
  t.after(() => dom.window.close())
  const root = createRoot(dom.window.document.createElement("div"))
  const seen: ReturnType<typeof useTask>[] = []
  function Probe({ taskID, enabled, live }: { taskID: string; enabled: boolean; live: number }) {
    useLiveRefresh({ workspaceVersion: live, taskCounts: "", detailRevision: live })
    seen.push(useTask(taskID, enabled))
    return null
  }
  return {
    render: ({ taskID, enabled = true }: { taskID: string; enabled?: boolean }, live = 0) =>
      act(async () =>
        root.render(
          <DataProvider>
            <Probe taskID={taskID} enabled={enabled} live={live} />
          </DataProvider>,
        ),
      ),
    unmount: () => act(async () => root.unmount()),
    latest: () => Object.fromEntries(Object.entries(seen.at(-1)!).filter(([key]) => key !== "retry")),
    retry: () => seen.at(-1)!.retry(),
  }
}

function useTask(taskID: string, enabled: boolean) {
  return useRead({
    live: "workspace",
    name: "task",
    identity: { taskID },
    enabled,
    read: (identity, transport) =>
      readTask({ ...transport, path: { taskId: identity.taskID } }),
  })
}

async function settle() {
  await act(async () => new Promise((resolve) => setTimeout(resolve, 0)))
}

async function waitFor(ready: () => boolean) {
  for (let attempt = 0; attempt < 50 && !ready(); attempt++) await settle()
  assert.ok(ready(), "condition was not reached")
}

function deferred() {
  let resolve!: () => void
  const promise = new Promise<void>((done) => {
    resolve = done
  })
  return { promise, resolve }
}
