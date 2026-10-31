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
  InsuranceRevisionQuery,
  InsuranceValidationError,
  Location,
  LocationInsuranceCatalog,
} from "../../lib/api/generated/types.gen.ts"

Object.defineProperty(globalThis, "document", { configurable: true, value: new JSDOM("").window.document })
const { SidebarProvider } = await import("@/components/ui/sidebar")
const { ManageAgent } = await import("./manage-agent.tsx")
const { InsuranceHistory } = await import("./insurance-history.tsx")

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

const officeNames: Record<string, string> = {
  hollywood: "Hollywood",
  sweetwater: "Sweetwater / Sweetwater Optical",
  north_miami_beach_optical: "North Miami Beach Optical",
  spring_hill: "Spring Hill",
  crystal_river: "Crystal River",
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
    { id: "car10002", code: "DEM03", name: "DEMO MEDICAID" },
    { id: "car10001", code: "EXC01", name: "EXAMPLE CHOICE HEALTH" },
    { id: "car10004", code: "EXC02", name: "EXAMPLE SELECT HEALTH" },
    { id: "car10005", code: "SELF", name: "SELF-PAY" },
    { id: "car10003", code: "ZET01", name: "ZETA VISION" },
    { id: "car10006", code: "", name: "UNCODED VISION" },
  ],
  regions: [
    {
      id: "south_florida",
      plans: [
        { id: "example-choice-medical", label: "Example Choice PPO", family: "Example", coverage: "medical", carrierId: "car10001", aliases: ["Example Choice", "Choice PPO"] },
        { id: "demo-medicaid", label: "Demo Medicaid MMA", family: "Demo", coverage: "medical", carrierId: "car10002", aliases: [] },
        { id: "zeta-vision", label: "Zeta Vision Plan", family: "Zeta", coverage: "routine_vision", carrierId: "car10003", aliases: [] },
        { id: "self-pay-medical", label: "Self-pay", family: "Self-pay", coverage: "medical", carrierId: "car10005", aliases: ["Cash"] },
        { id: "example-select", label: "Example Select HMO", family: "Example", coverage: "medical", carrierId: "car10004", aliases: ["Select"] },
      ],
      tables: [
        {
          id: "doctors",
          offices: ["hollywood", "sweetwater", "north_miami_beach_optical"],
          doctors: ["Dr. Rivera", "Dr. Chen"],
          rows: [
            { planId: "example-choice-medical", doctors: { "Dr. Chen": "yes", "Dr. Rivera": "yes" }, beforeBooking: "prior_authorization", onlyOffices: [], notice: "Synthetic caller notice" },
            { planId: "demo-medicaid", doctors: { "Dr. Chen": "pending", "Dr. Rivera": "no" }, beforeBooking: "pcp_referral", onlyOffices: ["hollywood"], notice: "" },
            { planId: "zeta-vision", doctors: { "Dr. Rivera": "yes" }, beforeBooking: "none", onlyOffices: [], notice: "" },
            { planId: "self-pay-medical", doctors: { "Dr. Rivera": "yes" }, beforeBooking: "none", onlyOffices: [], notice: "Self-pay rates apply" },
          ],
        },
      ],
    },
    {
      id: "spring_hill_crystal_river",
      plans: [
        { id: "spring-plan", label: "Spring Plan HMO", family: "Spring", coverage: "medical", carrierId: "car10001", aliases: [] },
        { id: "spring-select", label: "Spring Select PPO", family: "Spring", coverage: "medical", carrierId: "car10004", aliases: [] },
      ],
      tables: [
        {
          id: "spring_hill",
          offices: ["spring_hill"],
          doctors: ["Dr. Licht"],
          rows: [
            { planId: "spring-plan", doctors: { "Dr. Licht": "yes" }, beforeBooking: "none", onlyOffices: [], notice: "" },
            { planId: "spring-select", doctors: { "Dr. Licht": "yes" }, beforeBooking: "none", onlyOffices: [], notice: "" },
          ],
        },
        { id: "crystal_river", offices: ["crystal_river"], doctors: ["Dr. Licht"], rows: [{ planId: "spring-plan", doctors: { "Dr. Licht": "pending" }, beforeBooking: "none", onlyOffices: [], notice: "" }] },
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
  assert.match(page.header(), /^Insurance listPublished .+ by operator@abita\.testHistory$/)
  assert.deepEqual(page.groups(), [
    {
      title: "Medical3",
      rows: [
        "Demo Medicaid MMANo doctors take it · 1 pending · Only at HollywoodDEM03",
        "Example Choice PPODr. Rivera, Dr. ChenEXC01",
        "Self-payDr. RiveraSELF",
      ],
    },
    { title: "Routine vision1", rows: ["Zeta Vision PlanDr. RiveraZET01"] },
  ])
  await page.open("Example Choice PPO")
  assert.deepEqual(page.details("Example Choice PPO"), [
    "CompanyExample",
    "Visit typeMedical",
    "AMD carrierEXAMPLE CHOICE HEALTH (EXC01)",
    "Callers also sayExample Choice, Choice PPO",
    "DoctorsDr. Rivera: Yes\nDr. Chen: Yes",
    "Before bookingPrior auth",
    "Note for callersSynthetic caller notice",
  ])
  await page.open("Demo Medicaid MMA")
  assert.deepEqual(page.details("Demo Medicaid MMA"), [
    "CompanyDemo",
    "Visit typeMedical",
    "AMD carrierDEMO MEDICAID (DEM03)",
    "DoctorsDr. Rivera: No\nDr. Chen: Pending",
    "Before bookingPCP referral",
    "Only atHollywood",
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
  assert.deepEqual(publication.catalog.regions[0].plans, published.regions[0].plans.filter((plan) => plan.id !== "demo-medicaid"))
  assert.deepEqual(publication.catalog.regions[1], published.regions[1])
  assert.deepEqual(page.active.catalog, publication.catalog)
  assert.deepEqual(page.groups()[0].rows.map(title), ["Example Choice PPO", "Self-pay"])
  assert.equal(page.unload(), false)
  await page.unmount()
})

test("a new plan is added from one complete form, and Add stays disabled with the reason until the form is valid", async (t) => {
  const page = harness(t)
  await page.render(insuranceList())
  await waitFor(() => page.groups().length === 2)
  await page.click("Add plan")
  await page.fill(page.field("Find a plan"), "Acme Health HMO")
  assert.match(page.text(), /No plan in this region matches\. Create a new plan instead\./)
  await page.click("Create new plan")
  assert.equal(page.field("Plan name").value, "Acme Health HMO")
  assert.deepEqual(Array.from(page.host.querySelectorAll("datalist option"), (option) => option.getAttribute("value")), ["Demo", "Example", "Self-pay", "Zeta"])
  assert.equal(page.field("Dr. Rivera").value, "no")
  assert.equal(page.field("Before booking").value, "none")
  assert.equal(page.button("Add").disabled, true)
  assert.match(page.text(), /To add this plan, enter the company and choose the visit type\./)

  await page.fill(page.field("Company"), "Acme")
  await page.choose(page.field("Visit type"), "medical")
  assert.equal(page.button("Add").disabled, false)
  await page.choose(page.field("Dr. Rivera"), "yes")
  assert.equal(page.button("Add").disabled, true)
  assert.match(page.text(), /To add this plan, choose the AMD carrier, because a doctor takes this plan\./)
  await page.fill(page.field("Plan name"), " ")
  assert.match(page.text(), /To add this plan, enter the plan name and choose the AMD carrier, because a doctor takes this plan\./)
  await page.fill(page.field("Plan name"), "Acme Health HMO")
  await page.choose(page.field("AMD carrier"), "car10002")
  await page.choose(page.field("Dr. Chen"), "pending")
  await page.fill(page.field("Callers also say"), "Acme\n\n Acme HMO ")
  await page.choose(page.field("Before booking"), "staff_verify")
  await page.fill(page.field("Note for callers"), "Bring your Acme card.")
  assert.equal(page.button("Add").disabled, false)
  assert.doesNotMatch(page.text(), /To add this plan/)
  await page.click("Add")
  assert.equal(page.field("Plan name").value, "Acme Health HMO")
  await page.fill(page.field("Why are you changing this?"), "Office takes Acme now")
  await page.click("Publish")
  await waitFor(() => page.maybeButton("Publish") === undefined && page.header().includes("staff@abita.test"))

  const [publication] = page.publications
  assert.deepEqual(publication.catalog.regions[0].plans.at(-1), {
    id: "acme-health-hmo",
    label: "Acme Health HMO",
    family: "Acme",
    coverage: "medical",
    carrierId: "car10002",
    aliases: ["Acme", "Acme HMO"],
  })
  assert.deepEqual(publication.catalog.regions[0].tables[0].rows.at(-1), {
    planId: "acme-health-hmo",
    doctors: { "Dr. Rivera": "yes", "Dr. Chen": "pending" },
    beforeBooking: "staff_verify",
    onlyOffices: [],
    notice: "Bring your Acme card.",
  })
  assert.ok(page.groups()[0].rows.includes("Acme Health HMODr. Rivera · 1 pendingDEM03"))
  await page.unmount()
})

test("an existing plan of the region is found by what callers call it, added to the table, and opened for editing", async (t) => {
  const page = harness(t)
  await page.render(insuranceList())
  await waitFor(() => page.groups().length === 2)
  await page.click("Add plan")
  await page.fill(page.field("Find a plan"), "zeta")
  assert.match(page.text(), /No plan in this region matches/)
  await page.fill(page.field("Find a plan"), "select")
  assert.deepEqual(Array.from(page.host.querySelectorAll("[aria-label='Matching plans'] button"), (button) => button.textContent), ["Example Select HMOExample · Medical"])
  await page.click("Example Select HMOExample · Medical")
  assert.equal(page.maybeButton("Create new plan"), undefined)
  await page.choose(page.field("Dr. Chen"), "yes")
  await page.fill(page.field("Why are you changing this?"), "Dr. Chen takes Example Select")
  await page.click("Publish")
  await waitFor(() => page.publications.length === 1)
  const publication = page.publications[0]
  assert.equal(publication.catalog.regions[0].plans.length, 5)
  assert.deepEqual(publication.catalog.regions[0].tables[0].rows.at(-1), {
    planId: "example-select",
    doctors: { "Dr. Chen": "yes" },
    beforeBooking: "none",
    onlyOffices: [],
    notice: "",
  })
  await page.unmount()
})

test("editing splits the plan's shared fields from this table's answers, publishes them, and shows the new values", async (t) => {
  const page = harness(t)
  await page.render(insuranceList())
  await waitFor(() => page.groups().length === 2)
  await page.rowAction("Example Choice PPO", "Edit")
  assert.deepEqual(page.sections(), ["This plan", "At Hollywood, Sweetwater / Sweetwater Optical, and North Miami Beach Optical"])
  assert.deepEqual(page.sectionFields("This plan"), ["INPUT:Plan name", "INPUT:Company", "TEXTAREA:Callers also say", "SELECT:AMD carrier"])
  assert.match(page.section("This plan"), /Visit typeMedical/)
  assert.deepEqual(page.sectionFields("At Hollywood, Sweetwater / Sweetwater Optical, and North Miami Beach Optical"), [
    "SELECT:Dr. Rivera",
    "SELECT:Dr. Chen",
    "SELECT:Before booking",
    "INPUT:Hollywood",
    "INPUT:Sweetwater / Sweetwater Optical",
    "INPUT:North Miami Beach Optical",
    "TEXTAREA:Note for callers",
  ])
  assert.deepEqual(page.options("Dr. Chen"), ["Yes", "No", "Pending"])
  assert.deepEqual(page.options("Before booking"), ["Nothing", "PCP referral", "Prior auth", "Staff checks coverage"])
  assert.match(page.text(), /Abita says this to callers when this plan comes up\./)
  await page.choose(page.field("Dr. Chen"), "no")
  await page.choose(page.field("Before booking"), "staff_verify")
  await page.check("Sweetwater / Sweetwater Optical", true)
  await page.fill(page.field("Note for callers"), "Bring a referral")
  await page.fill(page.field("Plan name"), "Example Choice PPO Plus")
  await page.fill(page.field("Company"), "Example Health")
  await page.fill(page.field("Callers also say"), "Example Choice\n\nChoice Plus ")
  await page.fill(page.field("Why are you changing this?"), "Dr. Chen left the network")
  await page.click("Publish")
  await waitFor(() => page.maybeButton("Publish") === undefined && page.header().includes("staff@abita.test"))

  const [publication] = page.publications
  assert.deepEqual(publication.catalog.regions[0].tables[0].rows[0], {
    planId: "example-choice-medical",
    doctors: { "Dr. Chen": "no", "Dr. Rivera": "yes" },
    beforeBooking: "staff_verify",
    onlyOffices: ["sweetwater"],
    notice: "Bring a referral",
  })
  assert.deepEqual(publication.catalog.regions[0].plans[0], {
    ...published.regions[0].plans[0],
    label: "Example Choice PPO Plus",
    family: "Example Health",
    aliases: ["Example Choice", "Choice Plus"],
  })
  await page.open("Example Choice PPO Plus")
  assert.ok(page.details("Example Choice PPO Plus").includes("DoctorsDr. Rivera: Yes\nDr. Chen: No"))
  assert.ok(page.details("Example Choice PPO Plus").includes("Before bookingStaff checks coverage"))
  await page.unmount()
})

test("the AMD carrier is a dropdown of every carrier by name and code, and an unset doctor reads and edits as No", async (t) => {
  const page = harness(t)
  await page.render(insuranceList())
  await waitFor(() => page.groups().length === 2)
  await page.rowAction("Zeta Vision Plan", "Edit")
  const carrier = page.field("AMD carrier")
  assert.equal(carrier.tagName, "SELECT")
  assert.equal(carrier.value, "car10003")
  assert.deepEqual(page.options("AMD carrier"), [
    "No AMD carrier",
    "DEMO MEDICAID (DEM03)",
    "EXAMPLE CHOICE HEALTH (EXC01)",
    "EXAMPLE SELECT HEALTH (EXC02)",
    "SELF-PAY (SELF)",
    "UNCODED VISION",
    "ZETA VISION (ZET01)",
  ])
  assert.equal(page.field("Dr. Chen").value, "no")
  await page.choose(carrier, "car10006")
  await page.fill(page.field("Why are you changing this?"), "Zeta moved carriers")
  await page.click("Publish")
  await waitFor(() => page.publications.length === 1)
  assert.equal(page.publications[0].catalog.regions[0].plans[2].carrierId, "car10006")
  assert.deepEqual(page.publications[0].catalog.regions[0].tables[0].rows[2].doctors, { "Dr. Rivera": "yes" })
  await page.unmount()
})

test("a self-pay plan is a normal row that cannot be removed, whose plan fields are read-only while its doctors and note stay editable", async (t) => {
  const page = harness(t)
  await page.render(insuranceList())
  await waitFor(() => page.groups().length === 2)
  await page.open("Self-pay")
  assert.deepEqual(page.details("Self-pay"), [
    "CompanySelf-pay",
    "Visit typeMedical",
    "AMD carrierSELF-PAY (SELF)",
    "Callers also sayCash",
    "DoctorsDr. Rivera: Yes\nDr. Chen: No",
    "Before bookingNothing",
    "Note for callersSelf-pay rates apply",
  ])
  assert.equal(page.rowButton("Self-pay", "Remove"), undefined)
  await page.rowAction("Self-pay", "Edit")
  assert.deepEqual(page.sectionFields("This plan"), [])
  assert.match(page.section("This plan"), /Abita offers self-pay to callers whose insurance isn't taken here, so this plan's details can't be changed\./)
  assert.match(page.section("This plan"), /Plan nameSelf-payCompanySelf-pay/)
  assert.deepEqual(page.sectionFields("At Hollywood, Sweetwater / Sweetwater Optical, and North Miami Beach Optical"), [
    "SELECT:Dr. Rivera",
    "SELECT:Dr. Chen",
    "TEXTAREA:Note for callers",
  ])
  assert.equal(page.host.querySelectorAll("input[type='checkbox']").length, 0)
  await page.choose(page.field("Dr. Chen"), "yes")
  await page.fill(page.field("Note for callers"), "Self-pay rates apply. Ask about the exam price.")
  await page.fill(page.field("Why are you changing this?"), "Dr. Chen sees self-pay patients")
  await page.click("Publish")
  await waitFor(() => page.publications.length === 1)
  assert.deepEqual(page.publications[0].catalog.regions[0].plans[3], published.regions[0].plans[3])
  assert.deepEqual(page.publications[0].catalog.regions[0].tables[0].rows[3], {
    planId: "self-pay-medical",
    doctors: { "Dr. Rivera": "yes", "Dr. Chen": "yes" },
    beforeBooking: "none",
    onlyOffices: [],
    notice: "Self-pay rates apply. Ask about the exam price.",
  })
  await page.unmount()
})

test("before publishing, a shared plan change names the other offices it also changes by the Location names the backend joins for each office", async (t) => {
  const page = harness(t)
  await page.render(insuranceList(locations[1].id))
  await waitFor(() => page.groups().length === 2)
  await page.rowAction("Example Choice PPO", "Edit")
  assert.match(page.section("This plan"), /^This planShared with Hollywood and Sweetwater \/ Sweetwater Optical, so changes here apply there too\./)
  assert.equal(page.status(), "")
  await page.fill(page.field("Plan name"), "Example Choice Plus")
  assert.equal(page.status(), "Also changes Hollywood and Sweetwater / Sweetwater Optical.")
  await page.click("Discard changes")

  await page.render(insuranceList(locations[2].id), "spring hill")
  await waitFor(() => page.groups().length === 1)
  await page.rowAction("Spring Plan HMO", "Edit")
  assert.match(page.text(), /Shared with Crystal River, so changes here apply there too\./)
  await page.choose(page.field("Dr. Licht"), "no")
  assert.match(page.text(), /1 unpublished change/)
  assert.equal(page.status(), "")
  await page.fill(page.field("Company"), "Spring Health")
  assert.equal(page.status(), "Also changes Crystal River.")
  await page.unmount()
})

test("a plan only this office's table lists names no other office before or after an edit", async (t) => {
  const page = harness(t)
  await page.render(insuranceList(locations[2].id), "spring hill")
  await waitFor(() => page.groups().length === 1)
  await page.rowAction("Spring Select PPO", "Edit")
  assert.doesNotMatch(page.text(), /Shared with/)
  await page.fill(page.field("Plan name"), "Spring Select Plus")
  assert.match(page.text(), /1 unpublished change/)
  assert.equal(page.status(), "")
  await page.unmount()
})

test("a rejected publish shows each validation error beside the plan it names, lists the rest, and changes nothing", async (t) => {
  const page = harness(t)
  await page.render(insuranceList())
  await waitFor(() => page.groups().length === 2)
  await page.rowAction("Example Choice PPO", "Edit")
  await page.fill(page.field("Company"), "")
  await page.fill(page.field("Why are you changing this?"), "Clean up the company")
  page.publishStatus = 400
  page.publishErrors = [
    { region: "south_florida", table: "", planId: "example-choice-medical", rule: "empty_family", message: "Plan example-choice-medical needs a family." },
    { region: "south_florida", table: "doctors", planId: "demo-medicaid", rule: "accepted_without_carrier", message: "Demo Medicaid MMA needs an AMD carrier." },
    { region: "south_florida", table: "doctors", planId: "", rule: "unknown_doctor_column", message: "Table doctors names an unknown doctor." },
    { region: "spring_hill_crystal_river", table: "spring_hill", planId: "spring-plan", rule: "unknown_carrier", message: "Spring Plan HMO uses an unknown carrier." },
  ]
  await page.click("Publish")
  await waitFor(() => page.maybeButton("Publishing…") === undefined && page.publications.length === 1)

  assert.deepEqual(page.rowAlerts("Example Choice PPO"), ["Plan example-choice-medical needs a family."])
  assert.deepEqual(page.rowAlerts("Demo Medicaid MMA"), ["Demo Medicaid MMA needs an AMD carrier."])
  assert.equal(page.trigger("Demo Medicaid MMA").getAttribute("aria-expanded"), "true")
  assert.match(page.text(), /Table doctors names an unknown doctor\./)
  assert.match(page.text(), /Not published\. Fix the problems shown beside each plan, then publish again\.Spring Plan HMO uses an unknown carrier\./)
  assert.equal(page.field("Company").value, "")
  assert.equal(page.active.revision.id, imported.id)
  assert.equal(page.requests.length, 1)
  assert.match(page.header(), /by operator@abita\.testHistory$/)
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
  theirs.regions[0].tables[0].rows[2].notice = "Their synthetic notice"
  page.conflictWith = { revision: { ...imported, id: "00000000-0000-4000-8000-0000000000c2", createdBy: "other@abita.test", provenance: "portal" }, catalog: theirs }
  await page.click("Publish")
  await waitFor(() => /Someone else changed the insurance list\./.test(page.text()))

  assert.match(page.text(), /Your changes are kept on top of the current version\. Check them, then publish again\./)
  assert.equal(page.field("Dr. Chen").value, "no")
  assert.equal(page.field("Why are you changing this?").value, "Dr. Chen left the network")
  await waitFor(() => page.header().includes("other@abita.test"))
  await page.open("Zeta Vision Plan")
  assert.ok(page.details("Zeta Vision Plan").includes("Note for callersTheir synthetic notice"))

  await page.click("Publish")
  await waitFor(() => page.publications.length === 2 && page.maybeButton("Publish") === undefined)
  const [first, second] = page.publications
  assert.equal(second.expectedRevisionId, "00000000-0000-4000-8000-0000000000c2")
  assert.notEqual(second.revisionId, first.revisionId)
  assert.equal(second.catalog.regions[0].tables[0].rows[0].doctors["Dr. Chen"], "no")
  assert.equal(second.catalog.regions[0].tables[0].rows[2].notice, "Their synthetic notice")
  await page.unmount()
})

test("when the change can't be checked nothing is published, and trying again reuses the same revision ID", async (t) => {
  const page = harness(t)
  await page.render(insuranceList())
  await waitFor(() => page.groups().length === 2)
  await page.rowAction("Zeta Vision Plan", "Remove")
  await page.fill(page.field("Why are you changing this?"), "Zeta Vision left the network")
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

const renamed: InsuranceRevision = {
  id: "00000000-0000-4000-8000-0000000000c2",
  createdAt: "2026-10-30T16:00:00Z",
  createdBy: "staff@abita.test",
  reason: "Rename Example Choice",
  provenance: "portal",
  previousRevisionId: imported.id,
}
const dropped: InsuranceRevision = {
  id: "00000000-0000-4000-8000-0000000000c3",
  createdAt: "2026-10-30T17:00:00Z",
  createdBy: "north@abita.test",
  reason: "Drop Demo Medicaid",
  provenance: "portal",
  previousRevisionId: renamed.id,
}

function withHistory(page: ReturnType<typeof harness>) {
  const second = structuredClone(published)
  second.regions[0].plans[0].label = "Example Choice Plus"
  second.regions[1].plans[0].family = "Spring Health"
  const third = structuredClone(second)
  third.regions[0].tables[0].rows = third.regions[0].tables[0].rows.filter((row) => row.planId !== "demo-medicaid")
  page.history = [dropped, renamed, imported]
  page.catalogs = { [imported.id]: published, [renamed.id]: second, [dropped.id]: third }
  page.active = { revision: dropped, catalog: third }
}

function insuranceHistory(page: ReturnType<typeof harness>, onChanged = () => {}, hasDraft = false) {
  const active: LocationInsuranceCatalog = { ...page.active, officeId: "hollywood", officeNames, canEdit: page.canEdit }
  return <InsuranceHistory practiceID="practice-1" locationID={locations[0].id} active={active} hasDraft={hasDraft} onChanged={onChanged} />
}

test("History lists every revision newest first with its provenance, author, time and reason, and marks the active one", async (t) => {
  const page = harness(t)
  withHistory(page)
  await page.render(insuranceList())
  await waitFor(() => page.groups().length === 2)
  assert.ok(page.button("History"))

  await page.render(insuranceHistory(page), "history")
  await waitFor(() => page.historyRows().length === 3)
  assert.deepEqual(page.historyRequests, [{ practiceId: "practice-1", locationId: locations[0].id }])
  const rows = page.historyRows()
  assert.match(rows[0], /^Portal edit00000000Active.*north@abita\.test · .+Drop Demo Medicaid$/)
  assert.match(rows[1], /^Portal edit00000000staff@abita\.test · .+Rename Example Choice$/)
  assert.match(rows[2], /^Imported00000000operator@abita\.test · .+initial import$/)
  assert.deepEqual(Array.from(page.host.querySelectorAll("li > button time"), (time) => time.getAttribute("dateTime")), [dropped.createdAt, renamed.createdAt, imported.createdAt])
  await page.selectRevision("Portal edit00000000Active")
  await waitFor(() => /This is the active version\./.test(page.text()))
  assert.equal(page.maybeButton("Restore this version"), undefined)
  await page.unmount()
})

test("a selected revision names the plans and office rows restoring it would add, remove and change, and counts changes elsewhere", async (t) => {
  const page = harness(t)
  withHistory(page)
  await page.render(insuranceHistory(page))
  await waitFor(() => page.historyRows().length === 3)
  await page.selectRevision("Imported")
  await waitFor(() => /Restoring this version would/.test(page.text()))
  assert.deepEqual(page.readRequests, [{ practiceId: "practice-1", locationId: locations[0].id, revisionId: imported.id }])
  assert.match(page.text(), /make these changes to the active list for Hollywood\./)
  assert.deepEqual(page.diff("Plans"), ["Changed | Example Choice PPO | plan name"])
  assert.deepEqual(page.diff("This office's rows"), ["Added | Demo Medicaid MMA"])
  assert.match(page.text(), /1 more change elsewhere in the list\./)
  assert.ok(page.button("Restore this version"))

  await page.click("Back to history")
  await page.selectRevision("Portal edit00000000staff")
  await waitFor(() => page.diff("This office's rows").length === 1)
  assert.deepEqual(page.diff("Plans"), [])
  assert.doesNotMatch(page.text(), /elsewhere/)
  await page.unmount()
})

test("restoring publishes the earlier catalog as a restore with the reason Restore and its short ID, then reloads history and the list", async (t) => {
  const page = harness(t)
  withHistory(page)
  let changed = 0
  await page.render(insuranceHistory(page, () => changed++))
  await waitFor(() => page.historyRows().length === 3)
  await page.selectRevision("Imported")
  await waitFor(() => page.maybeButton("Restore this version") !== undefined)
  await page.click("Restore this version")
  assert.match(page.text(), /with the reason “Restore 00000000”/)
  await page.click("Restore")
  await waitFor(() => page.historyRows().length === 4)

  const [publication] = page.publications
  assert.match(publication.revisionId, /^[0-9a-f-]{36}$/)
  assert.deepEqual({ ...publication, revisionId: "" }, {
    practiceId: "practice-1",
    locationId: locations[0].id,
    revisionId: "",
    expectedRevisionId: dropped.id,
    reason: "Restore 00000000",
    provenance: "restore",
    catalog: published,
  })
  assert.equal(changed, 1)
  assert.match(page.historyRows()[0], /^Restore.*staff@abita\.test.*Restore 00000000$/)
  await page.unmount()
})

test("a restore the middleware rejects shows every error and changes nothing", async (t) => {
  const page = harness(t)
  withHistory(page)
  let changed = 0
  await page.render(insuranceHistory(page, () => changed++))
  await waitFor(() => page.historyRows().length === 3)
  await page.selectRevision("Imported")
  await waitFor(() => page.maybeButton("Restore this version") !== undefined)
  await page.click("Restore this version")
  page.publishStatus = 400
  page.publishErrors = [
    { region: "south_florida", table: "doctors", planId: "demo-medicaid", rule: "unknown_doctor_column", message: "Dr. Moreau is not a doctor column on the doctors table." },
    { region: "", table: "", planId: "", rule: "unknown_carrier", message: "Carrier car10009 is not in the carrier list." },
  ]
  await page.click("Restore")
  await waitFor(() => page.alert() !== "")
  assert.match(page.alert(), /^Not restored\. This version no longer passes the insurance list checks\. Nothing changed\./)
  assert.deepEqual(Array.from(page.host.querySelectorAll("[role='alert'] li"), (item) => item.textContent), [
    "Dr. Moreau is not a doctor column on the doctors table.",
    "Carrier car10009 is not in the carrier list.",
  ])
  assert.equal(page.publications.length, 1)
  assert.equal(page.active.revision.id, dropped.id)
  assert.equal(page.historyRequests.length, 1)
  assert.equal(changed, 0)
  await page.unmount()
})

test("a restore the member is not authorized for shows the 403 message and changes nothing", async (t) => {
  const page = harness(t)
  withHistory(page)
  await page.render(insuranceHistory(page))
  await waitFor(() => page.historyRows().length === 3)
  await page.selectRevision("Imported")
  await waitFor(() => page.maybeButton("Restore this version") !== undefined)
  await page.click("Restore this version")
  page.publishStatus = 403
  await page.click("Restore")
  await waitFor(() => page.alert() !== "")
  assert.equal(page.alert(), "You don't have access to every office this change affects. Nothing changed.")
  assert.equal(page.active.revision.id, dropped.id)
  await page.unmount()
})

test("members who cannot edit, or who have unpublished changes, see the diff without a Restore button", async (t) => {
  const page = harness(t)
  withHistory(page)
  page.canEdit = false
  await page.render(insuranceHistory(page))
  await waitFor(() => page.historyRows().length === 3)
  await page.selectRevision("Imported")
  await waitFor(() => page.diff("Plans").length === 1)
  assert.equal(page.maybeButton("Restore this version"), undefined)

  page.canEdit = true
  await page.render(insuranceHistory(page, () => {}, true), "with draft")
  await waitFor(() => page.historyRows().length === 3)
  await page.selectRevision("Imported")
  await waitFor(() => page.diff("Plans").length === 1)
  assert.equal(page.maybeButton("Restore this version"), undefined)
  assert.match(page.text(), /Publish or discard your unpublished changes before restoring\./)
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
    history: [imported] as InsuranceRevision[],
    catalogs: { [imported.id]: published } as Record<string, InsuranceCatalog>,
    historyRequests: [] as InsuranceCatalogQuery[],
    readRequests: [] as InsuranceRevisionQuery[],
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
    rowButton: (name: string, action: string) => {
      const plan = Array.from(host.querySelectorAll("li")).find((item) => item.querySelector(":scope > button span span")?.textContent === name)!
      return Array.from(plan.querySelectorAll("button")).find((item) => item.textContent === action)
    },
    rowAction: async (name: string, action: string) => {
      await page.open(name)
      const button = page.rowButton(name, action)
      assert.ok(button, `${name}: ${action}`)
      await act(async () => button.click())
    },
    sections: () => Array.from(host.querySelectorAll("section[aria-labelledby] > div > h3"), (heading) => heading.textContent ?? ""),
    sectionElement: (title: string) => {
      const section = Array.from(host.querySelectorAll("section[aria-labelledby]")).find((item) => item.querySelector(":scope > div > h3")?.textContent === title)
      assert.ok(section, title)
      return section
    },
    section: (title: string) => page.sectionElement(title).textContent ?? "",
    sectionFields: (title: string) =>
      Array.from(
        page.sectionElement(title).querySelectorAll<HTMLInputElement>("input, textarea, select"),
        (field) => `${field.tagName}:${field.labels?.[0]?.textContent ?? ""}`,
      ),
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
    historyRows: () => Array.from(host.querySelectorAll("li > button"), (row) => row.textContent ?? ""),
    selectRevision: async (prefix: string) => {
      const row = Array.from(host.querySelectorAll<HTMLButtonElement>("li > button")).find((item) => item.textContent?.startsWith(prefix))
      assert.ok(row, prefix)
      await act(async () => row.click())
    },
    diff: (title: string) =>
      Array.from(host.querySelectorAll(`section[aria-label="${title}"] li`), (item) =>
        Array.from(item.children, (child) => child.textContent ?? "").join(" | "),
      ),
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
      return Response.json({ ...page.active, officeId: offices[body.locationId], officeNames, canEdit: page.canEdit })
    }
    if (url.pathname === "/v1/insurance/revisions/history") {
      page.historyRequests.push((await request.json()) as InsuranceCatalogQuery)
      return Response.json({ revisions: page.history })
    }
    if (url.pathname === "/v1/insurance/revisions/read") {
      const body = (await request.json()) as InsuranceRevisionQuery
      page.readRequests.push(body)
      const revision = page.history.find((item) => item.id === body.revisionId)
      return Response.json({ revision, catalog: page.catalogs[body.revisionId] })
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
    if (page.publishStatus === 403) {
      return Response.json(
        { error: { code: "ACCESS_DENIED", message: "You don't have access to every office this change affects. Nothing changed.", correlationId: "synthetic", retryable: false } },
        { status: 403 },
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
      provenance: publication.provenance ?? "portal",
      previousRevisionId: publication.expectedRevisionId,
    }
    page.active = { revision, catalog: publication.catalog }
    page.history = [revision, ...page.history]
    page.catalogs[revision.id] = publication.catalog
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
