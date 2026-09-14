import assert from "node:assert/strict"
import test from "node:test"
import { act } from "react"
import { createRoot } from "react-dom/client"
import { JSDOM } from "jsdom"
import { TaskCallContext } from "./task-call-context.tsx"
import { clearAccessToken } from "../../lib/auth-client.ts"
import type { Task } from "../../lib/api/generated/types.gen.ts"

for (const origin of ["ABITA_AI", "VOICEMAIL_RECOVERY"] as const) {
  test(`${origin} shows linked voicemail and preserves its completion policy`, async (t) => {
    const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost" })
    for (const name of ["window", "document", "navigator", "HTMLElement", "Element", "Node"] as const) {
      Object.defineProperty(globalThis, name, { configurable: true, value: name === "window" ? dom.window : dom.window[name] })
    }
    Object.defineProperty(globalThis, "IS_REACT_ACT_ENVIRONMENT", { configurable: true, value: true })
    t.mock.method(dom.window.HTMLMediaElement.prototype, "play", async () => {})
    const task = {
      id: "task-1", practiceId: "practice-1", locationId: "location-1", locationName: "Synthetic office",
      title: "Review refill request", state: "OPEN", origin, urgency: "normal", category: "medication",
      phone: "+12025550123", createdAt: "2026-09-13T12:00:00Z", updatedAt: "2026-09-13T12:00:00Z",
      createdBy: { kind: "SERVICE", subject: "synthetic-agent" }, version: 2,
      interactions: [{ callId: "call-1", type: "VOICEMAIL", occurredAt: "2026-09-13T12:00:00Z" }],
    } as Task
    const completions: unknown[] = []
    const previousURL = process.env.NEXT_PUBLIC_PORTAL_API_URL
    process.env.NEXT_PUBLIC_PORTAL_API_URL = "http://localhost"
    clearAccessToken()
    t.mock.method(globalThis, "fetch", async (input: RequestInfo | URL, init?: RequestInit) => {
      const request = input instanceof Request ? input : new Request(new URL(String(input), "http://localhost"), init)
      const path = new URL(request.url).pathname
      if (path === "/api/auth/token") return Response.json({ token: "synthetic-token" })
      if (path === "/v1/tasks/task-1") return Response.json(task)
      if (path.endsWith("/complete")) {
        completions.push(await request.json())
        return Response.json({ ...task, state: "COMPLETED" })
      }
      if (path.endsWith("/voicemail-playback")) return Response.json({ token: "synthetic-playback" })
      if (path === "/v1/calling/calls/call-1") return Response.json({ id: "call-1", voicemail: { audioState: "READY", outcome: "VOICEMAIL", durationSeconds: 5 } })
      throw new Error(`Unexpected request: ${path}`)
    })
    const host = dom.window.document.createElement("div")
    dom.window.document.body.append(host)
    const root = createRoot(host)
    t.after(async () => {
      await act(async () => root.unmount())
      clearAccessToken()
      if (previousURL === undefined) delete process.env.NEXT_PUBLIC_PORTAL_API_URL
      else process.env.NEXT_PUBLIC_PORTAL_API_URL = previousURL
      dom.window.close()
    })
    await act(async () => {
      root.render(<TaskCallContext task={task} activeCall={undefined} view="task" canMutate={false} canCall={false} historyHint={0} taskCallPending={false} taskCallError="" onTaskUpdated={() => {}} onStartTaskCall={() => {}} onReturnToCall={() => {}} />)
    })
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 20)) })
    const play = Array.from(host.querySelectorAll("button")).find((button) => button.textContent?.includes("Play"))
    assert.ok(play, "linked voicemail must be playable from the original Task")
    await act(async () => play.click())
    const audio = host.querySelector("audio")
    assert.ok(audio, "playback must load")
    await act(async () => audio.dispatchEvent(new dom.window.Event("ended")))
    assert.deepEqual(completions, origin === "ABITA_AI" ? [] : [{ expectedVersion: 2 }])
  })
}
