import assert from "node:assert/strict"
import test from "node:test"

import type { InsuranceCatalog } from "./api/generated/types.gen.ts"
import { carrierChoices, changedPlans, diffCatalogs, isSelfPay, newPlanID, record, replay, same, touchedOffices, type InsuranceEdit, type NewPlan } from "./insurance-catalog.ts"

const catalog: InsuranceCatalog = {
  carriers: [
    { id: "car2", code: "BET01", name: "BETA" },
    { id: "car1", code: "ALP01", name: "ALPHA" },
    { id: "car3", code: "", name: "GAMMA" },
  ],
  regions: [
    {
      id: "south",
      plans: [
        { id: "alpha", label: "Alpha PPO", family: "Alpha", coverage: "medical", carrierId: "car1", aliases: [] },
        { id: "beta", label: "Beta HMO", family: "Beta", coverage: "medical", carrierId: "car2", aliases: [] },
        { id: "self-pay-medical", label: "Self pay", family: "Self-pay", coverage: "medical", carrierId: "car3", aliases: [] },
      ],
      tables: [
        {
          id: "doctors",
          offices: ["hollywood", "sweetwater"],
          doctors: ["Dr. A"],
          rows: [{ planId: "alpha", doctors: { "Dr. A": "yes" }, beforeBooking: "none", onlyOffices: [], notice: "" }],
        },
        { id: "optical", offices: ["optical"], doctors: ["Dr. B"], rows: [] },
      ],
    },
    {
      id: "north",
      plans: [{ id: "alpha", label: "Alpha PPO", family: "Alpha", coverage: "medical", carrierId: "car1", aliases: [] }],
      tables: [{ id: "spring", offices: ["spring"], doctors: ["Dr. C"], rows: [] }],
    },
  ],
}

const at = { region: "south", table: "doctors", plan: "alpha" }
const gamma: NewPlan = { label: "Gamma", family: "Gamma", coverage: "routine_vision", carrierId: "car3", aliases: ["Gamma Vision"], doctors: {}, beforeBooking: "none", notice: "" }

test("a change touches the offices whose tables list the plan, every office on a changed table, and every office for a carrier", () => {
  const listedTwice = replay(catalog, [{ kind: "addRow", at: { ...at, table: "optical" } }])
  const opticalOnly = { region: "south", table: "optical", plan: "gamma" }
  const cases: [string, InsuranceCatalog, InsuranceCatalog, string[]][] = [
    ["no change", catalog, catalog, []],
    ["a doctor answer on the shared table", catalog, replay(catalog, [{ kind: "setDoctor", at, doctor: "Dr. A", answer: "no" }]), ["hollywood", "sweetwater"]],
    ["renaming a plan only the doctors table lists", catalog, replay(catalog, [{ kind: "changePlan", at, change: { label: "Alpha Choice" } }]), ["hollywood", "sweetwater"]],
    ["removing a plan only the doctors table lists", catalog, replay(catalog, [{ kind: "removeRow", at }]), ["hollywood", "sweetwater"]],
    ["adding a plan to the optical table", catalog, replay(catalog, [{ kind: "createPlan", at: opticalOnly, plan: gamma }]), ["optical"]],
    ["renaming a plan both tables list", listedTwice, replay(listedTwice, [{ kind: "changePlan", at, change: { label: "Alpha Choice" } }]), ["hollywood", "optical", "sweetwater"]],
    ["moving a plan both tables list to another carrier", listedTwice, replay(listedTwice, [{ kind: "setCarrier", at, carrierId: "car2" }]), ["hollywood", "optical", "sweetwater"]],
    ["changing a carrier's code", catalog, { ...catalog, carriers: catalog.carriers.map((carrier) => (carrier.id === "car2" ? { ...carrier, code: "BET02" } : carrier)) }, ["hollywood", "optical", "spring", "sweetwater"]],
  ]
  for (const [name, before, after, offices] of cases) {
    assert.deepEqual(touchedOffices(before, after), offices, name)
    assert.deepEqual(touchedOffices(after, before), offices, `reverting ${name}`)
  }
})

