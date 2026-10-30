"use client"

import { useEffect, useId, useMemo, useRef, useState, useSyncExternalStore, type ReactNode } from "react"
import { PlusIcon, SearchIcon } from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group"
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select"
import { Spinner } from "@/components/ui/spinner"
import { Textarea } from "@/components/ui/textarea"
import type {
  InsuranceCatalog,
  InsuranceOfficeTable,
  InsuranceRegion,
  InsuranceRegionPlan,
  InsuranceTableRow,
  InsuranceValidationError,
  LocationInsuranceCatalog,
} from "@/lib/api/generated/types.gen"
import { publishInsuranceCatalog, readInsuranceCatalog, useInsuranceCatalog } from "@/lib/clients/insurance-catalog"
import type { PortalFailure } from "@/lib/clients/portal-request"
import { draftScope } from "@/lib/drafts"
import { formatTimestamp } from "@/lib/format-time"
import {
  carrierChoices,
  carrierValue,
  changedPlans,
  doctorAnswers,
  newPlanID,
  officeTables,
  record,
  regionOffices,
  replay,
  requirementKinds,
  same,
  touchedOffices,
  type CarrierChoice,
  type DoctorAnswer,
  type InsuranceEdit,
  type PlanAt,
} from "@/lib/insurance-catalog"
import { insuranceDrafts, type InsuranceDraft as Draft } from "@/lib/insurance-drafts"
import { fold } from "@/lib/knowledge-publication"
import { doctorAnswerLabels, insuranceCoverageLabels, insuranceRequirementLabels } from "@/lib/vocabulary"
import { AgentPage, AgentQueryBody, DisclosureRow, type AgentLocationProps } from "./manage-agent-page"

const list = new Intl.ListFormat("en", { type: "conjunction" })
const coverageOrder = Object.keys(insuranceCoverageLabels)

type Names = (office: string) => string

function officeNames(loaded: LocationInsuranceCatalog | undefined): Names {
  return (office) => loaded?.officeNames[office] ?? office
}

function coverageLabel(coverage: string) {
  return insuranceCoverageLabels[coverage] ?? coverage
}

function fresh(loaded: LocationInsuranceCatalog): Draft {
  return { base: { revision: loaded.revision, catalog: loaded.catalog }, edits: [], reason: "", publish: { status: "idle" } }
}

function save(key: string, next: Draft) {
  const changed = !same(replay(next.base.catalog, next.edits), next.base.catalog)
  insuranceDrafts.write(key, changed || next.reason.trim() ? next : undefined)
}

export function InsuranceList({ actorSubject, ...props }: AgentLocationProps & { actorSubject: string }) {
  const catalog = useInsuranceCatalog(props)
  const loaded = catalog.status === "ready" ? catalog.data : undefined
  const key = draftScope(actorSubject, props.practiceID)
  const stored = useSyncExternalStore(insuranceDrafts.subscribe, () => insuranceDrafts.read(key), () => undefined)
  const draft = loaded?.canEdit ? stored : undefined
  const [search, setSearch] = useState("")
  const names = useMemo(() => officeNames(loaded), [loaded])
  const words = fold(search).split(/\s+/).filter(Boolean)
  const wasPublishing = useRef(stored?.publish.status === "publishing")
  const reload = catalog.retry

  useEffect(() => {
    if (wasPublishing.current && !stored) void reload()
    wasPublishing.current = stored?.publish.status === "publishing"
  }, [stored, reload])

  return (
    <AgentPage
      title="Insurance list"
      detail={
        loaded && (
          <>
            Published <time dateTime={loaded.revision.createdAt}>{formatTimestamp(loaded.revision.createdAt)}</time> by{" "}
            {loaded.revision.createdBy}
          </>
        )
      }
      controls={
        props.locationID ? (
          <>
            <InputGroup className="w-full sm:w-72">
              <InputGroupAddon>
                <SearchIcon />
              </InputGroupAddon>
              <InputGroupInput
                type="search"
                aria-label="Search plans"
                autoComplete="off"
                placeholder="Search plans"
                value={search}
                onChange={(event) => setSearch(event.target.value)}
              />
            </InputGroup>
            {draft && loaded && (
              <PublishBar
                draftKey={key}
                draft={draft}
                loaded={loaded}
                names={names}
                practiceID={props.practiceID}
                locationID={props.locationID}
                onReload={reload}
              />
            )}
          </>
        ) : null
      }
    >
      <AgentQueryBody
        query={catalog}
        subject="insurance list"
        unavailable="The insurance list isn't available right now."
        onRetry={reload}
        missingLocation={!props.locationID}
        isEmpty={(data) => officeTables(data.catalog, data.officeId).length === 0}
        emptyTitle="This office isn't on the insurance list."
        emptyDescription="Abita can't check insurance for this office until it has a table on the list."
      >
        {(data) => (
          <InsuranceEditor
            draftKey={key}
            draft={draft}
            loaded={data}
            names={names}
            words={words}
            onEdited={() => setSearch("")}
          />
        )}
      </AgentQueryBody>
    </AgentPage>
  )
}

