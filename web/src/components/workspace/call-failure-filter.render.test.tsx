import assert from "node:assert/strict"
import test, { type TestContext } from "node:test"
import { act } from "react"
import { createRoot } from "react-dom/client"
import { JSDOM } from "jsdom"

import { clearAccessToken } from "../../lib/auth-client"
import { DataProvider } from "../../lib/data/provider"
import type { OperatorScorecardQuestion } from "../../lib/api/generated/types.gen"

const question = (key: string, label: string, source: "judge" | "code" = "judge", failure = true): OperatorScorecardQuestion => ({
  key, label, source, failure, code: key, prompt: label, question: label, yes: "Yes", no: "No",
})
const catalogs: Record<string, OperatorScorecardQuestion[]> = {
  "typesafe-scorecard-v11": [question("office_question_answered", "Current office answer"), question("call_ended_correctly", "Call ended correctly")],
  "typesafe-scorecard-v10": [question("office_question_answered", "Previous office answer"), question("call_ended_correctly", "Previous call ending")],
  "typesafe-scorecard-v9": [question("office_question_answered", "Previous office answer"), question("call_ended_correctly", "Previous call ending")],
  "typesafe-scorecard-v8": [question("office_question_answered", "Previous office answer"), question("call_ended_correctly", "Previous call ending")],
  "typesafe-scorecard-v7": [question("office_question_answered", "Historical office answer"), question("office_rules_grounded", "Office facts backed"), question("new_booking_requested", "Booking requested", "judge", false), question("knowledge_answered", "Code knowledge", "code")],
  "typesafe-scorecard-v6": [question("office_rules_grounded", "Older office facts"), question("need_understood", "Understood every request")],
}

test("judge filter offers unique current and historical failures using current labels", async (t) => {
  const view = await harness(t)
  await view.render()
  await view.open()
  assert.deepEqual(view.options(), ["Any judge failure", "All calls", "Current office answer", "Call ended correctly", "Office facts backed", "Understood every request"])
  assert.deepEqual(view.requests.sort(), ["typesafe-scorecard-v6", "typesafe-scorecard-v7", "typesafe-scorecard-v8", "typesafe-scorecard-v9", "typesafe-scorecard-v10", "typesafe-scorecard-v11"].sort())
  const legacy = Array.from(document.querySelectorAll<HTMLElement>("[role='option']")).find((item) => item.textContent === "Office facts backed")
  assert.ok(legacy)
  await act(async () => legacy.click())
  assert.equal(view.selected(), "office_rules_grounded")
})

test("a failed historical catalog is visible and retry recovers its missing judges", async (t) => {
  const view = await harness(t, "typesafe-scorecard-v7")
  await view.render()
  await waitFor(() => /Judge list unavailable/.test(view.host.textContent ?? ""))
  const retry = Array.from(view.host.querySelectorAll<HTMLButtonElement>("button")).find((item) => item.textContent === "Retry judge list")
  assert.ok(retry, "A failed historical catalog must expose retry")
  assert.match(view.host.textContent ?? "", /Judge list unavailable/)
  view.recover()
  await act(async () => retry.click())
  await waitFor(() => !/Judge list unavailable/.test(view.host.textContent ?? ""))
  assert.equal(view.requests.filter((version) => version === "typesafe-scorecard-v7").length, 2)
  assert.equal(view.requests.filter((version) => version === "typesafe-scorecard-v8").length, 1)
  assert.equal(view.requests.filter((version) => version === "typesafe-scorecard-v9").length, 1)
  assert.equal(view.requests.filter((version) => version === "typesafe-scorecard-v10").length, 1)
  assert.equal(view.requests.filter((version) => version === "typesafe-scorecard-v11").length, 1)
  await view.open()
  assert.ok(view.options().includes("Office facts backed"))
})

async function harness(t: TestContext, failedVersion = "") {
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
  process.env.NEXT_PUBLIC_PORTAL_API_URL = "http://portal.test"
  clearAccessToken()
  const requests: string[] = []
  t.mock.method(globalThis, "fetch", async (input: RequestInfo | URL) => {
    if (input === "/api/auth/token") return Response.json({ token: "synthetic-token" })
    const url = new URL((input as Request).url)
    assert.equal(url.pathname, "/v1/operator/scorecard/questions")
    const version = url.searchParams.get("version") ?? "current"
    requests.push(version)
    if (version === failedVersion) return Response.json({ error: { code: "UNAVAILABLE", retryable: true, message: "Synthetic failure", correlationId: "synthetic" } }, { status: 503 })
    return Response.json({ questions: catalogs[version] })
  })
  const { CallFailureFilter } = await import("./call-failure-filter")
  const host = document.createElement("div")
  document.body.append(host)
  const root = createRoot(host)
  let selected = "any"
  t.after(async () => {
    await act(async () => root.unmount())
    dom.window.close()
  })
  return {
    host, requests,
    render: () => act(async () => root.render(<DataProvider><CallFailureFilter value="any" onChange={(value) => { selected = value }} /></DataProvider>)),
    open: () => act(async () => {
      const trigger = host.querySelector<HTMLElement>("[role='combobox']")!
      trigger.dispatchEvent(new window.MouseEvent("mousedown", { button: 0, bubbles: true }))
      trigger.click()
    }),
    options: () => Array.from(document.querySelectorAll("[role='option']"), (option) => option.textContent),
    recover: () => { failedVersion = "" },
    selected: () => selected,
  }
}

async function waitFor(ready: () => boolean) {
  for (let attempt = 0; attempt < 50 && !ready(); attempt++) {
    await act(async () => new Promise((resolve) => setTimeout(resolve, 0)))
  }
  assert.ok(ready(), "condition was not reached")
}
