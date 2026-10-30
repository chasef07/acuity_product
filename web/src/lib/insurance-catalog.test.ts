import assert from "node:assert/strict"
import test from "node:test"

import type { InsuranceCatalog } from "./api/generated/types.gen.ts"
import { carrierChoices, changedPlans, diffCatalogs, record, replay, same, touchedOffices, type InsuranceEdit } from "./insurance-catalog.ts"

const catalog: InsuranceCatalog = {
  carriers: [
    { id: "car2", name: "BETA" },
    { id: "car1", name: "ALPHA" },
  ],
  regions: [
    {
      id: "south",
      plans: [
        { id: "alpha", label: "Alpha PPO", family: "Alpha", coverage: "medical", carrierCode: "ALP01", carrierId: "car1", selfPay: false, aliases: [] },
        { id: "beta", label: "Beta HMO", family: "Beta", coverage: "medical", carrierCode: "BET01", carrierId: "car2", selfPay: false, aliases: [] },
        { id: "cash", label: "Self pay", family: "Self-pay", coverage: "medical", carrierCode: "", carrierId: "", selfPay: true, aliases: [] },
      ],
      tables: [
        {
          id: "doctors",
          offices: ["hollywood", "sweetwater"],
          doctors: ["Dr. A"],
          rows: [{ planId: "alpha", doctors: { "Dr. A": "yes" }, requires: [], onlyOffices: [], notice: "", note: "" }],
        },
        { id: "optical", offices: ["optical"], doctors: ["Dr. B"], rows: [] },
      ],
    },
    {
      id: "north",
      plans: [{ id: "alpha", label: "Alpha PPO", family: "Alpha", coverage: "medical", carrierCode: "ALP01", carrierId: "car1", selfPay: false, aliases: [] }],
      tables: [{ id: "spring", offices: ["spring"], doctors: ["Dr. C"], rows: [] }],
    },
  ],
}

const at = { region: "south", table: "doctors", plan: "alpha" }

test("a table row change touches every office on that table and a plan change touches every office of the region", () => {
  assert.deepEqual(touchedOffices(catalog, replay(catalog, [{ kind: "setDoctor", at, doctor: "Dr. A", answer: "no" }])), ["hollywood", "sweetwater"])
  assert.deepEqual(touchedOffices(catalog, replay(catalog, [{ kind: "changePlan", at, change: { label: "Alpha Choice" } }])), ["hollywood", "optical", "sweetwater"])
  assert.deepEqual(touchedOffices(catalog, catalog), [])
})

test("undoing an edit by hand leaves nothing to publish", () => {
  const edits: InsuranceEdit[] = [
    { kind: "setDoctor", at, doctor: "Dr. A", answer: "pending" },
    { kind: "setRequirement", at, requirement: "prior_authorization", channel: "availity" },
    { kind: "setRequirement", at, requirement: "prior_authorization", channel: undefined },
    { kind: "setDoctor", at, doctor: "Dr. A", answer: "yes" },
  ]
  assert.ok(same(replay(catalog, edits), catalog))
  assert.equal(changedPlans(catalog, replay(catalog, edits.slice(0, 2))), 1)
})

test("removing a plan created in the draft forgets it instead of leaving an unused plan behind", () => {
  const created: PlanAtEdit = { kind: "createPlan", at: { ...at, plan: "gamma" }, label: "Gamma", family: "Gamma", coverage: "medical" }
  let edits = record([], created)
  edits = record(edits, { kind: "setDoctor", at: created.at, doctor: "Dr. A", answer: "yes" })
  assert.equal(replay(catalog, edits).regions[0].plans.length, 4)
  edits = record(edits, { kind: "removeRow", at: created.at })
  assert.deepEqual(edits, [])
  const removed = replay(catalog, record([], { kind: "removeRow", at }))
  assert.deepEqual(removed.regions[0].tables[0].rows, [])
  assert.deepEqual(planIDs(removed, "south"), ["beta", "cash"])
})

const optical = { ...at, table: "optical" }
const listedTwice = replay(catalog, [{ kind: "addRow", at: optical }])

