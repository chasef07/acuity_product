import assert from "node:assert/strict"
import test, { type TestContext } from "node:test"
import { act, type ReactNode } from "react"
import { createRoot } from "react-dom/client"
import { JSDOM } from "jsdom"

import { SidebarProvider } from "@/components/ui/sidebar"
import { InsuranceList } from "./insurance-list.tsx"
import { ManageAgent } from "./manage-agent.tsx"
import { clearAccessToken } from "../../lib/auth-client.ts"
import type {
  InsuranceCoverage,
  InsurancePlanRule,
  InsurancePlansQuery,
  Location,
  LocationKnowledge,
  LocationKnowledgeQuery,
} from "../../lib/api/generated/types.gen.ts"

const locations: Location[] = [
  { id: "00000000-0000-4000-8000-000000000001", name: "Hollywood" },
  { id: "00000000-0000-4000-8000-000000000002", name: "Sweetwater" },
]

const plans: InsurancePlanRule[] = [
  plan({ planId: "staff", label: "Demo Advantage HMO", outcome: "needs_staff_task", requirements: ["prior_authorization", "pcp_referral", "staff_verify"], carrierCode: "DEM03" }),
  plan({ planId: "pending", label: "Pending Plan HMO", outcome: "needs_staff_task", carrierCode: "PEN01" }),
  plan({ planId: "refused-elsewhere", label: "Sample Medicaid MMA", outcome: "not_accepted", requirements: ["pcp_referral"], acceptedAt: ["Sweetwater"], carrierCode: "SMP02" }),
  plan({ planId: "accepted-b", label: "example Select", outcome: "accepted", allowedProviders: ["Dr. Rivera"], carrierCode: "EXS01" }),
  plan({
    planId: "accepted-a",
    label: "Example Choice PPO",
    outcome: "accepted",
    allowedProviders: ["Dr. Rivera", "Dr. Chen"],
    names: ["Example Choice", "Choice PPO"],
    carrierCode: "EXC01",
    carrierId: "car10001",
    carrierName: "EXAMPLE CHOICE HEALTH",
    note: "Synthetic sheet note",
    callerNotice: "Synthetic caller notice",
  }),
  plan({ planId: "self-pay", label: "Zeta Discount Card", outcome: "not_accepted", callerNotice: "Self-pay rates apply" }),
]

test("Manage agent shows the selected page at full width without its own navigation", async (t) => {
  const page = harness(t)
  for (const [selected, heading] of [["transcripts", "Transcripts"], ["knowledge", "Knowledge base"], ["insurance", "Insurance list"]] as const) {
    await page.render(
      <ManageAgent practiceID="practice-1" locationScopeID="" locations={locations} page={selected} />,
    )
    assert.equal(page.host.querySelector("h1")?.textContent, heading)
    assert.equal(page.host.querySelector("nav"), null)
  }
  await waitFor(() => page.plansRequests.length === 1 && page.knowledgeRequests.length === 1)
  await page.unmount()
})

test("insurance plans are grouped by result, sorted by name, and summarized in one row", async (t) => {
  const page = harness(t)
  await page.render(insuranceList(locations[1].id))
  await waitFor(() => page.groups().length === 3)
  assert.deepEqual(page.plansRequests, [
    { practiceId: "practice-1", locationId: locations[1].id, coverage: "medical" },
  ])
  assert.deepEqual(page.groups(), [
    { title: "Accepted2", expanded: "true", rows: ["Example Choice PPODr. Rivera, Dr. ChenEXC01", "example SelectDr. RiveraEXS01"] },
    { title: "Staff confirms2", expanded: "true", rows: ["Demo Advantage HMOPrior auth, PCP referral, Staff verifiesDEM03", "Pending Plan HMOPending on the sheetPEN01"] },
    { title: "Not accepted2", expanded: "true", rows: ["Sample Medicaid MMASweetwater takes itSMP02", "Zeta Discount CardSelf-pay rates apply"] },
  ])
  assert.match(page.host.querySelector("[role='combobox'][aria-label='Location']")?.textContent ?? "", /^Sweetwater/)
  assert.match(page.host.querySelector("[role='combobox'][aria-label='Visit coverage']")?.textContent ?? "", /^Medical/)
  assert.equal(Array.from(page.host.querySelectorAll("input")).filter((input) => input.getAttribute("aria-hidden") !== "true").length, 0)
  await page.unmount()
})

