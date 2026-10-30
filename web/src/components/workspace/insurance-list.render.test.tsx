import assert from "node:assert/strict"
import test, { type TestContext } from "node:test"
import { act, type ReactNode } from "react"
import { createRoot } from "react-dom/client"
import { JSDOM } from "jsdom"

import { clearAccessToken } from "../../lib/auth-client.ts"
import { DataProvider } from "../../lib/data/provider.tsx"
import { clearDrafts } from "../../lib/drafts.ts"
import type {
  InsuranceCatalog,
  InsuranceCatalogQuery,
  InsurancePublication,
  InsuranceRevision,
  InsuranceValidationError,
  Location,
  LocationInsuranceCatalog,
} from "../../lib/api/generated/types.gen.ts"

Object.defineProperty(globalThis, "document", { configurable: true, value: new JSDOM("").window.document })
const { SidebarProvider } = await import("@/components/ui/sidebar")
const { ManageAgent } = await import("./manage-agent.tsx")

const locations: Location[] = [
  { id: "00000000-0000-4000-8000-000000000001", name: "Hollywood" },
  { id: "00000000-0000-4000-8000-000000000002", name: "North Miami Beach Optical" },
  { id: "00000000-0000-4000-8000-000000000003", name: "Spring Hill" },
]
const offices: Record<string, string> = {
  [locations[0].id]: "hollywood",
  [locations[1].id]: "north_miami_beach_optical",
  [locations[2].id]: "spring_hill",
}

const imported: InsuranceRevision = {
  id: "00000000-0000-4000-8000-0000000000c1",
  createdAt: "2026-10-30T15:00:00Z",
  createdBy: "operator@abita.test",
  reason: "initial import",
  provenance: "import",
}

const published: InsuranceCatalog = {
  carriers: [
    { id: "car10002", name: "DEMO MEDICAID" },
    { id: "car10001", name: "EXAMPLE CHOICE HEALTH" },
    { id: "car10003", name: "ZETA VISION" },
  ],
  regions: [
    {
      id: "south_florida",
      plans: [
        { id: "example-choice-medical", label: "Example Choice PPO", family: "Example", coverage: "medical", carrierCode: "EXC01", carrierId: "car10001", selfPay: false, aliases: ["Example Choice", "Choice PPO"] },
        { id: "demo-medicaid", label: "Demo Medicaid MMA", family: "Demo", coverage: "medical", carrierCode: "DEM03", carrierId: "car10002", selfPay: false, aliases: [] },
        { id: "zeta-vision", label: "Zeta Vision Plan", family: "Zeta", coverage: "routine_vision", carrierCode: "ZET01", carrierId: "car10003", selfPay: false, aliases: [] },
        { id: "self-pay-medical", label: "Self-pay", family: "Self-pay", coverage: "medical", carrierCode: "", carrierId: "", selfPay: true, aliases: [] },
        { id: "example-select", label: "Example Select HMO", family: "Example", coverage: "medical", carrierCode: "EXC02", carrierId: "car10001", selfPay: false, aliases: [] },
      ],
      tables: [
        {
          id: "doctors",
          offices: ["hollywood", "sweetwater", "north_miami_beach_optical"],
          doctors: ["Dr. Rivera", "Dr. Chen"],
          rows: [
            { planId: "example-choice-medical", doctors: { "Dr. Chen": "yes", "Dr. Rivera": "yes" }, requires: [{ kind: "prior_authorization", channel: "availity" }], onlyOffices: [], notice: "Synthetic caller notice", note: "Synthetic sheet note" },
            { planId: "demo-medicaid", doctors: { "Dr. Chen": "pending", "Dr. Rivera": "no" }, requires: [{ kind: "pcp_referral", channel: "" }], onlyOffices: ["hollywood"], notice: "", note: "" },
            { planId: "zeta-vision", doctors: { "Dr. Rivera": "yes" }, requires: [], onlyOffices: [], notice: "", note: "" },
            { planId: "self-pay-medical", doctors: {}, requires: [], onlyOffices: [], notice: "Self-pay rates apply", note: "" },
          ],
        },
      ],
    },
    {
      id: "spring_hill_crystal_river",
      plans: [{ id: "spring-plan", label: "Spring Plan HMO", family: "Spring", coverage: "medical", carrierCode: "EXC01", carrierId: "car10001", selfPay: false, aliases: [] }],
      tables: [
        { id: "spring_hill", offices: ["spring_hill"], doctors: ["Dr. Licht"], rows: [{ planId: "spring-plan", doctors: { "Dr. Licht": "yes" }, requires: [], onlyOffices: [], notice: "", note: "" }] },
        { id: "crystal_river", offices: ["crystal_river"], doctors: ["Dr. Licht"], rows: [] },
      ],
    },
  ],
}

