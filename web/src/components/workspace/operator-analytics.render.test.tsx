import assert from "node:assert/strict"
import test from "node:test"
import { act, type ReactNode } from "react"
import { createRoot } from "react-dom/client"
import { JSDOM } from "jsdom"

import { clearAccessToken } from "../../lib/auth-client"
import { DataProvider } from "../../lib/data/provider"
import { useLiveRefresh } from "../../lib/data/queries"

test("Command Center keeps the same call table during and after automatic refresh", async (t) => {
  const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost/workspace?view=operator-analytics" })
  Object.defineProperties(globalThis, {
    window: { configurable: true, value: dom.window },
    document: { configurable: true, value: dom.window.document },
    navigator: { configurable: true, value: dom.window.navigator },
    HTMLElement: { configurable: true, value: dom.window.HTMLElement },
    Element: { configurable: true, value: dom.window.Element },
    Node: { configurable: true, value: dom.window.Node },
    getComputedStyle: { configurable: true, value: dom.window.getComputedStyle.bind(dom.window) },
    IS_REACT_ACT_ENVIRONMENT: { configurable: true, value: true },
    requestAnimationFrame: { configurable: true, value: () => 0 },
    cancelAnimationFrame: { configurable: true, value: () => {} },
  })
  Object.defineProperty(dom.window, "matchMedia", { configurable: true, value: () => ({ matches: false, addEventListener() {}, removeEventListener() {} }) })
  process.env.NEXT_PUBLIC_PORTAL_API_URL = "http://portal.test"
  clearAccessToken()
  const pending: ((response: Response) => void)[] = []
  t.mock.method(globalThis, "fetch", async (input: RequestInfo | URL) => {
    if (input === "/api/auth/token") return Response.json({ token: "synthetic-token" })
    const url = new URL((input as Request).url)
    if (url.pathname === "/v1/operator/scorecard/questions") return Response.json({ questions: [] })
    assert.equal(url.pathname, "/v1/operator/ai-analytics/query")
    return new Promise<Response>((resolve) => pending.push(resolve))
  })
  const { OperatorAnalytics } = await import("./operator-analytics")
  const host = document.createElement("div")
  document.body.append(host)
  const root = createRoot(host)
  t.after(async () => {
    await act(async () => root.unmount())
    dom.window.close()
  })
  const render = (version: number, locationID = "office-1") => act(async () => root.render(
    <DataProvider>
      <LiveWorkspace version={version}>
        <OperatorAnalytics practiceID="practice-1" locationScopeID={locationID} tab="calls" />
      </LiveWorkspace>
    </DataProvider>,
  ))
  const result = (ids: string[]) => Response.json({
    summary: { totalCalls: ids.length },
    calls: ids.map((id) => ({ id, phone: "+15555550100", startedAt: "2026-10-19T12:00:00Z", locationName: "Synthetic Office", durationSeconds: 30, toolActions: [], toolErrorCount: 0, transferred: false, transcriptAvailable: true })),
    nextCursor: "",
  })
  await render(1)
  await waitFor(() => pending.length === 1)
  await act(async () => pending[0](result(["original"])))
  await waitFor(() => host.querySelectorAll("tbody tr").length === 1)
  const table = host.querySelector("table")
  const originalRow = host.querySelector("tbody tr")
  await render(2)
  await waitFor(() => pending.length === 2)
  assert.equal(host.querySelector("table") === table, true, "live update must not replace the table with loading placeholders")
  assert.equal(host.querySelector("tbody tr") === originalRow, true)
  await act(async () => pending[1](result(["new", "original"])))
  await waitFor(() => host.querySelectorAll("tbody tr").length === 2)
  assert.equal(host.querySelector("table") === table, true, "completed refresh must not remount the table")
  assert.equal(host.querySelectorAll("tbody tr")[1] === originalRow, true)
  assert.equal(host.querySelector('[aria-label="Refresh calls"]'), null)

  await render(3, "office-2")
  await waitFor(() => pending.length === 3)
  assert.equal(host.querySelector("table"), null)
  await act(async () => pending[2](new Response("", { status: 403 })))
  await waitFor(() => host.textContent!.includes("Analytics access unavailable"))
  assert.equal(host.querySelector("table"), null)
})

function LiveWorkspace({ version, children }: { version: number; children: ReactNode }) {
  useLiveRefresh({ workspaceVersion: version, taskCounts: "", detailRevision: 0 })
  return children
}

async function waitFor(ready: () => boolean) {
  for (let attempt = 0; attempt < 50 && !ready(); attempt++) {
    await act(async () => new Promise((resolve) => setTimeout(resolve, 0)))
  }
  assert.ok(ready(), "condition was not reached")
}
