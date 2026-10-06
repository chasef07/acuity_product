import assert from "node:assert/strict"
import test, { type TestContext } from "node:test"
import { act } from "react"
import { createRoot } from "react-dom/client"
import { JSDOM } from "jsdom"

import type { Task } from "../api/generated/types.gen.ts"
import { clearAccessToken } from "../auth-client.ts"
import { useReviewFolder } from "./tasks.ts"

const origin = Date.UTC(2026, 8, 1, 12)

function row(id: string, minutesAgo: number) {
  return { id, updatedAt: new Date(origin - minutesAgo * 60_000).toISOString() } as Task
}

test("an expanded review folder refreshes only its first page and keeps rows loaded deeper", async (t) => {
  let rows = Array.from({ length: 75 }, (_, index) => row(`text-${String(index).padStart(2, "0")}`, index))
  const cursors: string[] = []
  fakeBackend(t, (body) => {
    cursors.push(body.cursor ?? "")
    const start = Number(body.cursor || 0)
    const end = start + body.limit
    return { items: rows.slice(start, end), nextCursor: end < rows.length ? String(end) : "" }
  })
  const view = mount(t, (revision: number) =>
    useReviewFolder({ practiceID: "practice-1", locationID: "", search: "", kind: "texts", revision, count: 0 }))
  await view.render(0)
  await waitFor(() => view.latest().items.length === 50)
  await act(async () => view.latest().showMore())
  await waitFor(() => view.latest().items.length === 75)

  rows = [row("text-new", -5), ...rows.filter((item) => item.id !== "text-03")]
  await view.render(1)
  await waitFor(() => view.latest().items[0]?.id === "text-new")
  const ids = view.latest().items.map((item) => item.id)
  assert.deepEqual(cursors, ["", "50", ""])
  assert.equal(ids.length, 75)
  assert.equal(new Set(ids).size, 75)
  assert.equal(ids.includes("text-03"), false)
  assert.equal(ids.at(-1), "text-74")
  assert.equal(view.latest().nextCursor, "")
})

function fakeBackend(
  t: TestContext,
  respond: (body: { cursor?: string; limit: number }) => { items: Task[]; nextCursor: string },
) {
  process.env.NEXT_PUBLIC_PORTAL_API_URL = "http://portal.test"
  clearAccessToken()
  t.mock.method(globalThis, "fetch", async (input: RequestInfo | URL) => {
    if (input === "/api/auth/token") {
      return Response.json({ token: "synthetic-token" })
    }
    const request = input as Request
    assert.equal(new URL(request.url).pathname, "/v1/tasks/query")
    return Response.json(respond(await request.json()))
  })
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