test.afterEach(() => clearDrafts())

test("without access to every office on its table, the office's plans read in coverage groups with every detail and nothing to edit", async (t) => {
  const page = harness(t)
  page.canEdit = false
  await page.render(insuranceList())
  await waitFor(() => page.groups().length === 2)
  assert.deepEqual(page.requests, [{ practiceId: "practice-1", locationId: locations[0].id }])
  assert.match(page.header(), /^Insurance listPublished .+ by operator@abita\.test$/)
  assert.deepEqual(page.groups(), [
    {
      title: "Medical3",
      rows: [
        "Demo Medicaid MMANo doctors take it · 1 pending · Only at HollywoodDEM03",
        "Example Choice PPODr. Rivera, Dr. ChenEXC01",
        "Self-payNo doctors take it",
      ],
    },
    { title: "Routine vision1", rows: ["Zeta Vision PlanDr. RiveraZET01"] },
  ])
  await page.open("Example Choice PPO")
  assert.deepEqual(page.details("Example Choice PPO"), [
    "FamilyExample",
    "Visit coverageMedical",
    "AMD carrierEXAMPLE CHOICE HEALTH · EXC01 · car10001",
    "Callers also sayExample Choice, Choice PPO",
    "DoctorsDr. Rivera: Yes\nDr. Chen: Yes",
    "RequiresPrior auth (availity)",
    "Callers hearSynthetic caller notice",
    "Staff noteSynthetic sheet note",
  ])
  assert.deepEqual(page.buttonNames().filter((name) => ["Edit", "Remove", "Add plan", "Publish"].includes(name)), [])
  assert.deepEqual(page.inputs(), ["Search plans"])
  await page.unmount()
})

test("removing a plan from the office's table publishes the full catalog without its row and re-renders the list without it", async (t) => {
  const page = harness(t)
  await page.render(insuranceList())
  await waitFor(() => page.groups().length === 2)
  await page.rowAction("Demo Medicaid MMA", "Remove")
  assert.equal(page.button("Publish").disabled, true)
  await page.fill(page.field("Why are you changing this?"), "   ")
  assert.equal(page.button("Publish").disabled, true)
  await page.fill(page.field("Why are you changing this?"), "Office stopped taking Demo Medicaid")
  assert.equal(page.button("Publish").disabled, false)
  assert.match(page.text(), /1 unpublished change/)
  await page.click("Publish")
  await waitFor(() => page.maybeButton("Publish") === undefined && page.header().includes("staff@abita.test"))

  const [publication] = page.publications
  assert.match(publication.revisionId, /^[0-9a-f-]{36}$/)
  assert.equal(publication.expectedRevisionId, imported.id)
  assert.equal(publication.reason, "Office stopped taking Demo Medicaid")
  assert.deepEqual(publication.catalog.regions[0].tables[0].rows.map((row) => row.planId), ["example-choice-medical", "zeta-vision", "self-pay-medical"])
  assert.deepEqual(publication.catalog.regions[0].plans, published.regions[0].plans)
  assert.deepEqual(publication.catalog.regions[1], published.regions[1])
  assert.deepEqual(page.active.catalog, publication.catalog)
  assert.deepEqual(page.groups()[0].rows.map(title), ["Example Choice PPO", "Self-pay"])
  assert.equal(page.unload(), false)
  await page.unmount()
})