function planIDs(revised: InsuranceCatalog, region: string) {
  return revised.regions.find((item) => item.id === region)?.plans.map((plan) => plan.id)
}

test("removing the last row that lists a plan removes the plan from its region, and removing one of two rows keeps it", () => {
  const oneLeft = replay(listedTwice, [{ kind: "removeRow", at }])
  assert.deepEqual(planIDs(oneLeft, "south"), ["alpha", "beta", "cash"])
  assert.deepEqual(oneLeft.regions[0].tables[1].rows.map((row) => row.planId), ["alpha"])

  const noneLeft = replay(listedTwice, [{ kind: "removeRow", at }, { kind: "removeRow", at: optical }])
  assert.deepEqual(planIDs(noneLeft, "south"), ["beta", "cash"])
  assert.deepEqual(planIDs(noneLeft, "north"), ["alpha"])
  assert.deepEqual(touchedOffices(listedTwice, noneLeft), ["hollywood", "optical", "sweetwater"])
  assert.equal(changedPlans(listedTwice, noneLeft), 2)
})

test("a remove replayed onto a newer list keeps the plan only while another table still lists it", () => {
  const mine = record([], { kind: "removeRow", at })
  const theirsRemovedOptical = replay(listedTwice, [{ kind: "removeRow", at: optical }])
  assert.deepEqual(planIDs(theirsRemovedOptical, "south"), ["alpha", "beta", "cash"])
  assert.deepEqual(planIDs(replay(theirsRemovedOptical, mine), "south"), ["beta", "cash"])

  const theirsAddedOptical = listedTwice
  const replayed = replay(theirsAddedOptical, mine)
  assert.deepEqual(planIDs(replayed, "south"), ["alpha", "beta", "cash"])
  assert.deepEqual(replayed.regions[0].tables[1].rows.map((row) => row.planId), ["alpha"])
})

test("carrier choices are the code and ID pairs plans already use, named from the carrier list, with no carrier first", () => {
  assert.deepEqual(
    carrierChoices(catalog).map(({ label, code, id }) => ({ label, code, id })),
    [
      { label: "No AMD carrier", code: "", id: "" },
      { label: "ALPHA · ALP01 · car1", code: "ALP01", id: "car1" },
      { label: "BETA · BET01 · car2", code: "BET01", id: "car2" },
    ],
  )
})

type PlanAtEdit = Extract<InsuranceEdit, { kind: "createPlan" }>

test("the diff names the plans and this office's rows that were added, removed and changed, and counts every other change", () => {
  const active = replay(catalog, [{ kind: "addRow", at: { ...at, plan: "cash" } }])
  const restored = replay(active, [
    { kind: "removeRow", at: { ...at, plan: "cash" } },
    { kind: "setDoctor", at, doctor: "Dr. A", answer: "no" },
    { kind: "changeRow", at, change: { note: "Synthetic note" } },
    { kind: "createPlan", at: { ...at, plan: "gamma" }, label: "Gamma EPO", family: "Gamma", coverage: "medical" },
    { kind: "changePlan", at: { ...at, plan: "beta" }, change: { label: "Beta Plus" } },
    { kind: "addRow", at: { region: "south", table: "optical", plan: "beta" } },
    { kind: "changePlan", at: { region: "north", table: "spring", plan: "alpha" }, change: { family: "Alpha North" } },
    { kind: "addRow", at: { region: "north", table: "spring", plan: "alpha" } },
  ])
  restored.carriers = [...restored.carriers, { id: "car3", name: "GAMMA" }]

  assert.deepEqual(diffCatalogs(active, restored, "hollywood"), {
    plans: [
      { change: "changed", region: "south", plan: "beta", label: "Beta Plus", fields: ["label"] },
      { change: "added", region: "south", plan: "gamma", label: "Gamma EPO", fields: [] },
    ],
    rows: [
      { change: "changed", at, label: "Alpha PPO", fields: ["doctors", "note"] },
      { change: "removed", at: { ...at, plan: "cash" }, label: "Self pay", fields: [] },
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
