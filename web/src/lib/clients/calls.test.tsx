import assert from "node:assert/strict"
import test, { type TestContext } from "node:test"
import { act } from "react"
import { createRoot } from "react-dom/client"
import { JSDOM } from "jsdom"

import type { Task } from "../api/generated/types.gen.ts"
import { clearAccessToken } from "../auth-client.ts"
import {
  issueRecordingPlayback,
  useRecoverySource,
  useTaskCallEligibility,
} from "./calls.ts"

const recoveryTask = {
  id: "task-1",
  version: 3,
  callId: "call-missed",
  state: "OPEN",
} as Pick<Task, "id" | "version" | "callId" | "state">

const interactions = [
  { callId: "call-missed", type: "MISSED_CALL", occurredAt: "2026-09-01T10:00:00Z" },
  { callId: "call-voicemail", type: "VOICEMAIL", occurredAt: "2026-09-01T09:00:00Z" },
]

test("playback resolves with the capability URL for each recording kind", async (t) => {
  const backend = fakeBackend(t, () => Response.json({ token: "a/b", expiresAt: "2026-09-30T00:00:00Z" }))
  assert.deepEqual(await issueRecordingPlayback("voicemail", "call-1"), {
    ok: true,
    data: "http://portal.test/v1/calling/voicemail-playback/a%2Fb",
  })
  assert.deepEqual(await issueRecordingPlayback("call", "call-1"), {
    ok: true,
    data: "http://portal.test/v1/calling/recording-playback/a%2Fb",
  })
  assert.deepEqual(backend.requests, [
    "POST /v1/calling/calls/call-1/voicemail-playback",
    "POST /v1/calling/calls/call-1/recording-playback",
  ])

  backend.respond = () => new Response("", { status: 403 })
  assert.deepEqual(await issueRecordingPlayback("voicemail", "call-1"), {
    ok: false,
    failure: { kind: "unauthorized", retryable: false, status: 403 },
  })
})


test("recovery source loads the newest voicemail and keeps it through a failed refresh", async (t) => {
  const backend = fakeBackend(t, (url) =>
    url.pathname.startsWith("/v1/tasks/")
      ? Response.json({ interactions })
      : Response.json({ id: url.pathname.split("/").at(-1) }),
  )
  const view = mount(t, (revision: number) => useRecoverySource(recoveryTask, revision))
  await view.render(0)
  await waitFor(() => view.latest().call !== undefined)
  assert.deepEqual(view.latest(), { interactions, call: { id: "call-voicemail" } })
  assert.deepEqual(backend.requests, [
    "GET /v1/tasks/task-1",
    "GET /v1/calling/calls/call-voicemail",
  ])

  backend.respond = (url) =>
    url.pathname.startsWith("/v1/tasks/")
      ? Response.json({ interactions })
      : new Response("", { status: 503 })
  await view.render(1)
  await waitFor(() => view.latest().failure !== undefined)
  assert.deepEqual(view.latest(), {
    interactions,
    call: { id: "call-voicemail" },
    failure: { kind: "unavailable", retryable: true, status: 503 },
  })
  // The call reloads once, after the refreshed Task read lands.
  assert.deepEqual(backend.requests.slice(2), [
    "GET /v1/tasks/task-1",
    "GET /v1/calling/calls/call-voicemail",
  ])
})

test("call eligibility waits until enabled and keeps its answer while rechecking", async (t) => {
  const backend = fakeBackend(t, () => Response.json({ eligible: true, reason: "Ready to call." }))
  const view = mount(t, (props: { enabled: boolean; historyHint: number }) =>
    useTaskCallEligibility({ task: recoveryTask, ...props }),
  )
  await view.render({ enabled: false, historyHint: 0 })
  await act(async () => new Promise((resolve) => setTimeout(resolve, 0)))
  assert.equal(view.latest().status, "loading")
  assert.deepEqual(backend.requests, [])

  await view.render({ enabled: true, historyHint: 0 })
  await waitFor(() => view.latest().status !== "loading")
  const answer = { eligible: true, reason: "Ready to call." }
  const ready = view.latest()
  assert.deepEqual(ready.status === "ready" && ready.data, answer)

  let answer503!: () => void
  const recheck = new Promise<void>((resolve) => (answer503 = resolve))
  backend.respond = async () => {
    await recheck
    return new Response("", { status: 503 })
  }
  await view.render({ enabled: true, historyHint: 1 })
  assert.equal(view.latest().status, "ready")
  assert.equal(view.latest().refreshing, true)
  answer503()
  await waitFor(() => view.latest().status === "failed")
  assert.deepEqual(backend.requests, [
    "GET /v1/calling/tasks/task-1/eligibility",
    "GET /v1/calling/tasks/task-1/eligibility",
  ])
})

function fakeBackend(t: TestContext, respond: (url: URL) => Response | Promise<Response>) {
  process.env.NEXT_PUBLIC_PORTAL_API_URL = "http://portal.test"
  clearAccessToken()
  const backend = { requests: [] as string[], respond }
  t.mock.method(globalThis, "fetch", async (input: RequestInfo | URL) => {
    if (input === "/api/auth/token") {
      return Response.json({ token: "synthetic-token" })
    }
    const request = input as Request
    const url = new URL(request.url)
    backend.requests.push(`${request.method} ${url.pathname}`)
    return backend.respond(url)
  })
  return backend
}

function mount<P, R>(t: TestContext, hook: (props: P) => R) {
  const dom = new JSDOM("<!doctype html><html><body></body></html>")
  Object.defineProperties(globalThis, {
    window: { configurable: true, value: dom.window },
    document: { configurable: true, value: dom.window.document },
    IS_REACT_ACT_ENVIRONMENT: { configurable: true, value: true },
  })
  const root = createRoot(dom.window.document.createElement("div"))
  t.after(() => {
    act(() => root.unmount())
    dom.window.close()
  })
  const seen: R[] = []
  function Probe({ props }: { props: P }) {
    seen.push(hook(props))
    return null
  }
  return {
    render: (props: P) => act(async () => root.render(<Probe props={props} />)),
    latest: () => seen.at(-1) as R,
  }
}

async function waitFor(ready: () => boolean) {
  for (let attempt = 0; attempt < 50 && !ready(); attempt++) {
    await act(async () => new Promise((resolve) => setTimeout(resolve, 0)))
  }
  assert.ok(ready(), "condition was not reached")
}