test("adding a new plan with each doctor set to yes publishes it with a carrier chosen from the catalog's pairs", async (t) => {
  const page = harness(t)
  await page.render(insuranceList())
  await waitFor(() => page.groups().length === 2)
  await page.click("Add plan")
  assert.deepEqual(page.options("Plan"), ["A new plan", "Example Select HMO (Medical)"])
  await page.fill(page.field("New plan label"), "Acme Health HMO")
  await page.fill(page.field("New plan family"), "Acme")
  await page.click("Add")
  await page.choose(page.field("Dr. Rivera"), "yes")
  await page.choose(page.field("Dr. Chen"), "yes")
  await page.choose(page.field("AMD carrier"), "DEM03|car10002")
  await page.fill(page.field("Why are you changing this?"), "Office takes Acme now")
  await page.click("Publish")
  await waitFor(() => page.maybeButton("Publish") === undefined && page.header().includes("staff@abita.test"))

  const [publication] = page.publications
  assert.deepEqual(publication.catalog.regions[0].plans.at(-1), {
    id: "acme-health-hmo",
    label: "Acme Health HMO",
    family: "Acme",
    coverage: "medical",
    carrierCode: "DEM03",
    carrierId: "car10002",
    selfPay: false,
    aliases: [],
  })
  assert.deepEqual(publication.catalog.regions[0].tables[0].rows.at(-1), {
    planId: "acme-health-hmo",
    doctors: { "Dr. Rivera": "yes", "Dr. Chen": "yes" },
    requires: [],
    onlyOffices: [],
    notice: "",
    note: "",
  })
  assert.ok(page.groups()[0].rows.includes("Acme Health HMODr. Rivera, Dr. ChenDEM03"))
  await page.unmount()
})

test("an existing plan of the region can be put on the office's table", async (t) => {
  const page = harness(t)
  await page.render(insuranceList())
  await waitFor(() => page.groups().length === 2)
  await page.click("Add plan")
  await page.choose(page.field("Plan"), "example-select")
  await page.click("Add")
  await page.choose(page.field("Dr. Chen"), "yes")
  await page.fill(page.field("Why are you changing this?"), "Dr. Chen takes Example Select")
  await page.click("Publish")
  await waitFor(() => page.publications.length === 1)
  const publication = page.publications[0]
  assert.equal(publication.catalog.regions[0].plans.length, 5)
  assert.deepEqual(publication.catalog.regions[0].tables[0].rows.at(-1)?.planId, "example-select")
  await page.unmount()
})

test("changing a doctor from yes to no and editing the row's rules publishes and shows the new values", async (t) => {
  const page = harness(t)
  await page.render(insuranceList())
  await waitFor(() => page.groups().length === 2)
  await page.rowAction("Example Choice PPO", "Edit")
  await page.choose(page.field("Dr. Chen"), "no")
  await page.check("Prior auth", false)
  await page.check("PCP referral", true)
  await page.fill(page.field("PCP referral channel"), "availity")
  await page.check("Staff verifies", true)
  await page.check("Sweetwater", true)
  await page.fill(page.field("Callers hear"), "Bring a referral")
  await page.fill(page.field("Staff note"), "Referral from the PCP")
  await page.fill(page.field("Label"), "Example Choice PPO Plus")
  await page.fill(page.field("Family"), "Example Health")
  await page.fill(page.field("Callers also say (one per line)"), "Example Choice\n\nChoice Plus ")
  await page.fill(page.field("Why are you changing this?"), "Dr. Chen left the network")
  await page.click("Publish")
  await waitFor(() => page.maybeButton("Publish") === undefined && page.header().includes("staff@abita.test"))

  const [publication] = page.publications
  assert.deepEqual(publication.catalog.regions[0].tables[0].rows[0], {
    planId: "example-choice-medical",
    doctors: { "Dr. Chen": "no", "Dr. Rivera": "yes" },
    requires: [{ kind: "pcp_referral", channel: "availity" }, { kind: "staff_verify", channel: "" }],
    onlyOffices: ["sweetwater"],
    notice: "Bring a referral",
    note: "Referral from the PCP",
  })
  assert.deepEqual(publication.catalog.regions[0].plans[0], {
    ...published.regions[0].plans[0],
    label: "Example Choice PPO Plus",
    family: "Example Health",
    aliases: ["Example Choice", "Choice Plus"],
  })
  await page.open("Example Choice PPO Plus")
  assert.ok(page.details("Example Choice PPO Plus").includes("DoctorsDr. Rivera: Yes\nDr. Chen: No"))
  assert.ok(page.details("Example Choice PPO Plus").includes("RequiresPCP referral (availity), Staff verifies"))
  await page.unmount()
})

