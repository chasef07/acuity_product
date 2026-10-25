import assert from "node:assert/strict"
import test, { type TestContext } from "node:test"
import { act } from "react"
import { createRoot } from "react-dom/client"
import { JSDOM } from "jsdom"

import { clearAccessToken } from "../auth-client.ts"
import { DataProvider } from "../data/provider.tsx"
import { useLiveRefresh } from "../data/queries.ts"
import {
  flagAgentCallIssue,
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

test("transcript intent and outcome filters reset pagination when only labels change", async (t) => {
  const backend = fakeBackend(t, (_, body) => Response.json({ calls: [call(String(body.intent ?? "all"))], nextCursor: "next" }))
  const view = mountHook(t, useAgentCalls)
  const filters = { practiceID: "practice-1", locationID: "", range: "7d", phone: "", flaggedOnly: false, intent: "prescription_or_refill", outcome: "transferred" } as const
  await view.render(filters)
  await waitFor(() => view.latest().status === "ready")
  assert.equal(backend.requests[0].body?.intent, "prescription_or_refill")
  assert.equal(backend.requests[0].body?.outcome, "transferred")
  await act(async () => void view.latest().loadMore())
  assert.equal(backend.requests.at(-1)?.body?.cursor, "next")
  await view.render({ ...filters, intent: "insurance_question", outcome: "resolved" })
  await waitFor(() => backend.requests.length === 3)
  assert.equal(backend.requests.at(-1)?.body?.cursor, undefined)
  assert.equal(backend.requests.at(-1)?.body?.intent, "insurance_question")
  assert.equal(backend.requests.at(-1)?.body?.outcome, "resolved")
})

test("diagnostics label filters are sent on every page and reset when outcome changes", async (t) => {
  const backend = fakeBackend(t, () => Response.json({ summary, calls: [call("labeled")], nextCursor: "next" }))
  const view = mountHook(t, useAiCallAnalytics)
  const filters = { practiceID: "practice-1", locationID: "location-1", range: "24h", enabled: true, intent: "existing_patient_booking", outcome: "resolved" } as const
  await view.render(filters)
  await waitFor(() => view.latest().state === "ready")
  assert.equal(backend.requests[0].body?.intent, "existing_patient_booking")
  assert.equal(backend.requests[0].body?.outcome, "resolved")
  await act(async () => void view.latest().loadNextPage())
  assert.equal(backend.requests.at(-1)?.body?.intent, "existing_patient_booking")
  assert.equal(backend.requests.at(-1)?.body?.cursor, "next")
  await view.render({ ...filters, outcome: "abandoned" })
  await waitFor(() => backend.requests.length === 3)
  assert.equal(backend.requests.at(-1)?.body?.cursor, undefined)
  assert.equal(backend.requests.at(-1)?.body?.outcome, "abandoned")
})

test("Command Center failure filters intersect labels on every page and reset the cursor when the judge changes", async (t) => {
  const backend = fakeBackend(t, () => Response.json({ summary, calls: [call("failed")], nextCursor: "next" }))
  const view = mountHook(t, useAiCallAnalytics)
  const filters = { practiceID: "practice-1", locationID: "", range: "24h", enabled: true, failuresOnly: true, failingJudge: "transfer_policy_followed", intent: "speak_to_staff", outcome: "resolved" } as const
  await view.render(filters)
  await waitFor(() => view.latest().state === "ready")
  assert.equal(backend.requests[0].body?.failuresOnly, true)
  assert.equal(backend.requests[0].body?.failingJudge, "transfer_policy_followed")
  assert.equal(backend.requests[0].body?.intent, "speak_to_staff")
  await act(async () => void view.latest().loadNextPage())
  assert.equal(backend.requests.at(-1)?.body?.cursor, "next")
  assert.equal(backend.requests.at(-1)?.body?.failingJudge, "transfer_policy_followed")
  await view.render({ ...filters, failingJudge: "call_ended_correctly" })
  await waitFor(() => backend.requests.length === 3)
  assert.equal(backend.requests.at(-1)?.body?.cursor, undefined)
  assert.equal(backend.requests.at(-1)?.body?.failingJudge, "call_ended_correctly")
  assert.equal(backend.requests.at(-1)?.body?.outcome, "resolved")
})

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
  const filters = { practiceID: "practice-1", locationID: "", range: "7d", phone: "", flaggedOnly: false } as const
  await view.render(filters)
  await waitFor(() => view.latest().status === "ready")
  assert.deepEqual(backend.requests[0], {
    path: "/v1/agent-calls/query",
    body: { practiceId: "practice-1", range: "7d", phone: "", flaggedOnly: false, limit: 50 },
  })

  await act(async () => void view.latest().loadMore())
  await waitFor(() => pages.has("all-page"))
  await waitFor(() => view.latest().loadingMore)

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
  await waitFor(() => {
    const updated = view.latest()
    return updated.status === "ready" && updated.data.calls[1].issueFlagged
  })
})

