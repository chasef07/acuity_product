import type {
  InsuranceCatalog,
  InsuranceOfficeTable,
  InsuranceRegion,
  InsuranceRegionPlan,
  InsuranceTableRow,
} from "./api/generated/types.gen.ts"
import { fold } from "./knowledge-publication.ts"

export const doctorAnswers = ["yes", "no", "pending"] as const
export type DoctorAnswer = (typeof doctorAnswers)[number]

export const requirementKinds = ["prior_authorization", "pcp_referral", "staff_verify"] as const
export type RequirementKind = (typeof requirementKinds)[number]

export type PlanAt = { region: string; table: string; plan: string }
type PlanChange = Partial<Pick<InsuranceRegionPlan, "label" | "family" | "aliases" | "selfPay">>
type RowChange = Partial<Pick<InsuranceTableRow, "onlyOffices" | "notice" | "note">>
export type Carrier = { code: string; id: string }

export type InsuranceEdit =
  | { kind: "addRow"; at: PlanAt }
  | { kind: "createPlan"; at: PlanAt; label: string; family: string; coverage: string }
  | { kind: "removeRow"; at: PlanAt }
  | { kind: "changePlan"; at: PlanAt; change: PlanChange }
  | { kind: "setCarrier"; at: PlanAt; carrier: Carrier }
  | { kind: "setDoctor"; at: PlanAt; doctor: string; answer: DoctorAnswer | undefined }
  | { kind: "setRequirement"; at: PlanAt; requirement: RequirementKind; channel: string | undefined }
  | { kind: "changeRow"; at: PlanAt; change: RowChange }

function revise(catalog: InsuranceCatalog, edit: InsuranceEdit): InsuranceCatalog {
  return {
    ...catalog,
    regions: catalog.regions.map((region) => (region.id === edit.at.region ? reviseRegion(region, edit) : region)),
  }
}

export function replay(catalog: InsuranceCatalog, edits: InsuranceEdit[]) {
  return edits.reduce(revise, catalog)
}

export function record(edits: InsuranceEdit[], edit: InsuranceEdit) {
  const created = edits.some((earlier) => earlier.kind === "createPlan" && samePlan(earlier.at, edit.at))
  if (edit.kind === "removeRow" && created) return edits.filter((earlier) => !samePlan(earlier.at, edit.at))
  return [...edits, edit]
}

function samePlan(left: PlanAt, right: PlanAt) {
  return left.region === right.region && left.plan === right.plan
}

function reviseRegion(region: InsuranceRegion, edit: InsuranceEdit): InsuranceRegion {
  const { at } = edit
  switch (edit.kind) {
    case "createPlan": {
      if (region.plans.some((plan) => plan.id === at.plan)) return region
      const plan: InsuranceRegionPlan = {
        id: at.plan,
        label: edit.label,
        family: edit.family,
        coverage: edit.coverage,
        carrierCode: "",
        carrierId: "",
        selfPay: false,
        aliases: [],
      }
      return withTable({ ...region, plans: [...region.plans, plan] }, at, (table) => addRow(table, at.plan))
    }
    case "addRow":
      return region.plans.some((plan) => plan.id === at.plan) ? withTable(region, at, (table) => addRow(table, at.plan)) : region
    case "removeRow": {
      const removed = withTable(region, at, (table) => ({ ...table, rows: table.rows.filter((row) => row.planId !== at.plan) }))
      const listed = removed.tables.some((table) => table.rows.some((row) => row.planId === at.plan))
      return listed ? removed : { ...removed, plans: removed.plans.filter((plan) => plan.id !== at.plan) }
    }
    case "changePlan":
      return withPlan(region, at.plan, (plan) => ({ ...plan, ...edit.change }))
    case "setCarrier":
      return withPlan(region, at.plan, (plan) => ({ ...plan, carrierCode: edit.carrier.code, carrierId: edit.carrier.id }))
    case "setDoctor":
      return withRow(region, at, (row) => {
        const doctors = { ...row.doctors }
        if (edit.answer) doctors[edit.doctor] = edit.answer
        else delete doctors[edit.doctor]
        return { ...row, doctors }
      })
    case "setRequirement":
      return withRow(region, at, (row) => {
        const index = row.requires.findIndex((requirement) => requirement.kind === edit.requirement)
        if (edit.channel === undefined) return { ...row, requires: row.requires.filter((_, position) => position !== index) }
        const requirement = { kind: edit.requirement, channel: edit.channel }
        return {
          ...row,
          requires: index < 0 ? [...row.requires, requirement] : row.requires.map((item, position) => (position === index ? requirement : item)),
        }
      })
    case "changeRow":
      return withRow(region, at, (row) => ({ ...row, ...edit.change }))
  }
}