test("the carrier is a dropdown of the catalog's code and ID pairs labeled with carrier names, with no field to type an ID", async (t) => {
  const page = harness(t)
  await page.render(insuranceList())
  await waitFor(() => page.groups().length === 2)
  await page.rowAction("Example Choice PPO", "Edit")
  const carrier = page.field("AMD carrier")
  assert.equal(carrier.tagName, "SELECT")
  assert.equal(carrier.value, "EXC01|car10001")
  assert.deepEqual(page.options("AMD carrier"), [
    "No AMD carrier",
    "DEMO MEDICAID · DEM03 · car10002",
    "EXAMPLE CHOICE HEALTH · EXC01 · car10001",
    "EXAMPLE CHOICE HEALTH · EXC02 · car10001",
    "ZETA VISION · ZET01 · car10003",
  ])
  const details = page.host.querySelector("[aria-label='Plan details']")!
  assert.deepEqual(
    Array.from(details.querySelectorAll("input, textarea, select"), (field) => `${field.tagName}:${(field as HTMLInputElement).labels?.[0]?.textContent ?? ""}`),
    ["INPUT:Label", "INPUT:Family", "TEXTAREA:Callers also say (one per line)", "SELECT:AMD carrier", "INPUT:Self-pay"],
  )
  await page.choose(carrier, "|")
  await page.fill(page.field("Why are you changing this?"), "Billed without a carrier")
  await page.click("Publish")
  await waitFor(() => page.publications.length === 1)
  assert.deepEqual(
    [page.publications[0].catalog.regions[0].plans[0].carrierCode, page.publications[0].catalog.regions[0].plans[0].carrierId],
    ["", ""],
  )
  await page.unmount()
})

test("before publishing, a shared plan change names the other offices it also changes, using the Portal's office names", async (t) => {
  const page = harness(t)
  await page.render(insuranceList(locations[1].id))
  await waitFor(() => page.groups().length === 2)
  await page.rowAction("Example Choice PPO", "Edit")
  assert.match(page.text(), /The label, family, aliases, carrier and self-pay are shared with Hollywood and Sweetwater\./)
  assert.equal(page.status(), "")
  await page.fill(page.field("Label"), "Example Choice Plus")
  assert.equal(page.status(), "Also changes Hollywood and Sweetwater.")
  await page.click("Discard changes")

  await page.render(insuranceList(locations[2].id), "spring hill")
  await waitFor(() => page.groups().length === 1)
  await page.rowAction("Spring Plan HMO", "Edit")
  assert.match(page.text(), /shared with Crystal River\./)
  await page.choose(page.field("Dr. Licht"), "no")
  assert.match(page.text(), /1 unpublished change/)
  assert.equal(page.status(), "")
  await page.fill(page.field("Family"), "Spring Health")
  assert.equal(page.status(), "Also changes Crystal River.")
  await page.unmount()
})

test("a rejected publish shows each validation error beside the plan it names, lists the rest, and changes nothing", async (t) => {
  const page = harness(t)
  await page.render(insuranceList())
  await waitFor(() => page.groups().length === 2)
  await page.rowAction("Example Choice PPO", "Edit")
  await page.fill(page.field("Family"), "")
  await page.fill(page.field("Why are you changing this?"), "Clean up the family")
  page.publishStatus = 400
  page.publishErrors = [
    { region: "south_florida", table: "", planId: "example-choice-medical", rule: "empty_family", message: "Plan example-choice-medical needs a family." },
    { region: "south_florida", table: "doctors", planId: "demo-medicaid", rule: "note_rule_not_encoded", message: "Demo Medicaid's note names a rule that isn't encoded." },
    { region: "south_florida", table: "doctors", planId: "", rule: "unknown_doctor_column", message: "Table doctors names an unknown doctor." },
    { region: "spring_hill_crystal_river", table: "spring_hill", planId: "spring-plan", rule: "unknown_carrier", message: "Spring Plan HMO uses an unknown carrier." },
  ]
  await page.click("Publish")
  await waitFor(() => page.maybeButton("Publishing…") === undefined && page.publications.length === 1)

  assert.deepEqual(page.rowAlerts("Example Choice PPO"), ["Plan example-choice-medical needs a family."])
  assert.deepEqual(page.rowAlerts("Demo Medicaid MMA"), ["Demo Medicaid's note names a rule that isn't encoded."])
  assert.equal(page.trigger("Demo Medicaid MMA").getAttribute("aria-expanded"), "true")
  assert.match(page.text(), /Table doctors names an unknown doctor\./)
  assert.match(page.text(), /Not published\. Fix the problems shown beside each plan, then publish again\.Spring Plan HMO uses an unknown carrier\./)
  assert.equal(page.field("Family").value, "")
  assert.equal(page.active.revision.id, imported.id)
  assert.equal(page.requests.length, 1)
  assert.match(page.header(), /by operator@abita\.test$/)
  await page.unmount()
})

