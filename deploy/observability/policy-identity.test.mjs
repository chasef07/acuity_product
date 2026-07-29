import assert from "node:assert/strict"
import test from "node:test"
import {
  preparePolicy,
  validateDesiredPolicies,
} from "./policy-identity.mjs"

const desired = {
  displayName: "Renamed policy",
  userLabels: {
    acuity_contract: "call_center_v1",
    acuity_policy: "provider_commands",
  },
  conditions: [
    { displayName: "Existing condition", conditionThreshold: {} },
    { displayName: "New condition", conditionThreshold: {} },
  ],
}

test("stable policy key and condition display names preserve resource names", () => {
  validateDesiredPolicies([desired])
  const existing = {
    name: "projects/test/alertPolicies/1",
    displayName: "Old policy name",
    userLabels: { acuity_policy: "provider_commands" },
    conditions: [
      {
        name: "projects/test/alertPolicies/1/conditions/1",
        displayName: "Existing condition",
      },
    ],
  }

  const prepared = preparePolicy(desired, [existing])

  assert.equal(prepared.existing, existing)
  assert.equal(prepared.rendered.name, existing.name)
  assert.equal(
    prepared.rendered.conditions[0].name,
    existing.conditions[0].name,
  )
  assert.equal(prepared.rendered.conditions[1].name, undefined)
})

test("duplicate stable policy keys fail closed", () => {
  assert.throws(
    () => preparePolicy(desired, [
      {
        name: "projects/test/alertPolicies/1",
        userLabels: { acuity_policy: "provider_commands" },
      },
      {
        name: "projects/test/alertPolicies/2",
        userLabels: { acuity_policy: "provider_commands" },
      },
    ]),
    /multiple alert policies/,
  )
})

test("duplicate existing condition display names fail closed", () => {
  assert.throws(
    () => preparePolicy(desired, [{
      name: "projects/test/alertPolicies/1",
      userLabels: { acuity_policy: "provider_commands" },
      conditions: [
        { name: "conditions/1", displayName: "Existing condition" },
        { name: "conditions/2", displayName: "Existing condition" },
      ],
    }]),
    /invalid condition identity/,
  )
})
