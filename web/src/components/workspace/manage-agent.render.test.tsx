import assert from "node:assert/strict"
import test, { type TestContext } from "node:test"
import { act, type ReactNode } from "react"
import { createRoot } from "react-dom/client"
import { JSDOM } from "jsdom"

import { authClient, clearAccessToken } from "../../lib/auth-client.ts"
import { DataProvider } from "../../lib/data/provider.tsx"
import { useLiveRefresh } from "../../lib/data/queries.ts"
import { homeDay, homeGreeting } from "../../lib/home-greeting.ts"
import type {
  AgentCallDetail,
  AgentCallsPage,
  AgentCallsQuery,
  InsuranceCoverage,
  InsurancePlanRule,
  InsurancePlansQuery,
  KnowledgePublication,
  KnowledgeRevisionHistoryEntry,
  KnowledgeRevisionSummary,
  Location,
  LocationKnowledge,
  LocationKnowledgeHistory,
  LocationKnowledgeQuery,
  LocationKnowledgeRevisionQuery,
} from "../../lib/api/generated/types.gen.ts"

Object.defineProperty(globalThis, "document", { configurable: true, value: new JSDOM("").window.document })
const { SidebarProvider } = await import("@/components/ui/sidebar")
const { InsuranceList } = await import("./insurance-list.tsx")
const { KnowledgeHistory } = await import("./knowledge-base.tsx")
const { ManageAgent } = await import("./manage-agent.tsx")

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
  for (const [selected, heading] of [["transcripts", "Calls"], ["knowledge", "Knowledge base"], ["insurance", "Insurance list"]] as const) {
    await page.render(
      <ManageAgent actorSubject="staff-a" practiceID="practice-1" locationScopeID={locations[0].id} locations={locations} page={selected} onPage={() => {}} />,
    )
    assert.equal(page.host.querySelector("h1")?.textContent, heading)
    assert.equal(page.host.querySelector("nav"), null)
  }
  await waitFor(() => page.plansRequests.length === 1 && page.knowledgeRequests.length === 1)
  await page.unmount()
})

test("Home connects Acuity to knowledge, insurance, and tools", async (t) => {
  const page = harness(t)
  page.knowledge = { locationId: locations[0].id, sections: [], revision: { id: "revision-14", version: 14, createdAt: "2026-10-12T14:00:00Z" } }
  const pages: string[] = []
  await page.render(
    <ManageAgent actorSubject="staff-a" practiceID="practice-1" locationScopeID={locations[0].id} locations={locations} page="home" onPage={(next) => pages.push(next)} />,
  )
  const greeting = homeGreeting(homeDay(new Date()))
  assert.equal(page.host.querySelector("h1 span")?.textContent, `${greeting.greeting},`)
  assert.equal(page.host.querySelector("h1 time")?.textContent, greeting.dateLabel)
  const connections = page.host.querySelector('[aria-label="Agent connections"]')
  assert.ok(connections)
  assert.deepEqual(Array.from(connections.querySelectorAll("button"), (button) => button.getAttribute("aria-label")), ["Knowledge base", "Insurance", "Tools"])
  assert.equal(connections.querySelectorAll('svg[aria-label="Acuity"] circle').length, 7)
  assert.doesNotMatch(page.text(), /What Acuity can do|Transfer policy|Emergency policy/)
  assert.deepEqual(page.agentCallsRequests, [])
  await act(async () => page.button(/^Knowledge base/).click())
  await waitFor(() => /Version 14 · published/.test(page.text()))
  assert.doesNotMatch(page.text(), /v14/)
  assert.match(page.text(), /Open knowledge base/)
  assert.doesNotMatch(page.text(), /Needs knowledge|An office question went unanswered/)
  assert.deepEqual(page.agentCallsRequests, [])
  await page.click("Open knowledge base")
  await act(async () => page.button(/^Insurance/).click())
  await page.click("Open insurance list")
  assert.deepEqual(pages, ["knowledge", "insurance"])
  await act(async () => page.button(/^Tools/).click())
  assert.deepEqual(
    Array.from(page.host.querySelectorAll('[aria-label="Agent tools"] li'), (item) => item.querySelector("span")?.textContent),
    [
      "Search office knowledge",
      "Find an existing patient",
      "Register a new patient",
      "Check insurance acceptance",
      "Update insurance coverage",
      "Find available appointments",
      "Book an appointment",
      "Reschedule an appointment",
      "Cancel an appointment",
      "Save a staff follow-up task",
      "Transfer a call",
      "End a call",
    ],
  )
  await page.click("Close details")
  assert.equal(page.host.querySelector('[aria-label="Agent details"]'), null)
  await page.unmount()
})

test("pages that show one Location use the workspace's office, or the only office, and never offer their own Location picker", async (t) => {
  const page = harness(t)
  page.knowledge = { locationId: locations[1].id, sections: [], revision: { id: "revision-2", version: 2, createdAt: "2026-10-12T14:00:00Z" } }
  const scopes = [
    { locationScopeID: locations[1].id, offices: locations, expected: locations[1].id },
    { locationScopeID: "", offices: [locations[0]], expected: locations[0].id },
  ]
  for (const [index, { locationScopeID, offices, expected }] of scopes.entries()) {
    for (const selected of ["home", "knowledge", "insurance"] as const) {
      await page.render(
        <ManageAgent actorSubject="staff-a" practiceID="practice-1" locationScopeID={locationScopeID} locations={offices} page={selected} onPage={() => {}} />,
        `${index}:${selected}`,
      )
      if (selected === "home") await act(async () => page.button(/^Knowledge base/).click())
      await waitFor(() =>
        selected === "insurance"
          ? page.plansRequests.at(-1)?.locationId === expected && page.groups().length === 3
          : page.knowledgeRequests.at(-1)?.locationId === expected && /Version 2/.test(page.text()),
      )
      assert.equal(page.host.querySelector("[role='combobox'][aria-label='Location']"), null)
      assert.equal(page.host.querySelector("[role='combobox'][aria-label='Office']"), null)
      assert.doesNotMatch(page.text(), /Choose an office/)
    }
    assert.deepEqual(page.agentCallsRequests, [])
  }
  await page.unmount()
})

