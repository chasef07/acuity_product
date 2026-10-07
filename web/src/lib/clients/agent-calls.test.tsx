import assert from "node:assert/strict"
import test, { type TestContext } from "node:test"
import { act } from "react"
import { createRoot } from "react-dom/client"
import { JSDOM } from "jsdom"

import { clearAccessToken } from "../auth-client.ts"
import {
  flagAgentCallIssue,
  setCallTag,
  useAgentCalls,
  useAiCallAnalytics,
  useAiCallEvidence,
} from "./agent-calls.ts"

const call = (id: string) => ({
  id,
  phone: "+15555550100",
  startedAt: "2026-01-01T15:00:00Z",
  appointmentActions: [],
  transferred: false,
  issueFlagged: false,
})

const summary = { totalCalls: 3 }

test("recent calls page by cursor and a filter change abandons the old page", async (t) => {
  const pages = new Map<string, ReturnType<typeof deferred<Response>>>()
  const backend = fakeBackend(t, (_, body) => {
    if (!body.cursor) {
      return Response.json({
        calls: [call(body.flaggedOnly ? "flagged-1" : "all-1")],
        nextCursor: body.flaggedOnly ? "flagged-page" : "all-page",
      })
    }
    const page = deferred<Response>()
    pages.set(String(body.cursor), page)
    return page.promise
  })
  const view = mountHook(t, useAgentCalls)
  const filters = { practiceID: "practice-1", locationID: "", range: "7d", phone: "", flaggedOnly: false, revision: 0 } as const
  await view.render(filters)
  await waitFor(() => view.latest().status === "ready")
  assert.deepEqual(backend.requests[0], {
    path: "/v1/agent-calls/query",
    body: { practiceId: "practice-1", range: "7d", phone: "", flaggedOnly: false, limit: 50 },
  })

  await act(async () => void view.latest().loadMore())
  await waitFor(() => pages.has("all-page"))
  assert.equal(view.latest().loadingMore, true)

  await view.render({ ...filters, flaggedOnly: true })
  await waitFor(() => view.latest().status === "ready")
  assert.equal(backend.aborted, 1)
  assert.equal(view.latest().loadingMore, false)

  let appended: Promise<unknown> = Promise.resolve()
  await act(async () => {
    appended = view.latest().loadMore()
  })
  await waitFor(() => pages.has("flagged-page"))
  pages.get("all-page")!.resolve(Response.json({ calls: [call("stale")], nextCursor: "" }))
  await settle()
  assert.equal(view.latest().loadingMore, true)

  pages.get("flagged-page")!.resolve(Response.json({ calls: [call("flagged-2")], nextCursor: "" }))
  await waitFor(() => !view.latest().loadingMore)
  const ready = view.latest()
  assert.equal(ready.status, "ready")
  if (ready.status !== "ready") return
  assert.deepEqual(ready.data.calls.map((item) => item.id), ["flagged-1", "flagged-2"])
  assert.equal(ready.data.nextCursor, "")
  assert.deepEqual(await appended, { calls: [call("flagged-2")], nextCursor: "" })

  await act(async () =>
    ready.update((shown) => ({
      ...shown,
      calls: shown.calls.map((item) => ({ ...item, issueFlagged: item.id === "flagged-2" })),
    })),
  )
  const updated = view.latest()
  assert.equal(updated.status === "ready" && updated.data.calls[1].issueFlagged, true)
})

