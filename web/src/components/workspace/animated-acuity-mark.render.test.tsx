import assert from "node:assert/strict"
import test from "node:test"
import { act } from "react"
import { createRoot } from "react-dom/client"
import { JSDOM } from "jsdom"

import { AnimatedAcuityMark } from "../animated-acuity-mark"

test("every daily mark motion keeps animating when the first frame is stamped before the effect started", async (context) => {
  for (let day = 1; day <= 10; day += 1) {
    const dom = new JSDOM("<!doctype html><html><body></body></html>")
    const frames: FrameRequestCallback[] = []
    Object.defineProperties(globalThis, {
      window: { configurable: true, value: dom.window },
      document: { configurable: true, value: dom.window.document },
      IS_REACT_ACT_ENVIRONMENT: { configurable: true, value: true },
      requestAnimationFrame: { configurable: true, value: (callback: FrameRequestCallback) => frames.push(callback) },
      cancelAnimationFrame: { configurable: true, value: () => {} },
    })
    Object.defineProperty(dom.window, "matchMedia", { configurable: true, value: () => ({ matches: false }) })
    context.mock.timers.enable({ apis: ["Date"], now: new Date(2026, 9, day, 12) })
    const startedAt = performance.now()

    const container = document.createElement("div")
    document.body.append(container)
    const root = createRoot(container)
    await act(async () => root.render(<AnimatedAcuityMark />))
    const motion = container.querySelector("svg")?.getAttribute("data-motion")

    const runFrame = (timestamp: number) => {
      const next = frames.shift()
      assert.ok(next, `${motion} scheduled a frame`)
      next(timestamp)
    }
    runFrame(startedAt - 5)
    const first = container.querySelector("svg")!.innerHTML
    const later = new Set<string>()
    for (let elapsed = 100; elapsed <= 5000; elapsed += 100) {
      runFrame(startedAt + elapsed)
      later.add(container.querySelector("svg")!.innerHTML)
    }
    assert.equal(frames.length, 1, `${motion} keeps its frame loop running`)
    assert.ok([...later].some((frame) => frame !== first), `${motion} moves its dots`)

    await act(async () => root.unmount())
    context.mock.timers.reset()
  }
})
