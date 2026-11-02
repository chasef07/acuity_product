import assert from "node:assert/strict"
import test from "node:test"
import { act } from "react"
import { createRoot } from "react-dom/client"
import { JSDOM } from "jsdom"

import { clearAccessToken } from "../../lib/auth-client.ts"

const dom = new JSDOM("<!doctype html><html><body></body></html>")
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
const { MessageAttachmentView } = await import("./message-attachment.tsx")

test("downloading a previewed photo saves the loaded image without reading it again", async (t) => {
  process.env.NEXT_PUBLIC_PORTAL_API_URL = "http://portal.test"
  clearAccessToken()
  const reads: string[] = []
  t.mock.method(globalThis, "fetch", async (input: RequestInfo | URL) => {
    if (input === "/api/auth/token") return Response.json({ token: "synthetic-token" })
    reads.push(new URL((input as Request).url).pathname)
    return new Response(new Blob(["photo"], { type: "image/jpeg" }), { headers: { "Content-Type": "image/jpeg" } })
  })
  t.mock.method(URL, "createObjectURL", () => "blob:photo")
  t.mock.method(URL, "revokeObjectURL", () => undefined)
  const saved: string[] = []
  Object.defineProperty(dom.window.HTMLAnchorElement.prototype, "click", {
    configurable: true,
    value(this: HTMLAnchorElement) {
      saved.push(`${this.href} ${this.download}`)
    },
  })
  const container = document.createElement("div")
  document.body.append(container)
  const root = createRoot(container)
  t.after(async () => {
    await act(async () => root.unmount())
    dom.window.close()
  })

  await act(async () =>
    root.render(
      <MessageAttachmentView
        attachment={{
          id: "attachment-1",
          direction: "INBOUND",
          state: "Stored",
          fileName: "photo.jpg",
          contentType: "image/jpeg",
          byteSize: 5,
          createdAt: "2026-11-01T12:00:00Z",
          updatedAt: "2026-11-01T12:00:00Z",
        }}
        onChanged={() => undefined}
      />,
    ),
  )
  await waitFor(() => container.querySelector("img") !== null)
  await act(async () => container.querySelector<HTMLButtonElement>('[aria-label="Preview photo.jpg"]')!.click())
  const downloadButton = () =>
    [...document.querySelectorAll("button")].find((button) => button.textContent?.includes("Download"))
  await waitFor(() => downloadButton() !== undefined)
  await act(async () => downloadButton()!.click())
  await waitFor(() => saved.length === 1)

  assert.deepEqual(saved, ["blob:photo photo.jpg"])
  assert.deepEqual(reads, ["/v1/attachments/attachment-1"])
})

async function waitFor(ready: () => boolean) {
  for (let attempt = 0; attempt < 50 && !ready(); attempt++) {
    await act(async () => new Promise((resolve) => setTimeout(resolve, 0)))
  }
  assert.ok(ready(), "condition was not reached")
}