type Row = { at: PlanAt; region: InsuranceRegion; table: InsuranceOfficeTable; plan: InsuranceRegionPlan; row: InsuranceTableRow }

function officeRows(catalog: InsuranceCatalog, office: string): Row[] {
  return officeTables(catalog, office).flatMap(({ region, table }) => {
    const plans = new Map(region.plans.map((plan) => [plan.id, plan]))
    return table.rows.flatMap((row) => {
      const plan = plans.get(row.planId)
      return plan ? [{ at: { region: region.id, table: table.id, plan: row.planId }, region, table, plan, row }] : []
    })
  })
}

function rowErrors(errors: InsuranceValidationError[], at: PlanAt) {
  return errors.filter((error) => error.region === at.region && error.planId === at.plan && (error.table === "" || error.table === at.table))
}

function matches(row: Row, carriers: Map<string, string>, words: string[]) {
  const haystack = fold([row.plan.label, row.plan.family, ...row.plan.aliases, row.plan.carrierCode, carriers.get(row.plan.carrierId) ?? ""].join("\n"))
  return words.every((word) => haystack.includes(word))
}

function InsuranceEditor({
  draftKey,
  draft,
  loaded,
  names,
  words,
  onEdited,
}: {
  draftKey: string
  draft: Draft | undefined
  loaded: LocationInsuranceCatalog
  names: Names
  words: string[]
  onEdited: () => void
}) {
  const working = draft ?? fresh(loaded)
  const catalog = useMemo(() => replay(working.base.catalog, working.edits), [working.base.catalog, working.edits])
  const choices = useMemo(() => carrierChoices(working.base.catalog), [working.base.catalog])
  const carriers = useMemo(() => new Map(catalog.carriers.map((carrier) => [carrier.id, carrier.name])), [catalog.carriers])
  const [editing, setEditing] = useState("")
  const [adding, setAdding] = useState(false)
  const [seenRevision, setSeenRevision] = useState(loaded.revision.id)
  if (seenRevision !== loaded.revision.id) {
    setSeenRevision(loaded.revision.id)
    if (!draft) setEditing("")
  }
  const errors = working.publish.status === "rejected" ? working.publish.errors : []
  const rows = officeRows(catalog, loaded.officeId)
  const tables = officeTables(catalog, loaded.officeId)
  const canEdit = loaded.canEdit

  function apply(edit: InsuranceEdit) {
    const current = insuranceDrafts.read(draftKey) ?? working
    save(draftKey, { ...current, edits: record(current.edits, edit) })
  }

  const shown = rows.filter(
    (row) => JSON.stringify(row.at) === editing || rowErrors(errors, row.at).length > 0 || matches(row, carriers, words),
  )
  const groups = [...new Set(rows.map((row) => row.plan.coverage))].sort(
    (left, right) => rank(left) - rank(right) || left.localeCompare(right),
  )
  const tableErrors = errors.filter(
    (error) => error.planId === "" && tables.some(({ region, table }) => error.region === region.id && error.table === table.id),
  )

  return (
    <fieldset disabled={working.publish.status === "publishing"} className="flex min-w-0 flex-col gap-4">
      <p aria-live="polite" className="text-sm text-muted-foreground empty:hidden">
        {words.length > 0 ? (shown.length > 0 ? `${shown.length} of ${rows.length} plans` : "No plans match your search.") : null}
      </p>
      {tableErrors.map((error) => (
        <p key={`${error.rule}:${error.message}`} role="alert" className="text-sm text-destructive">
          {error.message}
        </p>
      ))}
      {groups.map((coverage) => {
        const group = shown
          .filter((row) => row.plan.coverage === coverage)
          .sort((left, right) => left.plan.label.localeCompare(right.plan.label, undefined, { sensitivity: "base" }))
        return group.length > 0 ? (
          <DisclosureRow key={`${coverage}:${words.join(" ")}`} group title={coverageLabel(coverage)} detail={group.length}>
            <ul className="mt-1 divide-y overflow-hidden rounded-lg border bg-card">
              {group.map((row) => {
                const id = JSON.stringify(row.at)
                const problems = rowErrors(errors, row.at)
                return (
                  <PlanRow
                    key={problems.length > 0 ? `${id}:${working.attempt?.revisionID}` : id}
                    row={row}
                    carriers={carriers}
                    choices={choices}
                    names={names}
                    office={loaded.officeId}
                    problems={problems}
                    canEdit={canEdit}
                    editing={editing === id}
                    onEditing={(open) => setEditing(open ? id : "")}
                    onEdit={apply}
                  />
                )
              })}
            </ul>
          </DisclosureRow>
        ) : null
      })}
      {canEdit && tables.length > 0 && (
        adding ? (
          <AddPlan
            region={tables[0].region}
            table={tables[0].table}
            onCancel={() => setAdding(false)}
            onAdd={(edit) => {
              apply(edit)
              setAdding(false)
              setEditing(JSON.stringify(edit.at))
              onEdited()
            }}
          />
        ) : (
          <Button variant="outline" className="w-fit" onClick={() => setAdding(true)}>
            <PlusIcon data-icon="inline-start" />
            Add plan
          </Button>
        )
      )}
    </fieldset>
  )
}

