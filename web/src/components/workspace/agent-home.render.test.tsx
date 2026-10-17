import assert from "node:assert/strict"
import test from "node:test"
import { act } from "react"
import { hydrateRoot } from "react-dom/client"
import { renderToString } from "react-dom/server"
import { JSDOM } from "jsdom"

import { AgentHome } from "./agent-home"
import { clearAccessToken } from "../../lib/auth-client"
import type { AgentCallsQuery } from "../../lib/api/generated/types.gen"

test("Home exposes a failed next page, retains loaded calls, and clears the error after retry", async (context) => {
  const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost/workspace" })
  Object.defineProperties(globalThis, {
    window: { configurable: true, value: dom.window },
    document: { configurable: true, value: dom.window.document },
    navigator: { configurable: true, value: dom.window.navigator },
    HTMLElement: { configurable: true, value: dom.window.HTMLElement },
    Element: { configurable: true, value: dom.window.Element },
    Node: { configurable: true, value: dom.window.Node },
    MouseEvent: { configurable: true, value: dom.window.MouseEvent },
    getComputedStyle: { configurable: true, value: dom.window.getComputedStyle.bind(dom.window) },
    IS_REACT_ACT_ENVIRONMENT: { configurable: true, value: true },
    requestAnimationFrame: { configurable: true, value: () => 0 },
    cancelAnimationFrame: { configurable: true, value: () => {} },
  })
  Object.defineProperty(dom.window, "matchMedia", {
    configurable: true,
    value: () => ({ addEventListener() {}, matches: true, removeEventListener() {} }),
  })
  process.env.NEXT_PUBLIC_PORTAL_API_URL = "http://portal.test"
  clearAccessToken()
  const queries: AgentCallsQuery[] = []
  let failNextPage = true
  context.mock.method(globalThis, "fetch", async (input: RequestInfo | URL) => {
    if (input === "/api/auth/token") return Response.json({ token: "synthetic-token" })
    const request = input as Request
    const pathname = new URL(request.url).pathname
    if (pathname === "/v1/knowledge/query") return Response.json({ locationId: "office-1", sections: [] })
    assert.equal(pathname, "/v1/agent-calls/query")
    const query = await request.json() as AgentCallsQuery
    queries.push(query)
    if (query.cursor && failNextPage) return Response.json({ error: { code: "UNAVAILABLE", message: "More calls temporarily unavailable", correlationId: "synthetic", retryable: true } }, { status: 503 })
    if (query.cursor) return Response.json({
      calls: [{ id: "call-2", phone: "+15555550112", startedAt: "2026-10-13T14:00:00Z", appointmentActions: [], transferred: false, issueFlagged: false, knowledgeGaps: ["Parking"] }],
      nextCursor: "",
    })
    return Response.json({
      calls: [{ id: "call-1", phone: "+15555550111", startedAt: "2026-10-13T15:00:00Z", appointmentActions: [], transferred: false, issueFlagged: false, knowledgeGaps: ["Saturday hours"] }],
      nextCursor: "page-2",
    })
  })
  const host = document.createElement("div")
  document.body.append(host)
  const home = <AgentHome practiceID="practice-1" locationID="office-1" onPage={() => {}} />
  host.innerHTML = renderToString(home)
  assert.equal(host.querySelector("h1")?.textContent, "Welcome")
  assert.equal(host.querySelector("header time")?.textContent, "")
  const hydrationErrors: unknown[] = []
  let root: ReturnType<typeof hydrateRoot> | undefined
  context.after(async () => {
    await act(async () => root?.unmount())
    dom.window.close()
  })
  await act(async () => { root = hydrateRoot(host, home, { onRecoverableError: (error) => hydrationErrors.push(error) }) })
  assert.deepEqual(hydrationErrors, [])
  assert.notEqual(host.querySelector("header time")?.textContent, "")
  await act(async () => host.querySelector<HTMLButtonElement>('button[aria-label="Knowledge base"]')!.click())
  const button = Array.from(host.querySelectorAll("button")).find((item) => item.textContent === "Load more calls")
  assert.ok(button)
  await act(async () => button.click())
  assert.equal(queries.length, 2)
  assert.equal(queries[1].cursor, "page-2")
  assert.match(host.textContent ?? "", /Saturday hours/)
  assert.equal(button.disabled, false)
  assert.match(host.querySelector("[role='alert']")?.textContent ?? "", /unavailable|could not|try again/i)
  failNextPage = false
  await act(async () => button.click())
  assert.equal(queries.length, 3)
  assert.equal(queries[2].cursor, "page-2")
  assert.match(host.textContent ?? "", /Saturday hours/)
  assert.match(host.textContent ?? "", /Parking/)
  assert.equal(host.querySelector("[role='alert']"), null)
  assert.equal(host.contains(button), false)
})