test("undoing an edit by hand leaves nothing to publish", () => {
  const edits: InsuranceEdit[] = [
    { kind: "setDoctor", at, doctor: "Dr. A", answer: "pending" },
    { kind: "setBeforeBooking", at, beforeBooking: "prior_authorization" },
    { kind: "setBeforeBooking", at, beforeBooking: "none" },
    { kind: "setDoctor", at, doctor: "Dr. A", answer: "yes" },
  ]
  assert.ok(same(replay(catalog, edits), catalog))
  assert.equal(changedPlans(catalog, replay(catalog, edits.slice(0, 2))), 1)
})

test("removing a plan created in the draft forgets it instead of leaving an unused plan behind", () => {
  const created: PlanAtEdit = { kind: "createPlan", at: { ...at, plan: "gamma" }, plan: gamma }
  let edits = record([], created)
  edits = record(edits, { kind: "setDoctor", at: created.at, doctor: "Dr. A", answer: "yes" })
  assert.equal(replay(catalog, edits).regions[0].plans.length, 4)
  edits = record(edits, { kind: "removeRow", at: created.at })
  assert.deepEqual(edits, [])
  const removed = replay(catalog, record([], { kind: "removeRow", at }))
  assert.deepEqual(removed.regions[0].tables[0].rows, [])
  assert.deepEqual(planIDs(removed, "south"), ["beta", "self-pay-medical"])
})

const optical = { ...at, table: "optical" }
const listedTwice = replay(catalog, [{ kind: "addRow", at: optical }])

function planIDs(revised: InsuranceCatalog, region: string) {
  return revised.regions.find((item) => item.id === region)?.plans.map((plan) => plan.id)
}

test("removing the last row that lists a plan removes the plan from its region, and removing one of two rows keeps it", () => {
  const oneLeft = replay(listedTwice, [{ kind: "removeRow", at }])
  assert.deepEqual(planIDs(oneLeft, "south"), ["alpha", "beta", "self-pay-medical"])
  assert.deepEqual(oneLeft.regions[0].tables[1].rows.map((row) => row.planId), ["alpha"])

  const noneLeft = replay(listedTwice, [{ kind: "removeRow", at }, { kind: "removeRow", at: optical }])
  assert.deepEqual(planIDs(noneLeft, "south"), ["beta", "self-pay-medical"])
  assert.deepEqual(planIDs(noneLeft, "north"), ["alpha"])
  assert.deepEqual(touchedOffices(listedTwice, noneLeft), ["hollywood", "optical", "sweetwater"])
  assert.equal(changedPlans(listedTwice, noneLeft), 2)
})

test("a remove replayed onto a newer list keeps the plan only while another table still lists it", () => {
  const mine = record([], { kind: "removeRow", at })
  const theirsRemovedOptical = replay(listedTwice, [{ kind: "removeRow", at: optical }])
  assert.deepEqual(planIDs(theirsRemovedOptical, "south"), ["alpha", "beta", "self-pay-medical"])
  assert.deepEqual(planIDs(replay(theirsRemovedOptical, mine), "south"), ["beta", "self-pay-medical"])

  const theirsAddedOptical = listedTwice
  const replayed = replay(theirsAddedOptical, mine)
  assert.deepEqual(planIDs(replayed, "south"), ["alpha", "beta", "self-pay-medical"])
  assert.deepEqual(replayed.regions[0].tables[1].rows.map((row) => row.planId), ["alpha"])
})

test("carrier choices are every carrier, named with its AMD code when it has one, after no carrier", () => {
  assert.deepEqual(carrierChoices(catalog), [
    { id: "", label: "No AMD carrier" },
    { id: "car1", label: "ALPHA (ALP01)" },
    { id: "car2", label: "BETA (BET01)" },
    { id: "car3", label: "GAMMA" },
  ])
})