test("with all offices in scope and several offices, one-Location pages ask for an office and load nothing", async (t) => {
  const page = harness(t)
  for (const selected of ["home", "knowledge", "insurance"] as const) {
    await page.render(
      <ManageAgent actorSubject="staff-a" practiceID="practice-1" locationScopeID="" locations={locations} page={selected} onPage={() => {}} />,
      selected,
    )
    await act(async () => new Promise((resolve) => setTimeout(resolve, 30)))
    assert.equal(page.host.querySelector("[role='combobox']"), null)
    assert.equal(page.host.querySelector("input[type='search']"), null)
    if (selected === "home") {
      const connections = page.host.querySelector('[role="group"][aria-label="Agent connections"]')!
      assert.equal(connections.querySelectorAll("button:disabled").length, 0)
      assert.doesNotMatch(page.text(), /Choose an office/)
      await act(async () => page.button(/^Knowledge base/).click())
      assert.match(page.text(), /Open knowledge base/)
    } else {
      assert.match(page.text(), /Choose an office.*Choose an office using the office picker above\./)
    }
  }
  assert.equal(page.host.querySelector("h1")?.textContent, "Insurance list")
  assert.deepEqual([page.knowledgeRequests, page.plansRequests, page.agentCallsRequests], [[], [], []])
  await page.unmount()
})

test("Calls follow the workspace's office, including all offices, without a page-level Office picker", async (t) => {
  const page = harness(t)
  for (const locationScopeID of ["", locations[1].id]) {
    await page.render(
      <ManageAgent actorSubject="staff-a" practiceID="practice-1" locationScopeID={locationScopeID} locations={locations} page="transcripts" onPage={() => {}} />,
      locationScopeID,
    )
    await waitFor(() => page.agentCallsRequests.length === (locationScopeID ? 2 : 1))
    assert.equal(page.host.querySelector("[role='combobox'][aria-label='Office']"), null)
  }
  assert.deepEqual(page.agentCallsRequests.map((request) => request.locationId), [undefined, locations[1].id])
  await page.unmount()
})

test("each page keeps its title, actions, and filters inside the panel", async (t) => {
  const page = harness(t)
  page.knowledge = syntheticKnowledge
  page.agentCalls = {
    calls: [{ id: "call-1", phone: "+15555550111", startedAt: "2026-10-13T15:00:00Z", appointmentActions: [], transferred: false, issueFlagged: false, knowledgeGaps: [] }],
    nextCursor: "",
  }
  const showPage = (selected: "home" | "transcripts" | "knowledge" | "insurance") =>
    page.render(
      <ManageAgent actorSubject="staff-a" practiceID="practice-1" locationScopeID={locations[0].id} locations={locations} page={selected} onPage={() => {}} />,
      selected,
    )

  await showPage("transcripts")
  await waitFor(() => page.host.querySelector("header")?.textContent === "Calls1 call")
  assert.equal(page.host.querySelector("h1")?.textContent, "Calls")
  assert.equal(page.host.querySelector("header button[aria-label='Refresh calls']"), null)
  assert.ok(page.host.querySelector("input[aria-label='Search phone number']"))
  assert.ok(page.host.querySelector("[role='combobox'][aria-label='Date range']"))
  assert.equal(page.host.querySelector("section")?.getAttribute("aria-labelledby"), page.host.querySelector("h1")?.id)

  await showPage("knowledge")
  await waitFor(() => /Office hours/.test(page.text()))
  assert.match(page.host.querySelector("header")?.textContent ?? "", /^Knowledge baseUpdated .+ · Version 3History$/)
  assert.ok(page.host.querySelector("input[type='search']"))

  await showPage("insurance")
  await waitFor(() => page.groups().length === 3)
  assert.equal(page.host.querySelector("header")?.textContent, "Insurance list")
  assert.ok(page.host.querySelector("[role='combobox'][aria-label='Visit coverage']"))

  await showPage("home")
  await act(async () => page.button(/^Knowledge base/).click())
  await waitFor(() => /Version 3 · published/.test(page.text()))
  const greeting = homeGreeting(homeDay(new Date()))
  assert.equal(page.host.querySelector("h1 span")?.textContent, `${greeting.greeting},`)
  await page.unmount()
})

test("Calls updates automatically without replacing the table while refreshing and keeps retry available", async (t) => {
  const page = harness(t)
  const original = { id: "call-1", phone: "+15555550111", startedAt: "2026-10-13T15:00:00Z", appointmentActions: [], transferred: false, issueFlagged: false, knowledgeGaps: [] }
  page.agentCalls = { calls: [original], nextCursor: "" }
  const show = (version: number) => page.render(<LiveWorkspace version={version}><ManageAgent actorSubject="staff-a" practiceID="practice-1" locationScopeID={locations[0].id} locations={locations} page="transcripts" onPage={() => {}} /></LiveWorkspace>)
  await show(1)
  await waitFor(() => page.host.querySelectorAll("tbody tr").length === 1)
  assert.equal(page.host.querySelector("button[aria-label='Refresh calls']"), null)
  assert.equal(page.host.querySelector("table")?.parentElement?.parentElement?.className, "w-full")
  page.agentCalls = { calls: [{ ...original, id: "call-2", phone: "+15555550222" }, original], nextCursor: "" }
  let finishRefresh!: () => void
  page.agentCallsGate = new Promise<void>((resolve) => { finishRefresh = resolve })
  const table = page.host.querySelector("table")
  await show(2)
  await waitFor(() => page.agentCallsRequests.length === 2)
  assert.equal(page.host.querySelector("table") === table, true)
  assert.equal(page.host.querySelectorAll("tbody tr").length, 1)
  assert.equal(page.host.querySelector('[aria-label="Loading calls"]'), null)
  await act(async () => finishRefresh())
  await waitFor(() => page.host.querySelectorAll("tbody tr").length === 2)
  assert.match(page.text(), /555.*0222/)
  page.agentCallsStatus = 503
  await show(3)
  await waitFor(() => page.alert().includes("Calls aren't available right now."))
  page.agentCallsStatus = 200
  await page.click("Try again")
  await waitFor(() => page.host.querySelectorAll("tbody tr").length === 2)
  assert.deepEqual(page.agentCallsRequests.map((request) => request.locationId), Array(4).fill(locations[0].id))
  await page.unmount()
})