test("call refresh retains rows, cancels old pagination, loads more after the refresh, and clears rows on scope changes and denial", async (t) => {
  const pending: ReturnType<typeof deferred<Response>>[] = []
  const backend = fakeBackend(t, () => {
    const response = deferred<Response>()
    pending.push(response)
    return response.promise
  })
  const view = mountHook(t, useAgentCalls)
  const filters = { practiceID: "practice-1", locationID: "office-1", range: "24h", phone: "", flaggedOnly: false } as const
  await view.render(filters)
  await waitFor(() => pending.length === 1)
  assert.equal(view.latest().status, "loading")
  pending[0].resolve(Response.json({ calls: [call("original")], nextCursor: "next" }))
  await waitFor(() => view.latest().status === "ready")

  await act(async () => void view.latest().loadMore())
  await waitFor(() => pending.length === 2)
  await waitFor(() => view.latest().loadingMore)
  await view.render(filters, 1)
  await waitFor(() => pending.length === 3)
  assert.equal(backend.aborted, 1)
  const refreshing = view.latest()
  assert.equal(refreshing.status, "ready")
  assert.equal(refreshing.status === "ready" && refreshing.data.calls[0].id, "original")
  assert.equal(refreshing.loadingMore, false)
  let pageAfterRefresh: Promise<unknown> = Promise.resolve()
  await act(async () => {
    pageAfterRefresh = refreshing.loadMore()
  })
  assert.equal(pending.length, 3, "load more waits for the refresh instead of racing it")
  pending[1].resolve(Response.json({ calls: [call("stale-page")], nextCursor: "" }))
  pending[2].resolve(Response.json({ calls: [call("new"), call("original")], nextCursor: "next-new" }))
  await waitFor(() => pending.length === 4)
  assert.equal(backend.requests.at(-1)?.body?.cursor, "next-new")
  pending[3].resolve(Response.json({ calls: [call("older")], nextCursor: "" }))
  await waitFor(() => {
    const current = view.latest()
    return current.status === "ready" && current.data.calls.length === 3
  })
  const updated = view.latest()
  assert.deepEqual(updated.status === "ready" && updated.data.calls.map((item) => item.id), ["new", "original", "older"])
  assert.deepEqual(await pageAfterRefresh, { calls: [call("older")], nextCursor: "" })

  await view.render({ ...filters, locationID: "office-2" }, 2)
  await waitFor(() => pending.length === 5)
  assert.equal(view.latest().status, "loading")
  pending[4].resolve(Response.json({ calls: [call("other-office")], nextCursor: "" }))
  await waitFor(() => view.latest().status === "ready")
  await view.render({ ...filters, locationID: "office-2" }, 3)
  await waitFor(() => pending.length === 6)
  pending[5].resolve(new Response("", { status: 403 }))
  await waitFor(() => view.latest().status === "failed")
  assert.equal("data" in view.latest(), false)
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
    enabled: true,
  } as const

  await view.render(filters)
  await waitFor(() => view.latest().state !== "loading")
  assert.equal(view.latest().state, "unavailable")

  answer = () => new Response("{}", { status: 403 })
  await act(async () => void view.latest().retry())
  await waitFor(() => view.latest().state === "denied")

  answer = (body) =>
    body.cursor
      ? new Response("{}", { status: 503 })
      : Response.json({ summary, calls: [call("ai-1")], nextCursor: "next" })
  await act(async () => void view.latest().retry())
  await waitFor(() => view.latest().state === "ready")
  assert.deepEqual(backend.requests.at(-1)?.body, {
    practiceId: "practice-1",
    locationId: "location-1",
    range: "24h",
    limit: 50,
  })

  await act(async () => void (await view.latest().loadNextPage()))
  await waitFor(() => view.latest().nextPage === "unavailable")
  assert.equal(view.latest().state, "ready")

  answer = (body) =>
    body.cursor
      ? new Response("{}", { status: 401 })
      : Response.json({ summary, calls: [call("ai-1")], nextCursor: "next" })
  await act(async () => void (await view.latest().loadNextPage()))
  await waitFor(() => view.latest().state === "denied")
  assert.equal(view.latest().data, undefined)

  await act(async () => void view.latest().retry())
  await waitFor(() => view.latest().state === "ready")
  const sent = backend.requests.length
  await view.render({ ...filters, enabled: false })
  await settle()
  assert.equal(backend.requests.length, sent)
  assert.equal(view.latest().state, "ready")
  await view.render(filters)
  assert.equal(view.latest().state, "ready")
  await waitFor(() => backend.requests.length === sent + 1)
})

