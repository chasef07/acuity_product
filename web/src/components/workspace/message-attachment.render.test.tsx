import assert from "node:assert/strict"
import test, { type TestContext } from "node:test"
import { act } from "react"
import { createRoot } from "react-dom/client"
import { JSDOM } from "jsdom"

import { EngagementWorkspaceView } from "./engagement-workspace.tsx"
import { clearAccessToken } from "../../lib/auth-client.ts"
import type { Message } from "../../lib/api/generated/types.gen.ts"

test("image download finishing after navigation does not allocate an object URL", async (t) => {
  const view = attachmentHarness(t)
  await view.render()
  assert.equal(view.downloadRequests, 1)
  await act(async () => view.root.unmount())
  await act(async () => view.release())
  assert.equal(view.createURL.mock.callCount(), 0)
})

test("loaded attachment previews revoke their object URL on navigation", async (t) => {
  const view = attachmentHarness(t)
  await view.render()
  await act(async () => view.release())
  assert.equal(view.host.querySelector("img")?.getAttribute("src"), "blob:synthetic-image")
  assert.equal(view.createURL.mock.callCount(), 1)
  assert.equal(view.revokeURL.mock.callCount(), 0)
  await act(async () => view.root.unmount())
  assert.deepEqual(view.revokeURL.mock.calls.map((call) => call.arguments), [["blob:synthetic-image"]])
})

function attachmentHarness(t: TestContext) {
  const dom = installDOM()
  const host = document.createElement("div")
  document.body.append(host)
  const root = createRoot(host)
  const previousAPIURL = process.env.NEXT_PUBLIC_PORTAL_API_URL
  process.env.NEXT_PUBLIC_PORTAL_API_URL = "http://portal.test"
  clearAccessToken()
  let release!: () => void
  const gate = new Promise<void>((resolve) => { release = resolve })
  const view = {
    host, root, release,
    downloadRequests: 0,
    createURL: t.mock.method(URL, "createObjectURL", () => "blob:synthetic-image"),
    revokeURL: t.mock.method(URL, "revokeObjectURL", () => {}),
    render: async () => {
      await act(async () => root.render(
        <EngagementWorkspaceView
          engagement={{ phone: "+15551234567", locations: [], latestActivity: timestamp, openTaskCount: 0, unread: false }}
          practiceID="practice-1" canMutate={false} revision={0}
          onTaskCreated={() => {}} onTaskOpen={() => {}} onCallOpen={() => {}} onAIInteractionOpen={() => {}}
          calling={{ callingOccupied: false, callingEnabled: false, outboundPending: false, ownsSoftphone: false, startOutbound: async () => undefined }}
        />,
      ))
      await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)) })
      await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)) })
    },
  }
  t.mock.method(globalThis, "fetch", async (input: RequestInfo | URL) => {
    const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url
    if (url === "/api/auth/token") return Response.json({ token: "synthetic-token" })
    if (url.includes("/attachments/")) {
      view.downloadRequests++
      await gate
      return new Response(new Blob(["synthetic image"], { type: "image/png" }), { headers: { "Content-Type": "image/png" } })
    }
    assert.match(url, /\/engagements\/.*\/timeline\?/)
    return Response.json({ items: [{ type: "MESSAGE", id: message.id, occurredAt: timestamp, message }], nextCursor: "" })
  })
  t.after(async () => {
    await act(async () => { root.unmount(); release() })
    clearAccessToken()
    if (previousAPIURL === undefined) delete process.env.NEXT_PUBLIC_PORTAL_API_URL
    else process.env.NEXT_PUBLIC_PORTAL_API_URL = previousAPIURL
    dom.window.close()
  })
  return view
}

const timestamp = "2026-09-25T12:00:00Z"
const message: Message = {
  id: "message-1", direction: "INBOUND", body: "Synthetic attachment", sender: "+15551234567", destination: "+15557654321", delivery: "Delivered", version: 1,
  createdAt: timestamp, updatedAt: timestamp,
  thread: { id: "thread-1", practiceId: "practice-1", locationId: "location-1", locationName: "Test location", officePhone: "+15557654321", externalPhone: "+15551234567", outboundBlocked: false, createdAt: timestamp, updatedAt: timestamp },
  attachment: { id: "attachment-1", direction: "INBOUND", state: "Stored", fileName: "synthetic.png", contentType: "image/png", byteSize: 15, createdAt: timestamp, updatedAt: timestamp },
}

function installDOM() {
  const dom = new JSDOM("<!doctype html><html><body></body></html>", {
    url: "http://localhost/workspace",
  })
  Object.defineProperties(globalThis, {
    window: { configurable: true, value: dom.window },
    document: { configurable: true, value: dom.window.document },
    navigator: { configurable: true, value: dom.window.navigator },
    HTMLElement: { configurable: true, value: dom.window.HTMLElement },
    Element: { configurable: true, value: dom.window.Element },
    Node: { configurable: true, value: dom.window.Node },
    MouseEvent: { configurable: true, value: dom.window.MouseEvent },
    getComputedStyle: {
      configurable: true,
      value: dom.window.getComputedStyle.bind(dom.window),
    },
    IS_REACT_ACT_ENVIRONMENT: { configurable: true, value: true },
  })
  Object.defineProperty(dom.window, "matchMedia", {
    configurable: true,
    value: () => ({
      addEventListener() {},
      matches: false,
      removeEventListener() {},
    }),
  })
  Object.defineProperties(dom.window, {
    requestAnimationFrame: {
      configurable: true,
      value: (callback: FrameRequestCallback) =>
        globalThis.setTimeout(() => callback(Date.now()), 0),
    },
    cancelAnimationFrame: {
      configurable: true,
      value: (id: number) => globalThis.clearTimeout(id),
    },
  })
  Object.defineProperty(dom.window.HTMLElement.prototype, "scrollTo", {
    configurable: true,
    value() {},
  })
  return dom
}