test("AI call analytics separate denied, unavailable, and next-page failures", async (t) => {
  let answer: (body: Record<string, unknown>) => Response = () =>
    Response.json({ calls: [], nextCursor: "" })
  const backend = fakeBackend(t, (_, body) => answer(body))
  const view = mountHook(t, useAiCallAnalytics)
  const filters = {
    practiceID: "practice-1",
    locationID: "location-1",
    range: "24h",
    manualTag: "",
    revision: 0,
    enabled: true,
  } as const

  await view.render(filters)
  await waitFor(() => view.latest().state !== "loading")
  assert.equal(view.latest().state, "unavailable")

  answer = () => new Response("{}", { status: 403 })
  await view.render({ ...filters, revision: 1 })
  await waitFor(() => view.latest().state !== "loading")
  assert.equal(view.latest().state, "denied")

  answer = (body) =>
    body.cursor
      ? new Response("{}", { status: 503 })
      : Response.json({ summary, availableTags: ["callback"], calls: [call("ai-1")], nextCursor: "next" })
  await view.render({ ...filters, revision: 2 })
  await waitFor(() => view.latest().state === "ready")
  assert.deepEqual(backend.requests.at(-1)?.body, {
    practiceId: "practice-1",
    locationId: "location-1",
    range: "24h",
    limit: 50,
  })

  await act(async () => void (await view.latest().loadNextPage()))
  assert.equal(view.latest().state, "ready")
  assert.equal(view.latest().nextPage, "unavailable")

  answer = (body) =>
    body.cursor
      ? new Response("{}", { status: 401 })
      : Response.json({ summary, calls: [call("ai-1")], nextCursor: "next" })
  await act(async () => void (await view.latest().loadNextPage()))
  assert.equal(view.latest().state, "denied")
  assert.equal(view.latest().data, undefined)

  await view.render({ ...filters, revision: 3 })
  await waitFor(() => view.latest().state === "ready")
  const sent = backend.requests.length
  await view.render({ ...filters, revision: 3, enabled: false })
  await settle()
  assert.equal(backend.requests.length, sent)
  assert.equal(view.latest().state, "ready")
  await view.render({ ...filters, revision: 3 })
  assert.equal(view.latest().state, "ready")
  await waitFor(() => backend.requests.length === sent + 1)
})

test("call commands report conflicts and send the saved choice", async (t) => {
  const backend = fakeBackend(t, (path) =>
    path.endsWith("/issue")
      ? new Response("{}", { status: 409 })
      : Response.json({ available: ["callback"], selected: ["callback"] }),
  )
  const flagged = await flagAgentCallIssue("call-1", "OTHER")
  assert.equal(!flagged.ok && flagged.failure.kind, "conflict")
  const tagged = await setCallTag("call-1", "callback", true)
  assert.deepEqual(tagged, { ok: true, data: { available: ["callback"], selected: ["callback"] } })
  assert.deepEqual(backend.requests, [
    { path: "/v1/agent-calls/call-1/issue", body: { reason: "OTHER" } },
    { path: "/v1/operator/ai-interactions/call-1/manual-tags", body: { name: "callback", applied: true } },
  ])

  const view = mountHook(t, useAiCallEvidence)
  await view.render("")
  await settle()
  assert.equal(view.latest().status, "loading")
  assert.equal(backend.requests.length, 2)
})

function fakeBackend(
  t: TestContext,
  respond: (path: string, body: Record<string, unknown>) => Response | Promise<Response>,
) {
  process.env.NEXT_PUBLIC_PORTAL_API_URL = "http://portal.test"
  clearAccessToken()
  const backend = {
    requests: [] as { path: string; body?: unknown }[],
    aborted: 0,
  }
  t.mock.method(globalThis, "fetch", async (input: RequestInfo | URL) => {
    if (input === "/api/auth/token") return Response.json({ token: "synthetic-token" })
    const request = input as Request
    const path = new URL(request.url).pathname
    const body = request.method === "GET" ? undefined : await request.clone().json()
    backend.requests.push(body === undefined ? { path } : { path, body })
    let answered = false
    return new Promise<Response>((resolve, reject) => {
      request.signal.addEventListener("abort", () => {
        if (answered) return
        answered = true
        backend.aborted += 1
        reject(request.signal.reason)
      })
      void Promise.resolve(respond(path, body ?? {})).then((response) => {
        if (answered) return
        answered = true
        resolve(response)
      })
    })
  })
  return backend
}

function mountHook<P, R>(t: TestContext, useHook: (props: P) => R) {
  const dom = new JSDOM("<!doctype html><html><body></body></html>")
  Object.defineProperties(globalThis, {
    window: { configurable: true, value: dom.window },
    document: { configurable: true, value: dom.window.document },
    IS_REACT_ACT_ENVIRONMENT: { configurable: true, value: true },
  })
  const root = createRoot(dom.window.document.createElement("div"))
  t.after(async () => {
    await act(async () => root.unmount())
    dom.window.close()
  })
  const seen: R[] = []
  function Probe({ props }: { props: P }) {
    seen.push(useHook(props))
    return null
  }
  return {
    render: (props: P) => act(async () => root.render(<Probe props={props} />)),
    latest: () => seen.at(-1)!,
  }
}

async function settle() {
  await act(async () => new Promise((resolve) => setTimeout(resolve, 0)))
}

async function waitFor(ready: () => boolean) {
  for (let attempt = 0; attempt < 50 && !ready(); attempt++) await settle()
  assert.ok(ready(), "condition was not reached")
}

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((done) => {
    resolve = done
  })
  return { promise, resolve }
}