function rank(coverage: string) {
  const index = coverageOrder.indexOf(coverage)
  return index < 0 ? coverageOrder.length : index
}

function rowSummary(row: Row, names: Names) {
  const answers = row.table.doctors.map((doctor) => [doctor, row.row.doctors[doctor]] as const)
  const yes = answers.filter(([, answer]) => answer === "yes").map(([doctor]) => doctor)
  const pending = answers.filter(([, answer]) => answer === "pending").length
  return [
    yes.length > 0 ? yes.join(", ") : "No doctors take it",
    pending > 0 ? `${pending} pending` : "",
    row.row.onlyOffices.length > 0 ? `Only at ${list.format(row.row.onlyOffices.map(names))}` : "",
  ]
    .filter(Boolean)
    .join(" · ")
}

function PlanRow({
  row,
  carriers,
  choices,
  names,
  office,
  problems,
  canEdit,
  editing,
  onEditing,
  onEdit,
}: {
  row: Row
  carriers: Map<string, string>
  choices: CarrierChoice[]
  names: Names
  office: string
  problems: InsuranceValidationError[]
  canEdit: boolean
  editing: boolean
  onEditing: (open: boolean) => void
  onEdit: (edit: InsuranceEdit) => void
}) {
  const { plan, row: entry, table, at } = row
  const [openAtMount] = useState(editing || problems.length > 0)
  const carrier = [carriers.get(plan.carrierId), plan.carrierCode, plan.carrierId].filter(Boolean).join(" · ")
  const requires = entry.requires
    .map((item) => `${insuranceRequirementLabels[item.kind as keyof typeof insuranceRequirementLabels] ?? item.kind}${item.channel ? ` (${item.channel})` : ""}`)
    .join(", ")
  const details = [
    { label: "Family", value: plan.family },
    { label: "Visit coverage", value: `${coverageLabel(plan.coverage)}${plan.selfPay ? " · Self-pay" : ""}` },
    { label: "AMD carrier", value: carrier },
    { label: "Callers also say", value: plan.aliases.join(", ") },
    {
      label: "Doctors",
      value: table.doctors
        .map((doctor) => `${doctor}: ${entry.doctors[doctor] ? doctorAnswerLabels[entry.doctors[doctor] as DoctorAnswer] ?? entry.doctors[doctor] : "Not set"}`)
        .join("\n"),
    },
    { label: "Requires", value: requires },
    { label: "Only at", value: list.format(entry.onlyOffices.map(names)) },
    { label: "Callers hear", value: entry.notice },
    { label: "Staff note", value: entry.note },
  ].filter((item) => item.value)

  return (
    <DisclosureRow title={plan.label || "Untitled plan"} detail={rowSummary(row, names)} aside={plan.carrierCode} defaultOpen={openAtMount}>
      <div className="flex flex-col gap-3 px-3 pb-3 pl-9 text-sm">
        {problems.map((problem) => (
          <p key={`${problem.rule}:${problem.message}`} role="alert" className="text-destructive">
            {problem.message}
          </p>
        ))}
        {editing ? (
          <PlanFields row={row} choices={choices} names={names} office={office} onEdit={onEdit} />
        ) : (
          <dl className="grid gap-x-4 gap-y-1.5 sm:grid-cols-[9rem_1fr]">
            {details.map((item) => (
              <div key={item.label} className="contents">
                <dt className="text-muted-foreground">{item.label}</dt>
                <dd className="whitespace-pre-line">{item.value}</dd>
              </div>
            ))}
          </dl>
        )}
        {canEdit && (
          <div className="flex gap-2">
            <Button size="sm" variant="outline" onClick={() => onEditing(!editing)}>
              {editing ? "Done" : "Edit"}
            </Button>
            <Button size="sm" variant="ghost" onClick={() => onEdit({ kind: "removeRow", at })}>
              Remove
            </Button>
          </div>
        )}
      </div>
    </DisclosureRow>
  )
}