test("a live refresh keeps every loaded page of calls", async (t) => {
  const page = harness(t)
  const first = agentCall("call-1", "+15555550111")
  const second = agentCall("call-2", "+15555550222")
  page.agentCallsFor = (body) => body.cursor === "page-2" ? { calls: [second], nextCursor: "" } : { calls: [first], nextCursor: "page-2" }
  const show = (version: number) => page.render(<LiveWorkspace version={version}><ManageAgent actorSubject="staff-a" practiceID="practice-1" locationScopeID={locations[0].id} locations={locations} page="transcripts" onPage={() => {}} /></LiveWorkspace>)
  await show(1)
  await waitFor(() => page.callPhones().length === 1)
  await page.click("Load more calls")
  await waitFor(() => page.callPhones().length === 2)
  const newest = agentCall("call-0", "+15555550100")
  page.agentCallsFor = (body) => body.cursor === "page-2" ? { calls: [second], nextCursor: "" } : { calls: [newest, first], nextCursor: "page-2" }
  await show(2)
  await waitFor(() => page.callPhones().includes("(555) 555-0100"))
  assert.deepEqual(page.callPhones(), ["(555) 555-0100", "(555) 555-0111", "(555) 555-0222"])
  assert.deepEqual(page.agentCallsRequests.map((request) => request.cursor), [undefined, "page-2", undefined, "page-2"])
  assert.equal(page.host.querySelector('[aria-label="Loading calls"]'), null)
  await page.unmount()
})

test("a failed live refresh keeps the loaded calls visible beside a refresh error", async (t) => {
  const page = harness(t)
  const first = agentCall("call-1", "+15555550111")
  const second = agentCall("call-2", "+15555550222")
  page.agentCallsFor = (body) => body.cursor === "page-2" ? { calls: [second], nextCursor: "" } : { calls: [first], nextCursor: "page-2" }
  const show = (version: number) => page.render(<LiveWorkspace version={version}><ManageAgent actorSubject="staff-a" practiceID="practice-1" locationScopeID={locations[0].id} locations={locations} page="transcripts" onPage={() => {}} /></LiveWorkspace>)
  await show(1)
  await waitFor(() => page.callPhones().length === 1)
  await page.click("Load more calls")
  await waitFor(() => page.callPhones().length === 2)
  page.agentCallsStatus = 503
  await show(2)
  await waitFor(() => page.alert().includes("Calls aren't available right now."))
  assert.deepEqual(page.callPhones(), ["(555) 555-0111", "(555) 555-0222"])
  page.agentCallsStatus = 200
  await page.click("Try again")
  await waitFor(() => page.alert() === "")
  assert.deepEqual(page.callPhones(), ["(555) 555-0111", "(555) 555-0222"])
  await page.unmount()
})

test("a call that never loaded shows its error, and a failed reload after a flag conflict keeps the transcript beside a refresh error", async (t) => {
  const page = harness(t)
  const call = agentCall("call-1", "+15555550111")
  page.agentCalls = { calls: [call], nextCursor: "" }
  page.callDetail = {
    call,
    locationName: "Hollywood",
    messages: [{ speaker: "Caller", text: "Synthetic caller question", occurredAt: "2026-10-13T15:00:05Z" }],
    scorecard: [],
  }
  page.callStatus = 503
  await page.render(<ManageAgent actorSubject="staff-a" practiceID="practice-1" locationScopeID={locations[0].id} locations={locations} page="transcripts" onPage={() => {}} />)
  await waitFor(() => page.callPhones().length === 1)
  await act(async () => page.host.querySelector<HTMLElement>("button[aria-label^='Open call from']")!.click())
  await waitFor(() => page.sheetAlerts().some((alert) => alert.startsWith("Call unavailable")))
  assert.doesNotMatch(page.sheetText(), /Synthetic caller question/)

  page.callStatus = 200
  await page.sheetClick("Try again")
  await waitFor(() => /Synthetic caller question/.test(page.sheetText()))
  assert.deepEqual(page.sheetAlerts(), [])

  await page.sheetClick("Flag an issue")
  await act(async () => {
    const trigger = page.sheet()!.querySelector<HTMLElement>("[role='combobox']")!
    trigger.dispatchEvent(new window.MouseEvent("mousedown", { button: 0, bubbles: true }))
    trigger.click()
  })
  const other = Array.from(document.querySelectorAll<HTMLElement>("[role='option']")).find((item) => item.textContent === "Other")
  assert.ok(other)
  await act(async () => other.click())
  page.flagStatus = 409
  page.callStatus = 503
  let finishReload!: () => void
  page.callGate = new Promise<void>((resolve) => { finishReload = resolve })
  await page.sheetClick("Flag issue")
  await waitFor(() => page.callReads.length === 3)
  assert.match(page.sheetText(), /Saving…/)
  assert.ok(Array.from(page.sheet()!.querySelectorAll("button")).every((button) => button.textContent !== "Flag an issue" || button.disabled))
  await act(async () => finishReload())
  await waitFor(() => page.sheetAlerts().length === 1)
  assert.deepEqual(page.flagRequests, ["call-1"])
  assert.match(page.sheetAlerts()[0], /^Call could not refresh/)
  assert.match(page.sheetText(), /Synthetic caller question/)
  assert.doesNotMatch(page.sheetText(), /Call unavailable|Flag an issue/)

  page.callStatus = 200
  page.callDetail = { ...page.callDetail, issue: { reason: "WRONG_APPOINTMENT_TYPE", note: "Synthetic first report", createdAt: "2026-10-13T16:00:00Z" } }
  await page.sheetClick("Try again")
  await waitFor(() => page.sheetAlerts().length === 0)
  assert.match(page.sheetText(), /Issue flagged.*Wrong Appointment Type.*Synthetic first report/)
  assert.match(page.sheetText(), /Synthetic caller question/)
  assert.deepEqual(page.callReads, ["call-1", "call-1", "call-1", "call-1"])
  assert.ok(page.host.querySelector("tbody [aria-label='Issue flagged']"))
  await page.unmount()
})