test("a conflict says someone else changed the list, loads their version, and keeps the unsaved edits on top of it", async (t) => {
  const page = harness(t)
  await page.render(insuranceList())
  await waitFor(() => page.groups().length === 2)
  await page.rowAction("Example Choice PPO", "Edit")
  await page.choose(page.field("Dr. Chen"), "no")
  await page.fill(page.field("Why are you changing this?"), "Dr. Chen left the network")
  const theirs = structuredClone(published)
  theirs.regions[0].tables[0].rows[2].note = "Their synthetic note"
  page.conflictWith = { revision: { ...imported, id: "00000000-0000-4000-8000-0000000000c2", createdBy: "other@abita.test", provenance: "portal" }, catalog: theirs }
  await page.click("Publish")
  await waitFor(() => /Someone else changed the insurance list\./.test(page.text()))

  assert.match(page.text(), /Your changes are kept on top of the current version\. Check them, then publish again\./)
  assert.equal(page.field("Dr. Chen").value, "no")
  assert.equal(page.field("Why are you changing this?").value, "Dr. Chen left the network")
  await waitFor(() => page.header().includes("other@abita.test"))
  await page.open("Zeta Vision Plan")
  assert.ok(page.details("Zeta Vision Plan").includes("Staff noteTheir synthetic note"))

  await page.click("Publish")
  await waitFor(() => page.publications.length === 2 && page.maybeButton("Publish") === undefined)
  const [first, second] = page.publications
  assert.equal(second.expectedRevisionId, "00000000-0000-4000-8000-0000000000c2")
  assert.notEqual(second.revisionId, first.revisionId)
  assert.equal(second.catalog.regions[0].tables[0].rows[0].doctors["Dr. Chen"], "no")
  assert.equal(second.catalog.regions[0].tables[0].rows[2].note, "Their synthetic note")
  await page.unmount()
})