function Field({ label, children }: { label: string; children: (id: string) => ReactNode }) {
  const id = useId()
  return (
    <div className="flex flex-col gap-1.5">
      <label htmlFor={id} className="text-sm font-medium">
        {label}
      </label>
      {children(id)}
    </div>
  )
}

function Check({ label, checked, onChange }: { label: string; checked: boolean; onChange: (checked: boolean) => void }) {
  return (
    <label className="flex items-center gap-2 text-sm">
      <input type="checkbox" className="size-4 accent-primary" checked={checked} onChange={(event) => onChange(event.target.checked)} />
      {label}
    </label>
  )
}

function PlanFields({
  row,
  choices,
  names,
  office,
  onEdit,
}: {
  row: Row
  choices: CarrierChoice[]
  names: Names
  office: string
  onEdit: (edit: InsuranceEdit) => void
}) {
  const { at, plan, row: entry, table, region } = row
  const [aliases, setAliases] = useState(plan.aliases.join("\n"))
  const sharedWith = regionOffices(region).filter((item) => item !== office).map(names)
  const current = carrierValue({ code: plan.carrierCode, id: plan.carrierId })
  const known = choices.some((choice) => choice.value === current)

  return (
    <div className="flex flex-col gap-4">
      <section aria-label="Plan details" className="flex flex-col gap-3">
        {sharedWith.length > 0 && (
          <p className="text-muted-foreground">
            The label, family, aliases, carrier and self-pay are shared with {list.format(sharedWith)}.
          </p>
        )}
        <Field label="Label">
          {(id) => <Input id={id} value={plan.label} maxLength={200} onChange={(event) => onEdit({ kind: "changePlan", at, change: { label: event.target.value } })} />}
        </Field>
        <Field label="Family">
          {(id) => <Input id={id} value={plan.family} maxLength={200} onChange={(event) => onEdit({ kind: "changePlan", at, change: { family: event.target.value } })} />}
        </Field>
        <Field label="Callers also say (one per line)">
          {(id) => (
            <Textarea
              id={id}
              value={aliases}
              onChange={(event) => {
                setAliases(event.target.value)
                onEdit({ kind: "changePlan", at, change: { aliases: event.target.value.split("\n").map((alias) => alias.trim()).filter(Boolean) } })
              }}
            />
          )}
        </Field>
        <Field label="AMD carrier">
          {(id) => (
            <NativeSelect
              id={id}
              className="w-full sm:w-96"
              value={current}
              onChange={(event) => {
                const choice = choices.find((item) => item.value === event.target.value)
                if (choice) onEdit({ kind: "setCarrier", at, carrier: { code: choice.code, id: choice.id } })
              }}
            >
              {!known && (
                <NativeSelectOption value={current} disabled>
                  {[plan.carrierCode, plan.carrierId].join(" · ")}
                </NativeSelectOption>
              )}
              {choices.map((choice) => (
                <NativeSelectOption key={choice.value} value={choice.value}>
                  {choice.label}
                </NativeSelectOption>
              ))}
            </NativeSelect>
          )}
        </Field>
        <Check label="Self-pay" checked={plan.selfPay} onChange={(selfPay) => onEdit({ kind: "changePlan", at, change: { selfPay } })} />
      </section>
      <section aria-label="This office's table" className="flex flex-col gap-3">
        <div className="grid gap-3 sm:grid-cols-2">
          {table.doctors.map((doctor) => (
            <Field key={doctor} label={doctor}>
              {(id) => (
                <NativeSelect
                  id={id}
                  className="w-full"
                  value={entry.doctors[doctor] ?? ""}
                  onChange={(event) =>
                    onEdit({ kind: "setDoctor", at, doctor, answer: (event.target.value || undefined) as DoctorAnswer | undefined })
                  }
                >
                  <NativeSelectOption value="">Not set</NativeSelectOption>
                  {doctorAnswers.map((answer) => (
                    <NativeSelectOption key={answer} value={answer}>
                      {doctorAnswerLabels[answer]}
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
              )}
            </Field>
          ))}
        </div>
        {requirementKinds.map((kind) => {
          const requirement = entry.requires.find((item) => item.kind === kind)
          const label = insuranceRequirementLabels[kind]
          return (
            <div key={kind} className="flex flex-wrap items-center gap-3">
              <Check
                label={label}
                checked={Boolean(requirement)}
                onChange={(checked) => onEdit({ kind: "setRequirement", at, requirement: kind, channel: checked ? "" : undefined })}
              />
              {requirement && (
                <Input
                  aria-label={`${label} channel`}
                  placeholder="Channel (optional)"
                  className="w-48"
                  value={requirement.channel}
                  maxLength={100}
                  onChange={(event) => onEdit({ kind: "setRequirement", at, requirement: kind, channel: event.target.value })}
                />
              )}
            </div>
          )
        })}
        {table.offices.length > 1 && (
          <fieldset className="flex flex-col gap-2">
            <legend className="mb-1 text-sm font-medium">Only at these offices (none checked means every office on this table)</legend>
            {table.offices.map((item) => (
              <Check
                key={item}
                label={names(item)}
                checked={entry.onlyOffices.includes(item)}
                onChange={(checked) =>
                  onEdit({
                    kind: "changeRow",
                    at,
                    change: { onlyOffices: checked ? [...entry.onlyOffices, item] : entry.onlyOffices.filter((office) => office !== item) },
                  })
                }
              />
            ))}
          </fieldset>
        )}
        <Field label="Callers hear">
          {(id) => <Textarea id={id} value={entry.notice} onChange={(event) => onEdit({ kind: "changeRow", at, change: { notice: event.target.value } })} />}
        </Field>
        <Field label="Staff note">
          {(id) => <Textarea id={id} value={entry.note} onChange={(event) => onEdit({ kind: "changeRow", at, change: { note: event.target.value } })} />}
        </Field>
      </section>
    </div>
  )
}

function AddPlan({
  region,
  table,
  onAdd,
  onCancel,
}: {
  region: InsuranceRegion
  table: InsuranceOfficeTable
  onAdd: (edit: Extract<InsuranceEdit, { kind: "addRow" | "createPlan" }>) => void
  onCancel: () => void
}) {
  const listed = new Set(table.rows.map((row) => row.planId))
  const available = region.plans
    .filter((plan) => !listed.has(plan.id))
    .sort((left, right) => left.label.localeCompare(right.label, undefined, { sensitivity: "base" }))
  const [choice, setChoice] = useState("")
  const [label, setLabel] = useState("")
  const [family, setFamily] = useState("")
  const [coverage, setCoverage] = useState(coverageOrder[0])
  const creating = choice === ""

  function add() {
    if (!creating) {
      onAdd({ kind: "addRow", at: { region: region.id, table: table.id, plan: choice } })
      return
    }
    const plan = newPlanID(region, label)
    onAdd({ kind: "createPlan", at: { region: region.id, table: table.id, plan }, label: label.trim(), family: family.trim(), coverage })
  }

  return (
    <section aria-label="Add plan" className="flex flex-col gap-3 rounded-lg border bg-card p-3 text-sm">
      <Field label="Plan">
        {(id) => (
          <NativeSelect id={id} className="w-full sm:w-96" value={choice} onChange={(event) => setChoice(event.target.value)}>
            <NativeSelectOption value="">A new plan</NativeSelectOption>
            {available.map((plan) => (
              <NativeSelectOption key={plan.id} value={plan.id}>
                {plan.label} ({coverageLabel(plan.coverage)})
              </NativeSelectOption>
            ))}
          </NativeSelect>
        )}
      </Field>
      {creating && (
        <>
          <Field label="New plan label">
            {(id) => <Input id={id} value={label} maxLength={200} onChange={(event) => setLabel(event.target.value)} />}
          </Field>
          <Field label="New plan family">
            {(id) => <Input id={id} value={family} maxLength={200} onChange={(event) => setFamily(event.target.value)} />}
          </Field>
          <Field label="Visit coverage">
            {(id) => (
              <NativeSelect id={id} className="w-full sm:w-60" value={coverage} onChange={(event) => setCoverage(event.target.value)}>
                {coverageOrder.map((item) => (
                  <NativeSelectOption key={item} value={item}>
                    {coverageLabel(item)}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            )}
          </Field>
        </>
      )}
      <div className="flex gap-2">
        <Button size="sm" disabled={creating && !label.trim()} onClick={add}>
          Add
        </Button>
        <Button size="sm" variant="ghost" onClick={onCancel}>
          Cancel
        </Button>
      </div>
    </section>
  )
}

function publishMessage(failure: PortalFailure) {
  if (failure.kind === "signedOut" || failure.kind === "unauthenticated") return "Sign in again to publish."
  if (failure.kind === "unauthorized")
    return failure.message ?? "You don't have access to every office this change affects. Nothing changed."
  if (failure.kind === "unavailable") return "The change couldn't be checked, so nothing was published. Try again."
  return failure.message ?? "Not published. Nothing changed. Try again."
}

function PublishBar({
  draftKey,
  draft,
  loaded,
  names,
  practiceID,
  locationID,
  onReload,
}: {
  draftKey: string
  draft: Draft
  loaded: LocationInsuranceCatalog
  names: Names
  practiceID: string
  locationID: string
  onReload: () => Promise<void>
}) {
  const reasonID = useId()
  const catalog = useMemo(() => replay(draft.base.catalog, draft.edits), [draft.base.catalog, draft.edits])
  const changed = !same(catalog, draft.base.catalog)
  const count = changedPlans(draft.base.catalog, catalog)
  const touched = touchedOffices(draft.base.catalog, catalog)
  const others = touched.filter((office) => office !== loaded.officeId).map(names)
  const publishing = draft.publish.status === "publishing"
  const placed = officeRows(catalog, loaded.officeId).map((row) => row.at)
  const tables = officeTables(catalog, loaded.officeId)
  const unplaced =
    draft.publish.status === "rejected"
      ? draft.publish.errors.filter(
          (error) =>
            !placed.some((at) => rowErrors([error], at).length > 0) &&
            !(error.planId === "" && tables.some(({ region, table }) => error.region === region.id && error.table === table.id)),
        )
      : []

  async function submit() {
    const publication = {
      practiceId: practiceID,
      locationId: locationID,
      expectedRevisionId: draft.base.revision.id,
      reason: draft.reason.trim(),
      catalog,
    }
    const payload = JSON.stringify(publication)
    const attempt = draft.attempt?.payload === payload ? draft.attempt : { payload, revisionID: crypto.randomUUID() }
    const sent: Draft = { ...draft, publish: { status: "publishing" }, attempt }
    insuranceDrafts.write(draftKey, sent)
    const outcome = await publishInsuranceCatalog({ ...publication, revisionId: attempt.revisionID })
    if (insuranceDrafts.read(draftKey) !== sent) return
    if (outcome.ok) {
      insuranceDrafts.write(draftKey, undefined)
      return
    }
    const { failure } = outcome
    if (failure.kind === "conflict") {
      const current = await readInsuranceCatalog({ practiceID, locationID })
      if (insuranceDrafts.read(draftKey) !== sent) return
      insuranceDrafts.write(
        draftKey,
        current.ok
          ? { ...sent, base: { revision: current.data.revision, catalog: current.data.catalog }, publish: { status: "conflict" } }
          : { ...sent, publish: { status: "failed", failure } },
      )
      if (current.ok) void onReload()
      return
    }
    insuranceDrafts.write(
      draftKey,
      failure.kind === "rejected" && failure.errors?.length
        ? { ...sent, publish: { status: "rejected", errors: failure.errors } }
        : { ...sent, publish: { status: "failed", failure } },
    )
  }

  function discard() {
    insuranceDrafts.write(draftKey, undefined)
    void onReload()
  }

  return (
    <fieldset
      disabled={publishing}
      className="flex min-w-0 flex-1 basis-xl flex-wrap items-center gap-2 rounded-lg border border-primary bg-primary/5 p-2"
    >
      {count > 0 && (
        <p className="shrink-0 text-sm font-medium">
          {count} unpublished {count === 1 ? "change" : "changes"}
        </p>
      )}
      <label htmlFor={reasonID} className="sr-only">
        Why are you changing this?
      </label>
      <Input
        id={reasonID}
        className="min-w-40 flex-1 bg-background"
        placeholder="Why are you changing this?"
        value={draft.reason}
        maxLength={2000}
        onChange={(event) => save(draftKey, { ...draft, reason: event.target.value })}
      />
      <Button variant="ghost" onClick={discard}>
        Discard changes
      </Button>
      <Button disabled={!changed || !draft.reason.trim()} onClick={() => void submit()}>
        {publishing && <Spinner data-icon="inline-start" />}
        {publishing ? "Publishing…" : "Publish"}
      </Button>
      {others.length > 0 && (
        <p role="status" className="basis-full text-sm">
          {touched.includes(loaded.officeId) ? "Also changes" : "Changes"} {list.format(others)}.
        </p>
      )}
      {draft.publish.status === "conflict" && (
        <Alert variant="destructive" className="basis-full">
          <AlertTitle>Someone else changed the insurance list.</AlertTitle>
          <AlertDescription>Your changes are kept on top of the current version. Check them, then publish again.</AlertDescription>
        </Alert>
      )}
      {draft.publish.status === "rejected" && (
        <Alert variant="destructive" className="basis-full">
          <AlertTitle>Not published. Fix the problems shown beside each plan, then publish again.</AlertTitle>
          {unplaced.length > 0 && (
            <AlertDescription>
              <ul className="list-disc pl-4">
                {unplaced.map((error) => (
                  <li key={`${error.region}:${error.table}:${error.planId}:${error.rule}:${error.message}`}>{error.message}</li>
                ))}
              </ul>
            </AlertDescription>
          )}
        </Alert>
      )}
      {draft.publish.status === "failed" && (
        <Alert variant="destructive" className="basis-full">
          <AlertTitle>
            {draft.publish.failure.kind === "conflict"
              ? "Someone else changed the insurance list, and the current version couldn't be loaded. Try again."
              : publishMessage(draft.publish.failure)}
          </AlertTitle>
        </Alert>
      )}
    </fieldset>
  )
}
