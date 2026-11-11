import assert from "node:assert/strict"
import test from "node:test"
import { act } from "react"
import { JSDOM } from "jsdom"

import type { CallingCall, Task } from "../../lib/api/generated/types.gen.ts"
import { clearAccessToken } from "../../lib/auth-client.ts"
import type { WrapUpRequest } from "../../lib/calling/call-wrap-up.ts"

const task: Task = {
  id: "task-refill",
  practiceId: "practice-1",
  locationId: "location-north",
  locationName: "North Office",
  phone: "+15551234567",
  title: "Synthetic refill request",
  category: "medication",
  state: "OPEN",
  origin: "ABITA_AI",
  urgency: "normal",
  createdBy: { kind: "SERVICE", subject: "abita" },
  createdAt: "2026-08-30T12:00:00Z",
  version: 1,
  updatedAt: "2026-08-30T12:00:00Z",
  relatedInteractionCount: 0,
  interactions: [],
}

const call: CallingCall = {
  id: "call-1",
  practiceId: "practice-1",
  locationId: "location-north",
  locationName: "North Office",
  direction: "INBOUND",
  entryPoint: "STANDALONE",
  state: "NEEDS_DISPOSITION",
  phone: "+15551234567",
  phoneSource: "",
  displayName: "",
  nameSource: "",
  transferReason: "",
  reasonSource: "",
  providerTermination: "",
  endRequested: false,
  callerId: "",
  retryAllowed: false,
  connectedAt: "2026-08-30T14:00:00Z",
  version: 3,
}

test("the wrap-up is a dialog named for the patient that Escape and outside clicks cannot dismiss", async (t) => {
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
  Object.defineProperty(dom.window, "matchMedia", { configurable: true, value: () => ({ matches: false, addEventListener() {}, removeEventListener() {} }) })
  const previousAPIURL = process.env.NEXT_PUBLIC_PORTAL_API_URL
  process.env.NEXT_PUBLIC_PORTAL_API_URL = "http://portal.test"
  clearAccessToken()
  t.mock.method(Date, "now", () => Date.parse("2026-08-30T14:03:42Z"))
  t.mock.method(globalThis, "fetch", async (input: RequestInfo | URL) => {
    const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url
    if (url === "/api/auth/token") return Response.json({ token: "synthetic-token" })
    assert.match(url, /\/v1\/engagements\/%2B15551234567\/summary\?practiceId=practice-1/)
    return Response.json({
      phone: "+15551234567", displayName: "Rosa Synthetic", names: ["Rosa Synthetic"],
      openTasks: [task], openTaskCount: 1, closedToday: [],
    })
  })
  const host = document.createElement("div")
  document.body.append(host)
  const { createRoot } = await import("react-dom/client")
  const { DataProvider } = await import("../../lib/data/provider.tsx")
  const { CallWrapUp } = await import("./call-wrap-up.tsx")
  const root = createRoot(host)
  t.after(async () => {
    await act(async () => root.unmount())
    clearAccessToken()
    if (previousAPIURL === undefined) delete process.env.NEXT_PUBLIC_PORTAL_API_URL
    else process.env.NEXT_PUBLIC_PORTAL_API_URL = previousAPIURL
    dom.window.close()
  })
  const sent: WrapUpRequest[] = []
  await act(async () =>
    root.render(
      <DataProvider>
        <CallWrapUp
          call={call}
          sawConnected
          saving={false}
          onRecover={() => {}}
          onSave={(request) => sent.push(request)}
        />
      </DataProvider>,
    ),
  )
  const dialog = () => document.querySelector<HTMLElement>('[role="dialog"]')
  const row = () => dialog()?.querySelector('[data-testid="call-wrap-up-task"]')
  for (let attempt = 0; attempt < 50 && !row(); attempt++) {
    await act(async () => new Promise((resolve) => setTimeout(resolve, 0)))
  }
  assert.ok(row(), "the patient's open Task is listed")
  const title = document.getElementById(dialog()!.getAttribute("aria-labelledby") ?? "")
  assert.equal(title?.textContent, "Rosa Synthetic")
  assert.match(dialog()!.textContent ?? "", /Call ended · 3m 42s/)
  assert.equal(dialog()!.querySelector('[data-slot="dialog-close"]'), null)

  await act(async () => {
    dialog()!.dispatchEvent(new window.KeyboardEvent("keydown", { key: "Escape", bubbles: true }))
    document.dispatchEvent(new window.KeyboardEvent("keydown", { key: "Escape", bubbles: true }))
  })
  const backdrop = document.querySelector<HTMLElement>('[data-slot="dialog-overlay"]')!
  await act(async () => {
    backdrop.dispatchEvent(new window.MouseEvent("pointerdown", { bubbles: true }))
    backdrop.dispatchEvent(new window.MouseEvent("mousedown", { bubbles: true }))
    backdrop.click()
    document.body.click()
  })
  await act(async () => new Promise((resolve) => setTimeout(resolve, 0)))
  assert.ok(row(), "the wrap-up is still open")
  assert.deepEqual(sent, [], "a dismissal attempt records nothing")
})