test("a plan row expands in place and a group collapses", async (t) => {
  const page = harness(t)
  await page.render(insuranceList())
  await waitFor(() => page.groups().length === 3)
  const row = page.button(/^Example Choice PPO/)
  assert.equal(row.getAttribute("aria-expanded"), "false")
  assert.equal(page.panel(row).open, false)
  assert.match(page.panel(row).text, /Synthetic sheet note/)
  await act(async () => row.click())
  assert.equal(row.getAttribute("aria-expanded"), "true")
  assert.equal(page.panel(row).open, true)
  const details = Array.from(row.closest("li")!.querySelectorAll("dl div"), (item) => item.textContent)
  assert.deepEqual(details, [
    "Callers also sayExample Choice, Choice PPO",
    "AMD carriercar10001 · EXAMPLE CHOICE HEALTH",
    "Sheet noteSynthetic sheet note",
    "Callers hearSynthetic caller notice",
  ])
  const accepted = page.button(/^Accepted/)
  await act(async () => accepted.click())
  assert.equal(accepted.getAttribute("aria-expanded"), "false")
  assert.deepEqual(page.groups()[0].rows, [])
  await page.unmount()
})

test("changing Location or coverage sends exactly one new request with the new values", async (t) => {
  const page = harness(t)
  const render = (locationID: string, coverage: InsuranceCoverage) =>
    page.render(
      <InsuranceList
        practiceID="practice-1"
        locations={locations}
        locationID={locationID}
        onLocation={() => {}}
        coverage={coverage}
        onCoverage={() => {}}
      />,
    )
  await render(locations[0].id, "medical")
  await waitFor(() => page.groups().length === 3)
  await render(locations[1].id, "medical")
  await waitFor(() => page.plansRequests.length === 2 && page.groups().length === 3)
  await render(locations[1].id, "routine_vision")
  await waitFor(() => page.plansRequests.length === 3 && page.groups().length === 3)
  await act(async () => new Promise((resolve) => setTimeout(resolve, 30)))
  assert.deepEqual(page.plansRequests, [
    { practiceId: "practice-1", locationId: locations[0].id, coverage: "medical" },
    { practiceId: "practice-1", locationId: locations[1].id, coverage: "medical" },
    { practiceId: "practice-1", locationId: locations[1].id, coverage: "routine_vision" },
  ])
  await page.unmount()
})

