import assert from "node:assert/strict"
import test from "node:test"
import { act } from "react"
import { createRoot } from "react-dom/client"
import { JSDOM } from "jsdom"

import { clearAccessToken } from "../auth-client.ts"
import { DataProvider } from "../data/provider.tsx"
import { useScorecardQuestions } from "./call-review.ts"

test("named scorecard versions are read once for the session; the current scorecard is read again on remount", async (t) => {
  const requests: string[] = []
  process.env.NEXT_PUBLIC_PORTAL_API_URL = "http://portal.test"
  clearAccessToken()
  t.mock.method(globalThis, "fetch", async (input: RequestInfo | URL) => {
    if (input === "/api/auth/token") return Response.json({ token: "synthetic-token" })
    const url = new URL((input as Request).url)
    requests.push(`${url.pathname}?${url.searchParams}`)
    return Response.json({ questions: [] })
  })
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
  const ready = new Map<string, boolean>()
  function Questions({ version }: { version?: string }) {
    ready.set(version ?? "", useScorecardQuestions(version).status === "ready")
    return null
  }
  const render = (versions: (string | undefined)[]) =>
    act(async () =>
      root.render(
        <DataProvider>
          {versions.map((version, index) => (
            <Questions key={index} version={version} />
          ))}
        </DataProvider>,
      ),
    )
  const versions = [undefined, "typesafe-scorecard-v12", "typesafe-scorecard-v11"]

  await render(versions)
  await waitFor(() => versions.every((version) => ready.get(version ?? "")))
  assert.equal(requests.length, 3)

  ready.clear()
  await render([])
  await render(versions)
  await waitFor(() => versions.every((version) => ready.get(version ?? "")))
  await act(async () => new Promise((resolve) => setTimeout(resolve, 0)))
  assert.deepEqual(requests.slice(3), ["/v1/operator/scorecard/questions?"])
})

async function waitFor(ready: () => boolean) {
  for (let attempt = 0; attempt < 50 && !ready(); attempt++) {
    await act(async () => new Promise((resolve) => setTimeout(resolve, 0)))
  }
  assert.ok(ready(), "condition was not reached")
}