function addRow(table: InsuranceOfficeTable, planId: string): InsuranceOfficeTable {
  if (table.rows.some((row) => row.planId === planId)) return table
  return { ...table, rows: [...table.rows, { planId, doctors: {}, requires: [], onlyOffices: [], notice: "", note: "" }] }
}

function withTable(region: InsuranceRegion, at: PlanAt, change: (table: InsuranceOfficeTable) => InsuranceOfficeTable) {
  return { ...region, tables: region.tables.map((table) => (table.id === at.table ? change(table) : table)) }
}

function withPlan(region: InsuranceRegion, planId: string, change: (plan: InsuranceRegionPlan) => InsuranceRegionPlan) {
  return { ...region, plans: region.plans.map((plan) => (plan.id === planId ? change(plan) : plan)) }
}

function withRow(region: InsuranceRegion, at: PlanAt, change: (row: InsuranceTableRow) => InsuranceTableRow) {
  return withTable(region, at, (table) => ({
    ...table,
    rows: table.rows.map((row) => (row.planId === at.plan ? change(row) : row)),
  }))
}

export function same(left: unknown, right: unknown): boolean {
  if (left === right) return true
  if (typeof left !== "object" || typeof right !== "object" || left === null || right === null) return false
  if (Array.isArray(left) !== Array.isArray(right)) return false
  const leftFields = left as Record<string, unknown>
  const rightFields = right as Record<string, unknown>
  const keys = Object.keys(leftFields)
  return (
    keys.length === Object.keys(rightFields).length &&
    keys.every((key) => Object.hasOwn(rightFields, key) && same(leftFields[key], rightFields[key]))
  )
}

export type OfficeTable = { region: InsuranceRegion; table: InsuranceOfficeTable }

export function officeTables(catalog: InsuranceCatalog, office: string): OfficeTable[] {
  return catalog.regions.flatMap((region) =>
    region.tables.filter((table) => table.offices.includes(office)).map((table) => ({ region, table })),
  )
}

export function regionOffices(region: InsuranceRegion) {
  return [...new Set(region.tables.flatMap((table) => table.offices))]
}

export function touchedOffices(before: InsuranceCatalog, after: InsuranceCatalog) {
  const offices = [...new Set([...allOffices(before), ...allOffices(after)])].sort()
  return offices.filter((office) => !same(officeView(before, office), officeView(after, office)))
}

function allOffices(catalog: InsuranceCatalog) {
  return catalog.regions.flatMap(regionOffices)
}

function officeView(catalog: InsuranceCatalog, office: string) {
  return {
    carriers: catalog.carriers,
    regions: catalog.regions.flatMap((region) => {
      const tables = region.tables.filter((table) => table.offices.includes(office))
      return tables.length > 0 ? [{ id: region.id, plans: region.plans, tables }] : []
    }),
  }
}

export function changedPlans(before: InsuranceCatalog, after: InsuranceCatalog) {
  const places = new Map<string, PlanAt>()
  for (const region of [...before.regions, ...after.regions]) {
    for (const table of region.tables) {
      for (const row of table.rows) {
        const at = { region: region.id, table: table.id, plan: row.planId }
        places.set(JSON.stringify(at), at)
      }
    }
  }
  return [...places.values()].filter((at) => !same(planRow(before, at), planRow(after, at))).length
}

function planRow(catalog: InsuranceCatalog, at: PlanAt) {
  const region = catalog.regions.find((item) => item.id === at.region)
  return {
    plan: region?.plans.find((plan) => plan.id === at.plan),
    row: region?.tables.find((table) => table.id === at.table)?.rows.find((row) => row.planId === at.plan),
  }
}

export type CarrierChoice = Carrier & { value: string; label: string }

export function carrierChoices(catalog: InsuranceCatalog): CarrierChoice[] {
  const names = new Map(catalog.carriers.map((carrier) => [carrier.id, carrier.name]))
  const choices = new Map<string, CarrierChoice>()
  for (const plan of catalog.regions.flatMap((region) => region.plans)) {
    const value = carrierValue({ code: plan.carrierCode, id: plan.carrierId })
    if (choices.has(value)) continue
    const label = plan.carrierCode || plan.carrierId
      ? [names.get(plan.carrierId), plan.carrierCode, plan.carrierId].filter(Boolean).join(" · ")
      : "No AMD carrier"
    choices.set(value, { code: plan.carrierCode, id: plan.carrierId, value, label })
  }
  return [...choices.values()].sort(
    (left, right) => Number(left.value !== "|") - Number(right.value !== "|") || left.label.localeCompare(right.label),
  )
}

export function carrierValue(carrier: Carrier) {
  return `${carrier.code}|${carrier.id}`
}

export function newPlanID(region: InsuranceRegion, label: string) {
  const slug = fold(label).replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 90) || "plan"
  const used = new Set(region.plans.map((plan) => plan.id))
  let candidate = slug
  for (let suffix = 2; used.has(candidate); suffix++) candidate = `${slug}-${suffix}`
  return candidate
}