test("when the change can't be checked nothing is published, and trying again reuses the same revision ID", async (t) => {
  const page = harness(t)
  await page.render(insuranceList())
  await waitFor(() => page.groups().length === 2)
  await page.rowAction("Self-pay", "Remove")
  await page.fill(page.field("Why are you changing this?"), "No self-pay row")
  page.publishStatus = 503
  await page.click("Publish")
  await waitFor(() => /The change couldn't be checked, so nothing was published\. Try again\./.test(page.text()))
  assert.equal(page.active.revision.id, imported.id)
  page.publishStatus = 200
  await page.click("Publish")
  await waitFor(() => page.maybeButton("Publish") === undefined)
  assert.equal(page.publications.length, 2)
  assert.equal(page.publications[0].revisionId, page.publications[1].revisionId)
  await page.unmount()
})

test("unpublished insurance changes survive leaving the page and returning, and Discard puts the published list back", async (t) => {
  const page = harness(t)
  await page.render(insuranceList())
  await waitFor(() => page.groups().length === 2)
  await page.rowAction("Zeta Vision Plan", "Remove")
  assert.equal(page.unload(), true)
  await page.render(<ManageAgent actorSubject="staff-a" practiceID="practice-1" locationScopeID={locations[0].id} locations={locations} page="transcripts" onPage={() => {}} />, "calls")
  await page.render(insuranceList(), "back")
  await waitFor(() => page.groups().length === 1)
  assert.match(page.text(), /1 unpublished change/)
  await page.click("Discard changes")
  await waitFor(() => page.groups().length === 2)
  assert.equal(page.unload(), false)
  await page.unmount()
})

test("before any list is published, the page says so", async (t) => {
  const page = harness(t)
  page.queryStatus = 503
  await page.render(insuranceList())
  await waitFor(() => page.alert() !== "")
  assert.equal(page.alert(), "No insurance list has been published for this practice yet.")
  await page.unmount()
})

function insuranceList(locationScopeID = locations[0].id) {
  return <ManageAgent actorSubject="staff-a" practiceID="practice-1" locationScopeID={locationScopeID} locations={locations} page="insurance" onPage={() => {}} />
}

function title(row: string) {
  return row.replace(/(Dr\.|No doctors).*$/, "")
}

function harness(t: TestContext) {
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
  const setValue = (field: HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement, value: string) => {
    const prototype =
      field instanceof window.HTMLTextAreaElement
        ? window.HTMLTextAreaElement.prototype
        : field instanceof window.HTMLSelectElement
          ? window.HTMLSelectElement.prototype
          : window.HTMLInputElement.prototype
    Object.getOwnPropertyDescriptor(prototype, "value")!.set!.call(field, value)
  }
  const page = {
    host,
    canEdit: true,
    queryStatus: 200,
    active: { revision: imported, catalog: published } as Pick<LocationInsuranceCatalog, "revision" | "catalog">,
    conflictWith: undefined as Pick<LocationInsuranceCatalog, "revision" | "catalog"> | undefined,
    requests: [] as InsuranceCatalogQuery[],
    publications: [] as InsurancePublication[],
    publishStatus: 200,
    publishErrors: [] as InsuranceValidationError[],
    render: (element: ReactNode, key = "first") =>
      act(async () => root.render(<DataProvider><SidebarProvider key={key}>{element}</SidebarProvider></DataProvider>)),
    unmount: () => act(async () => root.unmount()),
    text: () => host.textContent ?? "",
    header: () => host.querySelector("header")?.textContent ?? "",
    alert: () => host.querySelector("[role='alert']")?.textContent ?? "",
    status: () => host.querySelector("[role='status']:not([aria-label])")?.textContent ?? "",
    buttonNames: () => Array.from(host.querySelectorAll("button"), (button) => button.textContent ?? ""),
    inputs: () =>
      Array.from(host.querySelectorAll<HTMLInputElement>("input, textarea, select"))
        .filter((field) => field.getAttribute("aria-hidden") !== "true")
        .map((field) => field.getAttribute("aria-label") ?? field.labels?.[0]?.textContent ?? ""),
    button: (name: string) => {
      const button = Array.from(host.querySelectorAll("button")).find((item) => item.textContent === name)
      assert.ok(button, name)
      return button
    },
    maybeButton: (name: string) => Array.from(host.querySelectorAll("button")).find((item) => item.textContent === name),
    click: async (name: string) => {
      const button = page.button(name)
      await act(async () => button.click())
    },
    trigger: (name: string) => {
      const trigger = Array.from(host.querySelectorAll<HTMLButtonElement>("li > button")).find((item) => item.querySelector("span span")?.textContent === name)
      assert.ok(trigger, name)
      return trigger
    },
    open: async (name: string) => {
      const trigger = page.trigger(name)
      if (trigger.getAttribute("aria-expanded") === "false") await act(async () => trigger.click())
    },
    rowAction: async (name: string, action: string) => {
      await page.open(name)
      const plan = Array.from(host.querySelectorAll("li")).find((item) => item.querySelector(":scope > button span span")?.textContent === name)!
      const button = Array.from(plan.querySelectorAll("button")).find((item) => item.textContent === action)
      assert.ok(button, `${name}: ${action}`)
      await act(async () => button.click())
    },
    details: (name: string) => {
      const plan = Array.from(host.querySelectorAll("li")).find((item) => item.querySelector(":scope > button span span")?.textContent === name)!
      return Array.from(plan.querySelectorAll("dl div"), (item) => `${item.querySelector("dt")?.textContent}${item.querySelector("dd")?.textContent}`)
    },
    rowAlerts: (name: string) => {
      const plan = Array.from(host.querySelectorAll("li")).find((item) => item.querySelector(":scope > button span span")?.textContent === name)!
      return Array.from(plan.querySelectorAll("[role='alert']"), (alert) => alert.textContent ?? "")
    },
    field: (label: string) => {
      const field = Array.from(host.querySelectorAll<HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement>("input, textarea, select")).find(
        (item) => (item.labels?.[0]?.textContent ?? item.getAttribute("aria-label")) === label,
      )
      assert.ok(field, label)
      return field
    },
    options: (label: string) => Array.from((page.field(label) as HTMLSelectElement).options, (option) => option.textContent ?? ""),
    fill: (field: HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement, value: string) =>
      act(async () => {
        Object.assign(field, { attachEvent() {}, detachEvent() {} })
        field.focus()
        setValue(field, value)
        field.dispatchEvent(new window.KeyboardEvent("keyup", { bubbles: true }))
      }),
    choose: (field: HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement, value: string) =>
      act(async () => {
        setValue(field, value)
        field.dispatchEvent(new window.Event("change", { bubbles: true }))
      }),
    check: async (label: string, checked: boolean) => {
      const box = Array.from(host.querySelectorAll<HTMLInputElement>("input[type='checkbox']")).find((item) => item.labels?.[0]?.textContent === label)
      assert.ok(box, label)
      if (box.checked !== checked) await act(async () => box.click())
    },
    unload: () => {
      const event = new window.Event("beforeunload", { cancelable: true })
      window.dispatchEvent(event)
      return event.defaultPrevented
    },
    groups: () =>
      Array.from(host.querySelectorAll("[role='group'][aria-labelledby]"), (group) => ({
        title: group.querySelector("button")?.textContent ?? "",
        rows: Array.from(group.querySelectorAll(":scope li > button"), (item) => item.textContent ?? ""),
      })),
  }
  t.mock.method(globalThis, "fetch", async (input: RequestInfo | URL) => {
    if (input === "/api/auth/token") return Response.json({ token: "synthetic-token" })
    const request = input as Request
    const url = new URL(request.url)
    if (url.pathname === "/v1/insurance/revisions/query") {
      const body = (await request.json()) as InsuranceCatalogQuery
      page.requests.push(body)
      if (page.queryStatus !== 200) {
        return Response.json(
          { error: { code: "UNAVAILABLE", message: "No insurance list has been published for this practice yet.", correlationId: "synthetic", retryable: false } },
          { status: page.queryStatus },
        )
      }
      return Response.json({ ...page.active, officeId: offices[body.locationId], canEdit: page.canEdit })
    }
    assert.equal(url.pathname, "/v1/insurance/revisions")
    const publication = (await request.json()) as InsurancePublication
    page.publications.push(publication)
    if (page.conflictWith) {
      page.active = page.conflictWith
      page.conflictWith = undefined
      return Response.json(
        { error: { code: "INSURANCE_CHANGED", message: "Someone else changed the insurance list. Reload to see their changes.", correlationId: "synthetic", retryable: false } },
        { status: 409 },
      )
    }
    if (page.publishStatus === 400) {
      return Response.json(
        { error: { code: "INSURANCE_INVALID", message: "The insurance list has problems to fix before it can be published. Nothing changed.", correlationId: "synthetic", retryable: false, errors: page.publishErrors } },
        { status: 400 },
      )
    }
    if (page.publishStatus !== 200) {
      return Response.json(
        { error: { code: "UNAVAILABLE", message: "Not published. Nothing changed. Try again.", correlationId: "synthetic", retryable: true } },
        { status: page.publishStatus },
      )
    }
    const revision: InsuranceRevision = {
      id: publication.revisionId,
      createdAt: "2026-10-30T19:00:00Z",
      createdBy: "staff@abita.test",
      reason: publication.reason,
      provenance: "portal",
      previousRevisionId: publication.expectedRevisionId,
    }
    page.active = { revision, catalog: publication.catalog }
    return Response.json(revision)
  })
  return page
}

async function waitFor(ready: () => boolean) {
  for (let attempt = 0; attempt < 100 && !ready(); attempt++) {
    await act(async () => new Promise((resolve) => setTimeout(resolve, 10)))
  }
  assert.ok(ready(), "condition was not reached")
}
