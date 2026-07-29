const policyKeyPattern = /^[a-z][a-z0-9_]{0,62}$/

export function validateDesiredPolicies(policies) {
  const policyKeys = new Set()
  for (const [index, policy] of policies.entries()) {
    const policyKey = policy.userLabels?.acuity_policy
    if (
      !policy.displayName ||
      policy.name ||
      !policyKeyPattern.test(policyKey) ||
      policyKeys.has(policyKey)
    ) {
      throw new Error(`alert policy ${index} has invalid identity`)
    }
    policyKeys.add(policyKey)

    const conditionDisplayNames = new Set()
    for (const condition of policy.conditions ?? []) {
      if (
        !condition.displayName ||
        condition.name ||
        conditionDisplayNames.has(condition.displayName)
      ) {
        throw new Error(
          `alert policy ${policyKey} has invalid condition identity`,
        )
      }
      conditionDisplayNames.add(condition.displayName)
    }
  }
}

export function preparePolicy(desired, existingPolicies) {
  const policyKey = desired.userLabels.acuity_policy
  const matches = existingPolicies.filter(
    (existing) => existing.userLabels?.acuity_policy === policyKey,
  )
  if (matches.length > 1) {
    throw new Error(`multiple alert policies have key ${policyKey}`)
  }
  if (matches.length === 0) {
    return { existing: undefined, rendered: desired }
  }

  const existing = matches[0]
  if (!existing.name) {
    throw new Error(`existing alert policy ${policyKey} has no resource name`)
  }
  const existingByDisplayName = new Map()
  for (const condition of existing.conditions ?? []) {
    if (
      !condition.displayName ||
      !condition.name ||
      existingByDisplayName.has(condition.displayName)
    ) {
      throw new Error(
        `existing alert policy ${existing.name} has invalid condition identity`,
      )
    }
    existingByDisplayName.set(condition.displayName, condition)
  }

  return {
    existing,
    rendered: {
      ...desired,
      name: existing.name,
      conditions: desired.conditions.map((condition) => {
        const matched = existingByDisplayName.get(condition.displayName)
        return matched ? { ...condition, name: matched.name } : condition
      }),
    },
  }
}