test("a creating edit adds the whole new plan and its row, and new plan IDs never take a reserved self-pay ID", () => {
  const revised = replay(catalog, [{ kind: "createPlan", at: { ...at, plan: "gamma" }, plan: { ...gamma, doctors: { "Dr. A": "yes" }, beforeBooking: "pcp_referral", notice: "Bring your card." } }])
  assert.deepEqual(revised.regions[0].plans.at(-1), { id: "gamma", label: "Gamma", family: "Gamma", coverage: "routine_vision", carrierId: "car3", aliases: ["Gamma Vision"] })
  assert.deepEqual(revised.regions[0].tables[0].rows.at(-1), { planId: "gamma", doctors: { "Dr. A": "yes" }, beforeBooking: "pcp_referral", onlyOffices: [], notice: "Bring your card." })
  assert.equal(newPlanID(catalog.regions[1], "Self Pay Medical"), "self-pay-medical-2")
  assert.equal(newPlanID(catalog.regions[0], "Alpha"), "alpha-2")
})

test("a plan is self-pay only under the reserved ID for its own coverage", () => {
  const plan = catalog.regions[0].plans[2]
  assert.equal(isSelfPay(plan), true)
  assert.equal(isSelfPay({ ...plan, coverage: "routine_vision" }), false)
  assert.equal(isSelfPay({ ...plan, id: "self-pay" }), false)
})

type PlanAtEdit = Extract<InsuranceEdit, { kind: "createPlan" }>

test("the diff names the plans and this office's rows that were added, removed and changed, and counts every other change", () => {
  const active = replay(catalog, [{ kind: "addRow", at: { ...at, plan: "self-pay-medical" } }])
  const restored = replay(active, [
    { kind: "removeRow", at: { ...at, plan: "self-pay-medical" } },
    { kind: "setDoctor", at, doctor: "Dr. A", answer: "no" },
    { kind: "changeRow", at, change: { notice: "Synthetic notice" } },
    { kind: "createPlan", at: { ...at, plan: "gamma" }, plan: { ...gamma, label: "Gamma EPO" } },
    { kind: "changePlan", at: { ...at, plan: "beta" }, change: { label: "Beta Plus" } },
    { kind: "addRow", at: { region: "south", table: "optical", plan: "beta" } },
    { kind: "changePlan", at: { region: "north", table: "spring", plan: "alpha" }, change: { family: "Alpha North" } },
    { kind: "addRow", at: { region: "north", table: "spring", plan: "alpha" } },
  ])
  restored.carriers = [...restored.carriers, { id: "car4", code: "DEL01", name: "DELTA" }]

  assert.deepEqual(diffCatalogs(active, restored, "hollywood"), {
    plans: [
      { change: "changed", region: "south", plan: "beta", label: "Beta Plus", fields: ["label"] },
      { change: "removed", region: "south", plan: "self-pay-medical", label: "Self pay", fields: [] },
      { change: "added", region: "south", plan: "gamma", label: "Gamma EPO", fields: [] },
    ],
    rows: [
      { change: "changed", at, label: "Alpha PPO", fields: ["doctors", "notice"] },
      { change: "removed", at: { ...at, plan: "self-pay-medical" }, label: "Self pay", fields: [] },
      { change: "added", at: { ...at, plan: "gamma" }, label: "Gamma EPO", fields: [] },
    ],
    tables: [],
    elsewhere: 4,
  })
  assert.deepEqual(diffCatalogs(active, restored, "spring").rows, [
    { change: "added", at: { region: "north", table: "spring", plan: "alpha" }, label: "Alpha PPO", fields: [] },
  ])
})

test("the diff of a list with itself is empty, and a doctor column change on this office's table is named", () => {
  assert.deepEqual(diffCatalogs(catalog, structuredClone(catalog), "hollywood"), { plans: [], rows: [], tables: [], elsewhere: 0 })
  const fewerDoctors = structuredClone(catalog)
  fewerDoctors.regions[0].tables[0].doctors = []
  assert.deepEqual(diffCatalogs(catalog, fewerDoctors, "sweetwater"), {
    plans: [],
    rows: [],
    tables: [{ change: "changed", region: "south", table: "doctors", fields: ["doctors"] }],
    elsewhere: 0,
  })
})