test("insurance plans are grouped by result, sorted by name, summarized in one row, expand in place, and collapse by group", async (t) => {
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
  assert.equal(page.host.querySelector("[role='combobox'][aria-label='Location']"), null)
  assert.match(page.host.querySelector("[role='combobox'][aria-label='Visit coverage']")?.textContent ?? "", /^Medical/)
  assert.equal(Array.from(page.host.querySelectorAll("input")).filter((input) => input.getAttribute("aria-hidden") !== "true").length, 0)
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
        locationID={locationID}
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
  await waitFor(() => /No medical plans are on this office's insurance list\./.test(page.text()))

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
  await waitFor(() => /No knowledge has been imported for this office yet\./.test(page.text()))
  assert.doesNotMatch(page.text(), /Updated/)

  page.knowledgeStatus = 503
  page.knowledgeError = { code: "UNAVAILABLE", message: "Knowledge isn't available for this Location.", correlationId: "synthetic", retryable: false }
  await page.render(knowledgeBase(locations[1].id), "second")
  await waitFor(() => page.alert() !== "")
  assert.equal(page.alert(), "Knowledge isn't available for this Location.")
  assert.equal(page.knowledgeRequests.at(-1)?.locationId, locations[1].id)
  await page.unmount()
})

const syntheticKnowledge: LocationKnowledge = {
  locationId: locations[0].id,
  revision: { id: "00000000-0000-4000-8000-0000000000a1", version: 3, createdAt: "2026-09-30T15:00:00Z" },
  sections: [
    { id: "hours", title: "Office hours", text: "Monday to Friday\n8am to 5pm" },
    { id: "parking", title: "Parking", text: "Synthetic garage on site." },
  ],
}

test("several edits wait beside the search bar, outside the scrolling list, and publish together", async (t) => {
  const page = harness(t)
  page.knowledge = syntheticKnowledge
  await page.render(knowledgeBase())
  await waitFor(() => /Office hours/.test(page.text()))
  await page.rowAction("Office hours", "Edit")
  await page.fill(page.field("Text"), "Closes at six")
  await page.rowAction("Office hours", "Done")
  assert.match(page.text(), /1 unpublished change(?!s)/)
  await page.rowAction("Parking", "Edit")
  await page.fill(page.field("Text"), "Synthetic valet only.")
  await page.click("Add entry")
  await page.fill(page.field("Title"), "Fax")
  await page.fill(page.field("Text"), "Synthetic fax 555-0100.")
  assert.match(page.text(), /3 unpublished changes/)

  const publish = page.button(/^Publish$/)
  const search = page.host.querySelector("input[type='search']")!
  assert.equal(publish.closest(".overflow-auto"), null)
  assert.equal(publish.closest("fieldset")?.parentElement, search.closest("[data-slot='input-group']")?.parentElement)

  await page.fill(page.field("Why are you changing this?"), "New hours, valet, and fax")
  page.publishResult = { id: "00000000-0000-4000-8000-0000000000a4", version: 4, createdAt: "2026-10-12T18:30:00Z" }
  await page.click("Publish")
  await waitFor(() => page.publishRequests.length === 1 && page.knowledgeRequests.length === 2)
  assert.deepEqual(page.publishRequests[0].sections, [
    { id: "hours", title: "Office hours", text: "Closes at six" },
    { id: "parking", title: "Parking", text: "Synthetic valet only." },
    { id: "fax", title: "Fax", text: "Synthetic fax 555-0100." },
  ])
  await page.unmount()
})

test("adding an entry gives it an ID from its title and removing an entry leaves it out", async (t) => {
  const page = harness(t)
  page.knowledge = syntheticKnowledge
  await page.render(knowledgeBase())
  await waitFor(() => /Office hours/.test(page.text()))
  await page.rowAction("Parking", "Remove")
  assert.deepEqual(page.rowTitles(), ["Office hours"])
  await page.click("Add entry")
  await page.fill(page.field("Title"), "Café & valet parking")
  await page.fill(page.field("Text"), "Synthetic valet at the front door.")
  await page.fill(page.field("Why are you changing this?"), "Garage closed")
  page.publishResult = { id: "00000000-0000-4000-8000-0000000000a3", version: 4, createdAt: "2026-10-12T18:30:00Z" }
  await page.click("Publish")
  await waitFor(() => page.publishRequests.length === 1 && page.knowledgeRequests.length === 2)
  assert.deepEqual(page.publishRequests[0].sections, [
    { id: "hours", title: "Office hours", text: "Monday to Friday\n8am to 5pm" },
    { id: "cafe-valet-parking", title: "Café & valet parking", text: "Synthetic valet at the front door." },
  ])
  await page.unmount()
})

test("a rejected, conflicting, or failed publish says why and keeps the unpublished changes", async (t) => {
  const page = harness(t)
  page.knowledge = syntheticKnowledge
  await page.render(knowledgeBase())
  await waitFor(() => /Office hours/.test(page.text()))
  await page.rowAction("Office hours", "Edit")
  await page.fill(page.field("Text"), "**Monday** to Friday")
  await page.fill(page.field("Why are you changing this?"), "Bold the days")

  page.publishStatus = 400
  page.publishError = { code: "INVALID_KNOWLEDGE_ENTRY", message: "Entry \"hours\" uses markdown formatting; write plain text.", correlationId: "synthetic", retryable: false, entryId: "hours" }
  await page.click("Publish")
  await waitFor(() => page.alert() !== "")
  assert.equal(page.alert(), "Not published. Fix “Office hours” below.")
  const hours = page.button(/^Office hours$/).closest("li")!
  assert.equal(hours.querySelector("[role='alert']")?.textContent, "Entry \"hours\" uses markdown formatting; write plain text.")
  const rejected = page.field("Text")
  await page.fill(rejected, "**Monday** to Friday, closed weekends")
  assert.equal(page.field("Text"), rejected)
  assert.equal(page.alert(), "")
  await page.fill(rejected, "**Monday** to Friday")

  for (const [status, error, message] of [
    [409, { code: "KNOWLEDGE_CHANGED", message: "Someone else changed this office's knowledge. Reload to see their changes.", correlationId: "synthetic", retryable: false }, "Someone else changed this office's knowledge. Reload to see their changes."],
    [503, { code: "UNAVAILABLE", message: "Not published. Nothing changed. Try again.", correlationId: "synthetic", retryable: true }, "Not published. Nothing changed. Try again."],
    [503, undefined, "Not published. Nothing changed. Try again."],
  ] as const) {
    page.publishStatus = status
    page.publishError = error
    await page.click("Publish")
    await waitFor(() => page.alert() === message)
    assert.equal(page.field("Text").value, "**Monday** to Friday")
    assert.equal(page.field("Why are you changing this?").value, "Bold the days")
  }
  assert.equal(page.publishRequests.length, 4)
  assert.equal(new Set(page.publishRequests.map((request) => request.revisionId)).size, 1)
  assert.equal(page.knowledgeRequests.length, 1)

  await page.click("Discard changes")
  await waitFor(() => page.knowledgeRequests.length === 2 && /Office hours/.test(page.text()))
  assert.equal(page.maybeButton("Publish"), undefined)
  await page.unmount()
})

test("unpublished changes survive leaving the page and the browser asks before unloading them", async (t) => {
  const page = harness(t)
  page.knowledge = syntheticKnowledge
  await page.render(knowledgeBase())
  await waitFor(() => /Office hours/.test(page.text()))
  assert.equal(page.unload(), false)
  await page.rowAction("Parking", "Remove")
  await page.fill(page.field("Why are you changing this?"), "Garage closed")
  assert.equal(page.unload(), true)

  await page.render(insuranceList())
  await waitFor(() => page.groups().length === 3)
  assert.equal(page.unload(), true)
  await page.render(knowledgeBase())
  await waitFor(() => /Office hours/.test(page.text()))
  assert.deepEqual(page.rowTitles(), ["Office hours"])
  assert.equal(page.field("Why are you changing this?").value, "Garage closed")

  await page.click("Discard changes")
  await waitFor(() => page.rowTitles().length === 2)
  assert.equal(page.unload(), false)

  await page.rowAction("Parking", "Remove")
  assert.equal(page.unload(), true)
  await page.render(insuranceList())
  await authClient.signOut({ fetchOptions: { baseURL: "http://localhost/api/auth", customFetchImpl: async () => Response.json({ success: true }) } })
  assert.equal(page.unload(), false)
  await page.render(knowledgeBase(), "after sign-out")
  await waitFor(() => page.rowTitles().length === 2)
  assert.equal(page.maybeButton("Publish"), undefined)
  await page.unmount()
})

test("editing an entry and publishing with a reason sends the full list, stays locked after leaving and returning, then shows the new revision", async (t) => {
  const page = harness(t)
  page.knowledge = syntheticKnowledge
  await page.render(knowledgeBase())
  await waitFor(() => /Office hours/.test(page.text()))
  assert.equal(page.maybeButton("Publish"), undefined)

  await page.rowAction("Office hours", "Edit")
  await page.fill(page.field("Text"), "Closes at six")
  assert.equal(page.button(/^Publish$/).disabled, true)
  await page.fill(page.field("Why are you changing this?"), "   ")
  assert.equal(page.button(/^Publish$/).disabled, true)
  await page.fill(page.field("Why are you changing this?"), "Later closing")
  assert.equal(page.button(/^Publish$/).disabled, false)

  page.publishResult = { id: "00000000-0000-4000-8000-0000000000a3", version: 4, createdAt: "2026-10-12T19:00:00Z" }
  page.afterPublish = { ...syntheticKnowledge, revision: page.publishResult, sections: [{ ...syntheticKnowledge.sections[0], text: "Closes at six" }, syntheticKnowledge.sections[1]] }
  let release!: () => void
  page.publishGate = new Promise((resolve) => { release = resolve })
  await page.click("Publish")
  await waitFor(() => page.maybeButton("Publishing…") !== undefined)
  assert.equal(page.maybeButton("Publishing…")!.closest("fieldset")?.disabled, true)
  assert.equal(page.field("Text").closest("fieldset")?.disabled, true)

  await page.render(insuranceList())
  await waitFor(() => page.groups().length === 3)
  await page.render(knowledgeBase(), "returned")
  await waitFor(() => page.maybeButton("Publishing…") !== undefined && /Office hours/.test(page.text()))
  assert.equal(page.maybeButton("Publishing…")!.closest("fieldset")?.disabled, true)

  release()
  await waitFor(() => page.maybeButton("Publishing…") === undefined && page.host.querySelector("time")?.getAttribute("dateTime") === "2026-10-12T19:00:00Z")
  const [publication] = page.publishRequests
  assert.match(publication.revisionId, /^[0-9a-f-]{36}$/)
  assert.deepEqual({ ...publication, revisionId: "" }, {
    practiceId: "practice-1",
    locationId: locations[0].id,
    revisionId: "",
    expectedRevisionId: syntheticKnowledge.revision!.id,
    reason: "Later closing",
    sections: [
      { id: "hours", title: "Office hours", text: "Closes at six" },
      { id: "parking", title: "Parking", text: "Synthetic garage on site." },
    ],
  })
  assert.match(page.host.querySelector("header")?.textContent ?? "", /Version 4/)
  assert.equal(page.maybeButton("Publish"), undefined)
  assert.equal(page.unload(), false)
  await act(async () => page.button(/^Office hours$/).click())
  assert.match(page.text(), /Closes at six/)
  await page.unmount()
})

test("a publish that fails after leaving and returning says why, keeps the changes, and opens a rejected entry beside its message", async (t) => {
  const page = harness(t)
  page.knowledge = syntheticKnowledge
  const cases = [
    {
      text: "Closes at six",
      status: 409,
      error: { code: "KNOWLEDGE_CHANGED", message: "Someone else changed this office's knowledge. Reload to see their changes.", correlationId: "synthetic", retryable: false },
      settled: () => page.alert() === "Someone else changed this office's knowledge. Reload to see their changes.",
      check: () => {
        assert.equal(page.field("Why are you changing this?").value, "Later closing")
        assert.equal(page.button(/^Publish$/).disabled, false)
      },
    },
    {
      text: "**Closes** at six",
      status: 400,
      error: { code: "INVALID_KNOWLEDGE_ENTRY", message: "Entry \"hours\" uses markdown formatting; write plain text.", correlationId: "synthetic", retryable: false, entryId: "hours" },
      settled: () => page.maybeButton("Publishing…") === undefined,
      check: () => {
        const hours = page.button(/^Office hours$/)
        assert.equal(hours.getAttribute("aria-expanded"), "true")
        assert.equal(hours.closest("li")!.querySelector("[role='alert']")?.textContent, "Entry \"hours\" uses markdown formatting; write plain text.")
      },
    },
  ]
  for (const { text, status, error, settled, check } of cases) {
    await page.render(knowledgeBase())
    await waitFor(() => /Office hours/.test(page.text()) && page.maybeButton("Publish") === undefined)
    await page.rowAction("Office hours", "Edit")
    await page.fill(page.field("Text"), text)
    await page.fill(page.field("Why are you changing this?"), "Later closing")
    page.publishStatus = status
    page.publishError = error
    let release!: () => void
    page.publishGate = new Promise((resolve) => { release = resolve })
    await page.click("Publish")
    await waitFor(() => page.maybeButton("Publishing…") !== undefined)

    await page.render(insuranceList())
    await waitFor(() => page.groups().length === 3)
    await page.render(knowledgeBase(), "returned")
    await waitFor(() => page.maybeButton("Publishing…") !== undefined)
    release()
    await waitFor(settled)
    check()
    await page.click("Discard changes")
  }
  await page.unmount()
})

test("unpublished changes belong to the person who made them", async (t) => {
  const page = harness(t)
  page.knowledge = syntheticKnowledge
  await page.render(knowledgeBase(locations[0].id, "staff-a"))
  await waitFor(() => /Office hours/.test(page.text()))
  await page.rowAction("Parking", "Remove")
  await page.fill(page.field("Why are you changing this?"), "Garage closed")

  await page.render(knowledgeBase(locations[0].id, "staff-b"), "someone else")
  await waitFor(() => page.rowTitles().length === 2)
  assert.equal(page.maybeButton("Publish"), undefined)

  await page.render(knowledgeBase(locations[0].id, "staff-a"), "back again")
  await waitFor(() => page.rowTitles().length === 1)
  assert.equal(page.field("Why are you changing this?").value, "Garage closed")
  await page.click("Discard changes")
  await page.unmount()
})

const versionOne: LocationKnowledge = {
  locationId: locations[0].id,
  revision: { id: "00000000-0000-4000-8000-0000000000b1", version: 1, createdAt: "2026-09-01T15:00:00Z" },
  sections: [
    { id: "hours", title: "Office hours", text: "Monday to Friday\n8am to 4pm" },
    { id: "fax", title: "Fax", text: "Synthetic fax 555-0100." },
  ],
}
const versionTwo: LocationKnowledge = {
  locationId: locations[0].id,
  revision: { id: "00000000-0000-4000-8000-0000000000b2", version: 2, createdAt: "2026-09-15T15:00:00Z" },
  sections: [versionOne.sections[0], syntheticKnowledge.sections[1]],
}

function historyEntry(knowledge: LocationKnowledge, change: Partial<KnowledgeRevisionHistoryEntry>): KnowledgeRevisionHistoryEntry {
  return { ...knowledge.revision!, author: "Unknown user", provenance: "portal", reason: "", active: false, agentSearchCount: 0, ...change }
}

const syntheticHistory: LocationKnowledgeHistory = {
  locationId: locations[0].id,
  revisions: [
    historyEntry(syntheticKnowledge, { author: "staff@abita.test", reason: "Office closes later", active: true, previousRevisionId: versionTwo.revision!.id, lastAgentSearchAt: "2026-10-01T16:00:00Z", agentSearchCount: 4 }),
    historyEntry(versionTwo, { author: "operator@acuity.test", reason: "Add parking", previousRevisionId: versionOne.revision!.id }),
    historyEntry(versionOne, { provenance: "git:abc123", reason: "initial import", lastAgentSearchAt: "2026-09-10T16:00:00Z", agentSearchCount: 1 }),
  ],
}

function withHistory(page: ReturnType<typeof harness>) {
  page.knowledge = syntheticKnowledge
  page.history = syntheticHistory
  page.revisions = { [syntheticKnowledge.revision!.id]: syntheticKnowledge, [versionTwo.revision!.id]: versionTwo, [versionOne.revision!.id]: versionOne }
}

async function openHistory(page: ReturnType<typeof harness>, onRestored = () => {}) {
  await page.render(<KnowledgeHistory practiceID="practice-1" locationID={locations[0].id} onRestored={onRestored} />)
  await waitFor(() => page.historyRows().length === page.history.revisions.length)
}

async function selectVersion(page: ReturnType<typeof harness>, version: number) {
  const back = page.maybeButton("Back to history")
  if (back) await act(async () => back.click())
  const row = Array.from(page.host.querySelectorAll<HTMLButtonElement>("li button")).find((item) => item.textContent?.startsWith(`Version ${version}`))
  assert.ok(row, `Version ${version}`)
  await act(async () => row.click())
}

test("the header shows when the active version was published, its number, and a History button above collapsed sections that keep line breaks", async (t) => {
  const page = harness(t)
  withHistory(page)
  await page.render(knowledgeBase())
  await waitFor(() => /Office hours/.test(page.text()))
  const header = page.host.querySelector("header")!
  assert.match(header.textContent ?? "", /Updated .+ · Version 3History$/)
  assert.ok(Array.from(header.querySelectorAll("button")).some((button) => button.textContent === "History"))
  assert.deepEqual(page.knowledgeRequests, [{ practiceId: "practice-1", locationId: locations[0].id }])
  assert.equal(page.host.querySelector("time")?.getAttribute("dateTime"), "2026-09-30T15:00:00Z")
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

test("History lists every revision newest first with its author, reason, and agent use, and selecting one shows what changed since the version before it", async (t) => {
  const page = harness(t)
  withHistory(page)
  await openHistory(page)
  assert.deepEqual(page.historyRequests, [{ practiceId: "practice-1", locationId: locations[0].id }])
  const rows = page.historyRows()
  assert.match(rows[0], /^Version 3Active.*staff@abita\.test.*Office closes later.*Last used by the agent: .+ · 4 searches$/)
  assert.match(rows[1], /^Version 2.*operator@acuity\.test.*Add parking.*Not used by the agent yet$/)
  assert.doesNotMatch(rows[1], /Active/)
  assert.match(rows[2], /^Version 1.*Unknown user.*initial import.*Last used by the agent: .+ · 1 search$/)

  await selectVersion(page, 2)
  await waitFor(() => /Added/.test(page.text()))
  assert.deepEqual(page.revisionRequests.map((request) => request.revisionId).sort(), [versionOne.revision!.id, versionTwo.revision!.id])
  assert.match(page.text(), /Compared with version 1/)
  assert.match(page.text(), /Added.*Parking.*Synthetic garage on site\./)
  assert.match(page.text(), /Removed.*Fax.*Synthetic fax 555-0100\./)
  assert.doesNotMatch(page.text(), /Changed/)

  await selectVersion(page, 3)
  await waitFor(() => /Changed/.test(page.text()))
  assert.match(page.text(), /Changed.*Office hours.*Before[\s\S]*8am to 4pm.*After[\s\S]*8am to 5pm/)
  assert.doesNotMatch(page.text(), /Added|Removed/)
  assert.equal(page.maybeButton("Restore this version"), undefined)

  await selectVersion(page, 1)
  await waitFor(() => /Fax/.test(page.text()))
  assert.match(page.text(), /First version.*Added.*Office hours[\s\S]*Fax/)
  await page.unmount()
})

test("restoring a past revision publishes its entries as a new version with a reason", async (t) => {
  const page = harness(t)
  withHistory(page)
  let restoredCount = 0
  await openHistory(page, () => restoredCount++)
  await selectVersion(page, 1)
  await waitFor(() => page.maybeButton("Restore this version") !== undefined)
  await page.click("Restore this version")
  assert.equal(page.maybeButton("Restore")!.disabled, true)
  await page.fill(page.field("Why restore this version?"), "Undo the parking change")
  assert.equal(page.maybeButton("Restore")!.disabled, false)

  const restored = { id: "00000000-0000-4000-8000-0000000000c4", version: 4, createdAt: "2026-10-12T20:00:00Z" }
  page.publishResult = restored
  page.afterPublishHistory = {
    ...syntheticHistory,
    revisions: [
      historyEntry({ ...versionOne, revision: restored }, { author: "staff@abita.test", reason: "Undo the parking change", active: true, previousRevisionId: syntheticKnowledge.revision!.id }),
      ...syntheticHistory.revisions.map((row) => ({ ...row, active: false })),
    ],
  }
  await page.click("Restore")
  await waitFor(() => page.historyRows().length === 4)
  const [publication] = page.publishRequests
  assert.match(publication.revisionId, /^[0-9a-f-]{36}$/)
  assert.deepEqual({ ...publication, revisionId: "" }, {
    practiceId: "practice-1",
    locationId: locations[0].id,
    revisionId: "",
    expectedRevisionId: syntheticKnowledge.revision!.id,
    reason: "Undo the parking change",
    sections: versionOne.sections,
  })
  assert.equal(restoredCount, 1)
  assert.match(page.historyRows()[0], /^Version 4Active.*Undo the parking change/)
  await page.unmount()
})

test("a restore against a stale active revision says so and changes nothing", async (t) => {
  const page = harness(t)
  withHistory(page)
  let restoredCount = 0
  await openHistory(page, () => restoredCount++)
  await selectVersion(page, 1)
  await waitFor(() => page.maybeButton("Restore this version") !== undefined)
  await page.click("Restore this version")
  await page.fill(page.field("Why restore this version?"), "Undo the parking change")
  page.publishStatus = 409
  page.publishError = { code: "KNOWLEDGE_CHANGED", message: "Someone else changed this office's knowledge. Reload to see their changes.", correlationId: "synthetic", retryable: false }
  await page.click("Restore")
  await waitFor(() => page.text().includes("Someone else changed this office's knowledge. Reload to see their changes."))
  assert.equal(page.publishRequests.length, 1)
  assert.equal(page.historyRequests.length, 1)
  assert.equal(restoredCount, 0)
  assert.equal(page.field("Why restore this version?").value, "Undo the parking change")
  await page.unmount()
})

function insuranceList(locationScopeID = locations[0].id) {
  return <ManageAgent actorSubject="staff-a" practiceID="practice-1" locationScopeID={locationScopeID} locations={locations} page="insurance" onPage={() => {}} />
}

function knowledgeBase(locationScopeID = locations[0].id, actorSubject = "staff-a") {
  return <ManageAgent actorSubject={actorSubject} practiceID="practice-1" locationScopeID={locationScopeID} locations={locations} page="knowledge" onPage={() => {}} />
}

function LiveWorkspace({ version, children }: { version: number; children: ReactNode }) {
  useLiveRefresh({ workspaceVersion: version, taskCounts: "", detailRevision: 0 })
  return children
}

function agentCall(id: string, phone: string): AgentCallsPage["calls"][number] {
  return { id, phone, startedAt: "2026-10-13T15:00:00Z", appointmentActions: [], transferred: false, issueFlagged: false, knowledgeGaps: [] }
}

function plan(change: Partial<InsurancePlanRule> & Pick<InsurancePlanRule, "planId" | "label" | "outcome">): InsurancePlanRule {
  return { names: [], allowedProviders: [], requirements: [], acceptedAt: [], ...change }
}

type Envelope = { code: string; message: string; correlationId: string; retryable: boolean; entryId?: string }

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
    requestAnimationFrame: { configurable: true, value: () => 0 },
    cancelAnimationFrame: { configurable: true, value: () => {} },
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
    publishRequests: [] as KnowledgePublication[],
    publishStatus: 200,
    publishError: undefined as Envelope | undefined,
    publishResult: { id: "", version: 1, createdAt: "" } as KnowledgeRevisionSummary,
    afterPublish: undefined as LocationKnowledge | undefined,
    historyRequests: [] as LocationKnowledgeQuery[],
    history: { locationId: "", revisions: [] } as LocationKnowledgeHistory,
    afterPublishHistory: undefined as LocationKnowledgeHistory | undefined,
    revisionRequests: [] as LocationKnowledgeRevisionQuery[],
    revisions: {} as Record<string, LocationKnowledge>,
    agentCallsRequests: [] as AgentCallsQuery[],
    agentCalls: { calls: [], nextCursor: "" } as AgentCallsPage,
    agentCallsStatus: 200,
    agentCallsGate: Promise.resolve(),
    agentCallsFor: ((): AgentCallsPage => page.agentCalls) as (body: AgentCallsQuery) => AgentCallsPage,
    callReads: [] as string[],
    callStatus: 200,
    callGate: Promise.resolve(),
    callDetail: undefined as AgentCallDetail | undefined,
    flagRequests: [] as string[],
    flagStatus: 200,
    sheet: () => document.querySelector("[data-slot='sheet-content']"),
    sheetText: () => page.sheet()?.textContent ?? "",
    sheetAlerts: () => Array.from(page.sheet()?.querySelectorAll("[role='alert']") ?? [], (alert) => alert.textContent ?? ""),
    sheetClick: async (name: string) => {
      const button = Array.from(page.sheet()?.querySelectorAll("button") ?? []).find((item) => item.textContent === name)
      assert.ok(button, name)
      await act(async () => button.click())
    },
    callPhones: () => Array.from(host.querySelectorAll("tbody tr td:nth-child(2)"), (cell) => cell.textContent ?? ""),
    historyRows: () => Array.from(host.querySelectorAll("li"), (row) => row.textContent ?? ""),
    publishGate: Promise.resolve(),
    render: (element: ReactNode, key = "first") =>
      act(async () => root.render(<DataProvider><SidebarProvider key={key}>{element}</SidebarProvider></DataProvider>)),
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
    maybeButton: (name: string) =>
      Array.from(host.querySelectorAll("button")).find((item) => item.textContent === name),
    field: (label: string) => {
      const field = Array.from(host.querySelectorAll<HTMLInputElement | HTMLTextAreaElement>("input, textarea")).find(
        (item) => item.labels?.[0]?.textContent === label,
      )
      assert.ok(field, label)
      return field
    },
    fill: (field: HTMLInputElement | HTMLTextAreaElement, value: string) =>
      act(async () => {
        const prototype = field instanceof window.HTMLTextAreaElement ? window.HTMLTextAreaElement.prototype : window.HTMLInputElement.prototype
        Object.assign(field, { attachEvent() {}, detachEvent() {} })
        field.focus()
        Object.getOwnPropertyDescriptor(prototype, "value")!.set!.call(field, value)
        field.dispatchEvent(new window.KeyboardEvent("keyup", { bubbles: true }))
      }),
    unload: () => {
      const event = new window.Event("beforeunload", { cancelable: true })
      window.dispatchEvent(event)
      return event.defaultPrevented
    },
    rowAction: async (title: string, name: string) => {
      const row = page.button(new RegExp(`^${title}$`))
      if (row.getAttribute("aria-expanded") === "false") await act(async () => row.click())
      const button = Array.from(row.closest("li")!.querySelectorAll("button")).find((item) => item.textContent === name)
      assert.ok(button, `${title}: ${name}`)
      await act(async () => button.click())
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
    if (url.pathname === "/v1/knowledge/revisions/query") {
      page.historyRequests.push((await request.json()) as LocationKnowledgeQuery)
      return Response.json(page.history)
    }
    if (url.pathname === "/v1/knowledge/revisions/read") {
      const body = (await request.json()) as LocationKnowledgeRevisionQuery
      page.revisionRequests.push(body)
      return Response.json(page.revisions[body.revisionId])
    }
    if (url.pathname === "/v1/knowledge/revisions") {
      page.publishRequests.push((await request.json()) as KnowledgePublication)
      await page.publishGate
      if (page.publishStatus !== 200) return failure(page.publishStatus, page.publishError)
      if (page.afterPublish) page.knowledge = page.afterPublish
      if (page.afterPublishHistory) page.history = page.afterPublishHistory
      return Response.json(page.publishResult)
    }
    const callPath = /^\/v1\/agent-calls\/([^/]+)(\/issue)?$/.exec(url.pathname)
    if (callPath && request.method !== "POST") {
      if (callPath[2]) {
        page.flagRequests.push(callPath[1])
        return new Response("{}", { status: page.flagStatus })
      }
      page.callReads.push(callPath[1])
      await page.callGate
      return page.callStatus === 200 ? Response.json(page.callDetail) : new Response("", { status: page.callStatus })
    }
    assert.equal(url.pathname, "/v1/agent-calls/query")
    const agentCallsQuery = (await request.json()) as AgentCallsQuery
    page.agentCallsRequests.push(agentCallsQuery)
    await page.agentCallsGate
    return page.agentCallsStatus === 200 ? Response.json(page.agentCallsFor(agentCallsQuery)) : new Response("", { status: page.agentCallsStatus })
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
