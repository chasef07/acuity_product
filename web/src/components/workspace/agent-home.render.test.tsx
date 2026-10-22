import assert from "node:assert/strict"
import test from "node:test"
import { act } from "react"
import { hydrateRoot } from "react-dom/client"
import { renderToString } from "react-dom/server"
import { JSDOM } from "jsdom"

import { AgentHome } from "./agent-home"
import { clearAccessToken } from "../../lib/auth-client"
import { DataProvider } from "../../lib/data/provider"

test("Home hydrates its greeting and shows knowledge status without loading call gaps", async (context) => {
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
  context.mock.method(globalThis, "fetch", async (input: RequestInfo | URL) => {
    if (input === "/api/auth/token") return Response.json({ token: "synthetic-token" })
    const request = input as Request
    const pathname = new URL(request.url).pathname
    if (pathname === "/v1/knowledge/query") return Response.json({ locationId: "office-1", sections: [] })
    assert.fail(`Unexpected Home request: ${pathname}`)
  })
  const host = document.createElement("div")
  document.body.append(host)
  const home = <DataProvider><AgentHome practiceID="practice-1" locationID="office-1" onPage={() => {}} /></DataProvider>
  host.innerHTML = renderToString(home)
  assert.equal(host.querySelector("h1")?.textContent, "Welcome,")
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
  for (let attempt = 0; attempt < 50 && !/No knowledge published yet/.test(host.textContent ?? ""); attempt++) {
    await act(async () => new Promise((resolve) => setTimeout(resolve, 0)))
  }
  assert.match(host.textContent ?? "", /No knowledge published yet/)
  assert.doesNotMatch(host.textContent ?? "", /Needs knowledge|Load more calls|office question went unanswered/i)
})
