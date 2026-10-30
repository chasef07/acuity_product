import assert from "node:assert/strict"
import test from "node:test"

import type { InsuranceCatalog } from "./api/generated/types.gen.ts"
import { carrierChoices, changedPlans, record, replay, same, touchedOffices, type InsuranceEdit } from "./insurance-catalog.ts"

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
  assert.equal(removed.regions[0].plans.length, 3)
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
