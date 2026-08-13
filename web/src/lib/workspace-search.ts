import { normalizeUSPhone } from "./phone.ts"

export function resolveWorkspaceSearch(value: string) {
  const query = value.trim()
  const phone = normalizeUSPhone(query)
  return phone
    ? ({ kind: "phone", value: phone } as const)
    : ({ kind: "tasks", value: query } as const)
}
