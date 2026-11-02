import assert from "node:assert/strict"
import test from "node:test"
import { act } from "react"
import { createRoot } from "react-dom/client"
import { JSDOM } from "jsdom"

import { PixelWaveField } from "../marketing/pixel-wave-field"

test("the pixel wave stops drawing while off screen and resumes when it scrolls back", async (context) => {
  const dom = new JSDOM("<!doctype html><html><body></body></html>")
  const frames = new Map<number, FrameRequestCallback>()
  let nextFrame = 0
  let intersection: IntersectionObserverCallback | undefined
  Object.defineProperties(globalThis, {
    window: { configurable: true, value: dom.window },
    document: { configurable: true, value: dom.window.document },
    IS_REACT_ACT_ENVIRONMENT: { configurable: true, value: true },
    requestAnimationFrame: {
      configurable: true,
      value: (callback: FrameRequestCallback) => {
        frames.set(++nextFrame, callback)
        return nextFrame
      },
    },
    cancelAnimationFrame: { configurable: true, value: (id: number) => frames.delete(id) },
    ResizeObserver: {
      configurable: true,
      value: class {
        observe() {}
        disconnect() {}
      },
    },
    IntersectionObserver: {
      configurable: true,
      value: class {
        constructor(callback: IntersectionObserverCallback) {
          intersection = callback
        }
        observe() {}
        disconnect() {}
      },
    },
  })
  Object.defineProperty(dom.window, "matchMedia", { configurable: true, value: () => ({ matches: false }) })
  Object.defineProperty(dom.window.HTMLCanvasElement.prototype, "getContext", {
    configurable: true,
    value: () => ({ setTransform() {}, clearRect() {}, fillRect() {} }),
  })
  const container = document.createElement("div")
  const root = createRoot(container)
  context.after(() => dom.window.close())
  const runFrame = () => {
    const [id, draw] = [...frames][0]!
    frames.delete(id)
    draw(performance.now())
  }
  const scroll = (isIntersecting: boolean) =>
    intersection?.([{ isIntersecting } as IntersectionObserverEntry], {} as IntersectionObserver)

  await act(async () => root.render(<PixelWaveField />))
  runFrame()
  assert.equal(frames.size, 1)

  scroll(false)
  assert.equal(frames.size, 0)

  scroll(true)
  assert.equal(frames.size, 1)
  runFrame()
  assert.equal(frames.size, 1)

  await act(async () => root.unmount())
  assert.equal(frames.size, 0)
})