test("analytics stays denied through a pending refresh after pagination loses authorization", async (t) => {
  const refresh = deferred<Response>()
  let firstPageRequests = 0
  fakeBackend(t, (_, body) => {
    if (body.cursor) return new Response("", { status: 401 })
    if (++firstPageRequests > 1) return refresh.promise
    return Response.json({ summary, calls: [call("original")], nextCursor: "next" })
  })
  const view = mountHook(t, useAiCallAnalytics)
  const filters = { practiceID: "practice-1", locationID: "office-1", range: "24h", enabled: true } as const
  await view.render(filters)
  await waitFor(() => view.latest().state === "ready")
  await act(async () => void (await view.latest().loadNextPage()))
  await waitFor(() => view.latest().state === "denied")
  assert.equal(view.latest().data, undefined)
  await act(async () => void view.latest().retry())
  await waitFor(() => firstPageRequests === 2)
  assert.equal(view.latest().state, "denied")
  assert.equal(view.latest().data, undefined)
  refresh.resolve(Response.json({ summary, calls: [call("authorized")], nextCursor: "" }))
  await waitFor(() => view.latest().state === "ready")
  assert.equal(view.latest().data?.calls[0].id, "authorized")
})

test("call commands report conflicts and send the saved choice", async (t) => {
  const backend = fakeBackend(t, () => new Response("{}", { status: 409 }))
  const flagged = await flagAgentCallIssue("call-1", "OTHER", "")
  assert.equal(!flagged.ok && flagged.failure.kind, "conflict")
  assert.deepEqual(backend.requests, [
    { path: "/v1/agent-calls/call-1/issue", body: { reason: "OTHER", note: "" } },
  ])

  const view = mountHook(t, useAiCallEvidence)
  await view.render("")
  await settle()
  assert.equal(view.latest().status, "loading")
  assert.equal(backend.requests.length, 1)
})

function fakeBackend(
  t: TestContext,
  respond: (path: string, body: Record<string, unknown>) => Response | Promise<Response>,
) {
  process.env.NEXT_PUBLIC_PORTAL_API_URL = "http://portal.test"
  clearAccessToken()
  const backend = {
    requests: [] as { path: string; body?: Record<string, unknown> }[],
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
  function Probe({ props, live }: { props: P; live: number }) {
    useLiveRefresh({ workspaceVersion: live, taskCounts: "", detailRevision: live })
    seen.push(useHook(props))
    return null
  }
  return {
    render: (props: P, live = 0) =>
      act(async () => root.render(<DataProvider><Probe props={props} live={live} /></DataProvider>)),
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