test("a Location without the coverage, a 503 and no access each read once", async (t) => {
  const page = harness(t)
  page.plansFor = () => []
  await page.render(insuranceList())
  await waitFor(() => /No medical plans are on this Location's insurance list\./.test(page.text()))

  page.plansStatus = 503
  page.plansError = { code: "UNAVAILABLE", message: "Insurance rules aren't available for this Location.", correlationId: "synthetic", retryable: false }
  await page.render(insuranceList(locations[1].id), "second")
  await waitFor(() => page.alert() !== "")
  assert.equal(page.alert(), "Insurance rules aren't available for this Location.")

  page.plansError = undefined
  await page.render(insuranceList(), "third")
  await waitFor(() => page.alert() !== "")
  assert.equal(page.alert(), "Insurance rules aren't available right now.Try again")
  page.plansStatus = 200
  page.plansFor = () => plans
  await page.click("Try again")
  await waitFor(() => page.groups().length === 3)

  page.plansStatus = 403
  await page.render(insuranceList(locations[1].id), "fourth")
  await waitFor(() => page.alert() !== "")
  assert.equal(page.alert(), "You don't have access to this Location's insurance rules.")
  await page.unmount()
})

test("knowledge shows its import date and collapsed sections that keep line breaks", async (t) => {
  const page = harness(t)
  page.knowledge = {
    locationId: locations[0].id,
    revision: { id: "revision-1", createdAt: "2026-09-30T15:00:00Z" },
    sections: [
      { id: "hours", title: "Office hours", text: "Monday to Friday\n8am to 5pm" },
      { id: "parking", title: "Parking", text: "Synthetic garage on site." },
    ],
  }
  await page.render(knowledgeBase())
  await waitFor(() => /Office hours/.test(page.text()))
  assert.deepEqual(page.knowledgeRequests, [{ practiceId: "practice-1", locationId: locations[0].id }])
  assert.equal(page.host.querySelector("time")?.getAttribute("dateTime"), "2026-09-30T15:00:00Z")
  assert.match(page.text(), /Updated /)
  const hours = page.button(/^Office hours$/)
  assert.equal(hours.getAttribute("aria-expanded"), "false")
  assert.equal(page.panel(hours).open, false)
  await act(async () => hours.click())
  assert.equal(hours.getAttribute("aria-expanded"), "true")
  assert.equal(page.panel(hours).open, true)
  const text = hours.nextElementSibling?.querySelector("p")
  assert.equal(text?.textContent, "Monday to Friday\n8am to 5pm")
  assert.match(text?.className ?? "", /whitespace-pre-wrap/)
  assert.equal(page.panel(page.button(/^Parking$/)).open, false)
  await page.unmount()
})

test("knowledge search filters sections by title and text, opens matches, and says when nothing matches", async (t) => {
  const page = harness(t)
  page.knowledge = {
    locationId: locations[0].id,
    sections: [
      { id: "hours", title: "Office hours", text: "Monday to Friday\n8am to 5pm" },
      { id: "fax", title: "Office fax number", text: "Synthetic fax: (000) 555-0100." },
      { id: "languages", title: "Languages spoken", text: "Doctors speak Español and English." },
    ],
  }
  await page.render(knowledgeBase())
  await waitFor(() => /Office hours/.test(page.text()))
  const search = page.host.querySelector<HTMLInputElement>("input[type='search']")
  assert.ok(search)
  assert.equal(search.getAttribute("aria-label"), "Search knowledge")
  const count = page.host.querySelector("[aria-live='polite']")
  assert.equal(count?.textContent, "")

  await page.type(search, "FAX")
  assert.equal(page.host.querySelector("[aria-live='polite']"), count)
  assert.equal(count?.textContent, "1 of 3 entries")
  assert.deepEqual(page.rowTitles(), ["Office fax number"])
  assert.equal(page.panel(page.button(/^Office fax number$/)).open, true)
  assert.match(page.text(), /1 of 3 entries/)

  await page.type(search, "espanol monday")
  assert.deepEqual(page.rowTitles(), [])
  assert.match(page.text(), /No entries match “espanol monday”\./)

  await page.type(search, "espanol")
  assert.deepEqual(page.rowTitles(), ["Languages spoken"])

  await page.type(search, "")
  assert.deepEqual(page.rowTitles(), ["Office hours", "Office fax number", "Languages spoken"])
  assert.equal(page.panel(page.button(/^Office hours$/)).open, false)
  assert.doesNotMatch(page.text(), /of 3 entries/)
  await page.unmount()
})

test("knowledge without an import and unavailable knowledge say so", async (t) => {
  const page = harness(t)
  page.knowledge = { locationId: locations[0].id, sections: [] }
  await page.render(knowledgeBase())
  await waitFor(() => /No knowledge has been imported for this Location yet\./.test(page.text()))
  assert.doesNotMatch(page.text(), /Updated/)

  page.knowledgeStatus = 503
  page.knowledgeError = { code: "UNAVAILABLE", message: "Knowledge isn't available for this Location.", correlationId: "synthetic", retryable: false }
  await page.render(knowledgeBase(locations[1].id), "second")
  await waitFor(() => page.alert() !== "")
  assert.equal(page.alert(), "Knowledge isn't available for this Location.")
  assert.equal(page.knowledgeRequests.at(-1)?.locationId, locations[1].id)
  await page.unmount()
})

function insuranceList(locationScopeID = "") {
  return <ManageAgent practiceID="practice-1" locationScopeID={locationScopeID} locations={locations} page="insurance" />
}

function knowledgeBase(locationScopeID = "") {
  return <ManageAgent practiceID="practice-1" locationScopeID={locationScopeID} locations={locations} page="knowledge" />
}

function plan(change: Partial<InsurancePlanRule> & Pick<InsurancePlanRule, "planId" | "label" | "outcome">): InsurancePlanRule {
  return { names: [], allowedProviders: [], requirements: [], acceptedAt: [], ...change }
}

type Envelope = { code: string; message: string; correlationId: string; retryable: boolean }

function harness(t: TestContext) {
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
    value: () => ({ addEventListener() {}, matches: false, removeEventListener() {} }),
  })
  t.after(() => dom.window.close())
  process.env.NEXT_PUBLIC_PORTAL_API_URL = "http://portal.test"
  clearAccessToken()
  const host = document.createElement("div")
  document.body.append(host)
  const root = createRoot(host)
  const page = {
    host,
    plansRequests: [] as InsurancePlansQuery[],
    knowledgeRequests: [] as LocationKnowledgeQuery[],
    plansStatus: 200,
    plansError: undefined as Envelope | undefined,
    plansFor: (() => plans) as (body: InsurancePlansQuery) => InsurancePlanRule[],
    knowledgeStatus: 200,
    knowledgeError: undefined as Envelope | undefined,
    knowledge: { locationId: "", sections: [] } as LocationKnowledge,
    render: (element: ReactNode, key = "first") =>
      act(async () => root.render(<SidebarProvider key={key}>{element}</SidebarProvider>)),
    unmount: () => act(async () => root.unmount()),
    text: () => host.textContent ?? "",
    panel: (trigger: Element) => {
      const panel = trigger.nextElementSibling
      assert.equal(panel?.getAttribute("data-slot"), "collapsible-content")
      return { open: !panel.hasAttribute("hidden"), text: panel.textContent ?? "" }
    },
    alert: () => host.querySelector("[role='alert']")?.textContent ?? "",
    type: (input: HTMLInputElement, value: string) =>
      act(async () => {
        Object.assign(input, { attachEvent() {}, detachEvent() {} })
        input.focus()
        Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, "value")!.set!.call(input, value)
        input.dispatchEvent(new window.KeyboardEvent("keyup", { bubbles: true }))
      }),
    rowTitles: () => Array.from(host.querySelectorAll("li > button"), (row) => row.textContent ?? ""),
    button: (name: RegExp) => {
      const button = Array.from(host.querySelectorAll("button")).find((item) => name.test(item.textContent ?? ""))
      assert.ok(button, String(name))
      return button
    },
    click: async (name: string) => {
      const button = Array.from(host.querySelectorAll("button")).find((item) => item.textContent === name)
      assert.ok(button, name)
      await act(async () => button.click())
    },
    groups: () =>
      Array.from(host.querySelectorAll("[role='group'][aria-labelledby]"), (group) => {
        const trigger = group.querySelector("button")!
        return {
          title: trigger.textContent ?? "",
          expanded: trigger.getAttribute("aria-expanded") ?? "",
          rows: Array.from(group.querySelectorAll("li > button"))
            .filter((row) => !row.closest("[data-slot='collapsible-content'][data-closed]"))
            .map((row) => row.textContent ?? ""),
        }
      }),
  }
  t.mock.method(globalThis, "fetch", async (input: RequestInfo | URL) => {
    if (input === "/api/auth/token") return Response.json({ token: "synthetic-token" })
    const request = input as Request
    const url = new URL(request.url)
    assert.equal(request.headers.get("Authorization"), "Bearer synthetic-token")
    if (url.pathname === "/v1/insurance/plans/query") {
      const body = (await request.json()) as InsurancePlansQuery
      page.plansRequests.push(body)
      if (page.plansStatus !== 200) return failure(page.plansStatus, page.plansError)
      return Response.json({ locationId: body.locationId, coverage: body.coverage, plans: page.plansFor(body) })
    }
    if (url.pathname === "/v1/knowledge/query") {
      const body = (await request.json()) as LocationKnowledgeQuery
      page.knowledgeRequests.push(body)
      if (page.knowledgeStatus !== 200) return failure(page.knowledgeStatus, page.knowledgeError)
      return Response.json(page.knowledge)
    }
    assert.equal(url.pathname, "/v1/agent-calls/query")
    return Response.json({ calls: [], nextCursor: "" })
  })
  return page
}

function failure(status: number, error: Envelope | undefined) {
  return error ? Response.json({ error }, { status }) : new Response("", { status })
}

async function waitFor(ready: () => boolean) {
  for (let attempt = 0; attempt < 100 && !ready(); attempt++) {
    await act(async () => new Promise((resolve) => setTimeout(resolve, 10)))
  }
  assert.ok(ready(), "condition was not reached")
}
