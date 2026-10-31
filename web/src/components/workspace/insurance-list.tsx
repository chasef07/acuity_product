"use client"

import { useEffect, useId, useMemo, useRef, useState, useSyncExternalStore, type ReactNode } from "react"
import { HistoryIcon, PlusIcon, SearchIcon } from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group"
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { Spinner } from "@/components/ui/spinner"
import { Textarea } from "@/components/ui/textarea"
import type {
  InsuranceCarrier,
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
  carrierLabel,
  changedPlans,
  doctorAnswer,
  doctorAnswers,
  isSelfPay,
  newPlanID,
  officeTables,
  planOffices,
  record,
  replay,
  same,
  touchedOffices,
  type BeforeBooking,
  type CarrierChoice,
  type Coverage,
  type DoctorAnswer,
  type InsuranceEdit,
  type NewPlan,
  type PlanAt,
} from "@/lib/insurance-catalog"
import { insuranceDrafts, type InsuranceDraft as Draft } from "@/lib/insurance-drafts"
import { fold } from "@/lib/knowledge-publication"
import { doctorAnswerLabels, insuranceBeforeBookingLabels, insuranceCoverageLabels } from "@/lib/vocabulary"
import { InsuranceHistory } from "./insurance-history"
import { AgentPage, AgentQueryBody, DisclosureRow, type AgentLocationProps } from "./manage-agent-page"

const list = new Intl.ListFormat("en", { type: "conjunction" })
const coverageOrder = Object.keys(insuranceCoverageLabels) as Coverage[]
const beforeBookingOrder = Object.keys(insuranceBeforeBookingLabels) as BeforeBooking[]

type Names = (office: string) => string

export function officeNames(loaded: LocationInsuranceCatalog | undefined): Names {
  return (office) => loaded?.officeNames[office] ?? office
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
  const [historyOpen, setHistoryOpen] = useState(false)
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
      actions={
        loaded && (
          <Button variant="outline" size="sm" onClick={() => setHistoryOpen(true)}>
            <HistoryIcon data-icon="inline-start" />
            History
          </Button>
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
      {loaded && (
        <Sheet open={historyOpen} onOpenChange={setHistoryOpen}>
          <SheetContent className="h-full overflow-y-auto data-[side=right]:w-full data-[side=right]:sm:max-w-xl">
            <SheetHeader className="border-b px-5 py-4">
              <SheetTitle>History</SheetTitle>
              <SheetDescription>
                Every published version of the practice&apos;s insurance list. Restoring publishes an earlier version as a new one.
              </SheetDescription>
            </SheetHeader>
            <InsuranceHistory
              practiceID={props.practiceID}
              locationID={props.locationID}
              active={loaded}
              hasDraft={Boolean(draft)}
              onChanged={() => void reload()}
            />
          </SheetContent>
        </Sheet>
      )}
    </AgentPage>
  )
}

type Row = { at: PlanAt; region: InsuranceRegion; table: InsuranceOfficeTable; plan: InsuranceRegionPlan; row: InsuranceTableRow }
type Carriers = Map<string, InsuranceCarrier>

export function officeRows(catalog: InsuranceCatalog, office: string): Row[] {
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

function matchesWords(words: string[], values: string[]) {
  const haystack = fold(values.join("\n"))
  return words.every((word) => haystack.includes(word))
}

function matches(row: Row, carriers: Carriers, words: string[]) {
  const carrier = carriers.get(row.plan.carrierId)
  return matchesWords(words, [row.plan.label, row.plan.family, ...row.plan.aliases, carrier?.code ?? "", carrier?.name ?? ""])
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
  const choices = useMemo(() => carrierChoices(catalog), [catalog])
  const carriers = useMemo(() => new Map(catalog.carriers.map((carrier) => [carrier.id, carrier])), [catalog.carriers])
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
  const groups = [...new Set(rows.map((row) => row.plan.coverage))].sort((left, right) => coverageOrder.indexOf(left) - coverageOrder.indexOf(right))
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
          <DisclosureRow key={`${coverage}:${words.join(" ")}`} group title={insuranceCoverageLabels[coverage]} detail={group.length}>
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
            choices={choices}
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

function rowSummary(row: Row, names: Names) {
  const answers = row.table.doctors.map((doctor) => [doctor, doctorAnswer(row.row, doctor)] as const)
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

type Detail = { label: string; value: string }

function carrierText(plan: InsuranceRegionPlan, carriers: Carriers) {
  const carrier = carriers.get(plan.carrierId)
  return carrier ? carrierLabel(carrier) : plan.carrierId || "No AMD carrier"
}

function planDetails(plan: InsuranceRegionPlan, carriers: Carriers): Detail[] {
  return [
    { label: "Company", value: plan.family },
    { label: "Visit type", value: insuranceCoverageLabels[plan.coverage] },
    { label: "AMD carrier", value: carrierText(plan, carriers) },
    { label: "Callers also say", value: plan.aliases.join(", ") },
  ]
}

function Details({ items }: { items: Detail[] }) {
  return (
    <dl className="grid gap-x-4 gap-y-1.5 sm:grid-cols-[9rem_1fr]">
      {items
        .filter((item) => item.value)
        .map((item) => (
          <div key={item.label} className="contents">
            <dt className="text-muted-foreground">{item.label}</dt>
            <dd className="whitespace-pre-line">{item.value}</dd>
          </div>
        ))}
    </dl>
  )
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
  carriers: Carriers
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
  const details = [
    ...planDetails(plan, carriers),
    { label: "Doctors", value: table.doctors.map((doctor) => `${doctor}: ${doctorAnswerLabels[doctorAnswer(entry, doctor)]}`).join("\n") },
    { label: "Before booking", value: insuranceBeforeBookingLabels[entry.beforeBooking] },
    { label: "Only at", value: list.format(entry.onlyOffices.map(names)) },
    { label: "Note for callers", value: entry.notice },
  ]

  return (
    <DisclosureRow title={plan.label || "Untitled plan"} detail={rowSummary(row, names)} aside={carriers.get(plan.carrierId)?.code} defaultOpen={openAtMount}>
      <div className="flex flex-col gap-3 px-3 pb-3 pl-9 text-sm">
        {problems.map((problem) => (
          <p key={`${problem.rule}:${problem.message}`} role="alert" className="text-destructive">
            {problem.message}
          </p>
        ))}
        {editing ? (
          <PlanFields row={row} carriers={carriers} choices={choices} names={names} office={office} onEdit={onEdit} />
        ) : (
          <Details items={details} />
        )}
        {canEdit && (
          <div className="flex gap-2">
            <Button size="sm" variant="outline" onClick={() => onEditing(!editing)}>
              {editing ? "Done" : "Edit"}
            </Button>
            {!isSelfPay(plan) && (
              <Button size="sm" variant="ghost" onClick={() => onEdit({ kind: "removeRow", at })}>
                Remove
              </Button>
            )}
          </div>
        )}
      </div>
    </DisclosureRow>
  )
}

type Control = { id: string; "aria-describedby"?: string }

function Field({ label, hint, children }: { label: string; hint?: string; children: (control: Control) => ReactNode }) {
  const id = useId()
  const hintID = useId()
  return (
    <div className="flex flex-col gap-1.5">
      <label htmlFor={id} className="text-sm font-medium">
        {label}
      </label>
      {children(hint ? { id, "aria-describedby": hintID } : { id })}
      {hint && (
        <p id={hintID} className="text-xs text-muted-foreground">
          {hint}
        </p>
      )}
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

function FormSection({ title, note, children }: { title: string; note?: string; children: ReactNode }) {
  const id = useId()
  return (
    <section aria-labelledby={id} className="flex flex-col gap-3">
      <div className="flex flex-col gap-0.5">
        <h3 id={id} className="font-medium">
          {title}
        </h3>
        {note && <p className="text-muted-foreground">{note}</p>}
      </div>
      {children}
    </section>
  )
}

const hints = {
  label: "Abita says this name to callers.",
  family: "The insurance company. Pick one this region already lists, or type a new one.",
  aliases: "Other names callers use for this plan, one per line. Abita treats them as this plan.",
  carrier: "The AdvancedMD carrier for this plan. A plan needs one before any doctor can say Yes.",
  doctors: "Yes lets Abita book this plan with that doctor. No and Pending mean Abita won't book it with them.",
  beforeBooking: "Anything but Nothing stops Abita from booking. Abita tells the caller what's needed instead.",
  notice: "Abita says this to callers when this plan comes up.",
}

function splitAliases(text: string) {
  return text.split("\n").map((alias) => alias.trim()).filter(Boolean)
}

function CompanyField({ region, value, onChange }: { region: InsuranceRegion; value: string; onChange: (family: string) => void }) {
  const listID = useId()
  const families = [...new Set(region.plans.map((plan) => plan.family).filter(Boolean))].sort((left, right) => left.localeCompare(right))
  return (
    <Field label="Company" hint={hints.family}>
      {(control) => (
        <>
          <Input {...control} list={listID} autoComplete="off" value={value} maxLength={200} onChange={(event) => onChange(event.target.value)} />
          <datalist id={listID}>
            {families.map((family) => (
              <option key={family} value={family} />
            ))}
          </datalist>
        </>
      )}
    </Field>
  )
}

function CarrierField({ choices, value, onChange }: { choices: CarrierChoice[]; value: string; onChange: (carrierId: string) => void }) {
  const known = choices.some((choice) => choice.id === value)
  return (
    <Field label="AMD carrier" hint={hints.carrier}>
      {(control) => (
        <NativeSelect {...control} className="w-full sm:w-96" value={value} onChange={(event) => onChange(event.target.value)}>
          {!known && (
            <NativeSelectOption value={value} disabled>
              {value}
            </NativeSelectOption>
          )}
          {choices.map((choice) => (
            <NativeSelectOption key={choice.id} value={choice.id}>
              {choice.label}
            </NativeSelectOption>
          ))}
        </NativeSelect>
      )}
    </Field>
  )
}

function DoctorFields({ doctors, answer, onChange }: { doctors: string[]; answer: (doctor: string) => DoctorAnswer; onChange: (doctor: string, answer: DoctorAnswer) => void }) {
  return (
    <div className="flex flex-col gap-1.5">
      <div className="grid gap-3 sm:grid-cols-2">
        {doctors.map((doctor) => (
          <Field key={doctor} label={doctor}>
            {(control) => (
              <NativeSelect {...control} className="w-full" value={answer(doctor)} onChange={(event) => onChange(doctor, event.target.value as DoctorAnswer)}>
                {doctorAnswers.map((item) => (
                  <NativeSelectOption key={item} value={item}>
                    {doctorAnswerLabels[item]}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            )}
          </Field>
        ))}
      </div>
      <p className="text-xs text-muted-foreground">{hints.doctors}</p>
    </div>
  )
}

function BeforeBookingField({ value, onChange }: { value: BeforeBooking; onChange: (beforeBooking: BeforeBooking) => void }) {
  return (
    <Field label="Before booking" hint={hints.beforeBooking}>
      {(control) => (
        <NativeSelect {...control} className="w-full sm:w-72" value={value} onChange={(event) => onChange(event.target.value as BeforeBooking)}>
          {beforeBookingOrder.map((item) => (
            <NativeSelectOption key={item} value={item}>
              {insuranceBeforeBookingLabels[item]}
            </NativeSelectOption>
          ))}
        </NativeSelect>
      )}
    </Field>
  )
}

function NoticeField({ value, onChange }: { value: string; onChange: (notice: string) => void }) {
  return (
    <Field label="Note for callers" hint={hints.notice}>
      {(control) => <Textarea {...control} value={value} onChange={(event) => onChange(event.target.value)} />}
    </Field>
  )
}

export function PlanFields({
  row,
  carriers,
  choices,
  names,
  office,
  onEdit,
}: {
  row: Row
  carriers: Carriers
  choices: CarrierChoice[]
  names: Names
  office: string
  onEdit: (edit: InsuranceEdit) => void
}) {
  const { at, plan, row: entry, table, region } = row
  const [aliases, setAliases] = useState(plan.aliases.join("\n"))
  const sharedWith = planOffices(region, plan.id).filter((item) => item !== office).map(names)
  const selfPay = isSelfPay(plan)

  return (
    <div className="flex flex-col gap-5">
      <FormSection title="This plan" note={sharedWith.length > 0 ? `Shared with ${list.format(sharedWith)}, so changes here apply there too.` : undefined}>
        {selfPay ? (
          <>
            <p className="text-muted-foreground">
              Abita offers self-pay to callers whose insurance isn&apos;t taken here, so this plan&apos;s details can&apos;t be changed.
            </p>
            <Details items={[{ label: "Plan name", value: plan.label }, ...planDetails(plan, carriers)]} />
          </>
        ) : (
          <>
            <Field label="Plan name" hint={hints.label}>
              {(control) => (
                <Input {...control} value={plan.label} maxLength={200} onChange={(event) => onEdit({ kind: "changePlan", at, change: { label: event.target.value } })} />
              )}
            </Field>
            <CompanyField region={region} value={plan.family} onChange={(family) => onEdit({ kind: "changePlan", at, change: { family } })} />
            <Field label="Callers also say" hint={hints.aliases}>
              {(control) => (
                <Textarea
                  {...control}
                  value={aliases}
                  onChange={(event) => {
                    setAliases(event.target.value)
                    onEdit({ kind: "changePlan", at, change: { aliases: splitAliases(event.target.value) } })
                  }}
                />
              )}
            </Field>
            <CarrierField choices={choices} value={plan.carrierId} onChange={(carrierId) => onEdit({ kind: "setCarrier", at, carrierId })} />
            <Details items={[{ label: "Visit type", value: insuranceCoverageLabels[plan.coverage] }]} />
          </>
        )}
      </FormSection>
      <FormSection title={`At ${list.format(table.offices.map(names))}`}>
        <DoctorFields
          doctors={table.doctors}
          answer={(doctor) => doctorAnswer(entry, doctor)}
          onChange={(doctor, answer) => onEdit({ kind: "setDoctor", at, doctor, answer })}
        />
        {!selfPay && (
          <BeforeBookingField value={entry.beforeBooking} onChange={(beforeBooking) => onEdit({ kind: "setBeforeBooking", at, beforeBooking })} />
        )}
        {!selfPay && table.offices.length > 1 && (
          <fieldset className="flex flex-col gap-2">
            <legend className="mb-1 text-sm font-medium">Only at these offices (none checked means every office above)</legend>
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
        <NoticeField value={entry.notice} onChange={(notice) => onEdit({ kind: "changeRow", at, change: { notice } })} />
      </FormSection>
    </div>
  )
}

type AddEdit = Extract<InsuranceEdit, { kind: "addRow" | "createPlan" }>
const shownMatches = 8

export function AddPlan({
  region,
  table,
  choices,
  onAdd,
  onCancel,
}: {
  region: InsuranceRegion
  table: InsuranceOfficeTable
  choices: CarrierChoice[]
  onAdd: (edit: AddEdit) => void
  onCancel: () => void
}) {
  const [search, setSearch] = useState("")
  const [creating, setCreating] = useState(false)
  const words = fold(search).split(/\s+/).filter(Boolean)
  const listed = new Set(table.rows.map((row) => row.planId))
  const found = region.plans
    .filter((plan) => !listed.has(plan.id) && words.length > 0 && matchesWords(words, [plan.label, plan.family, ...plan.aliases]))
    .sort((left, right) => left.label.localeCompare(right.label, undefined, { sensitivity: "base" }))

  if (creating) {
    return <NewPlanForm region={region} table={table} choices={choices} label={search.trim()} onAdd={onAdd} onCancel={onCancel} />
  }

  return (
    <section aria-label="Add plan" className="flex flex-col gap-3 rounded-lg border bg-card p-3 text-sm">
      <Field label="Find a plan" hint="Search the plans other offices in this region already list, by name, company, or what callers call it.">
        {(control) => <Input {...control} type="search" autoComplete="off" value={search} onChange={(event) => setSearch(event.target.value)} />}
      </Field>
      {words.length > 0 &&
        (found.length > 0 ? (
          <ul aria-label="Matching plans" className="divide-y rounded-lg border">
            {found.slice(0, shownMatches).map((plan) => (
              <li key={plan.id}>
                <button
                  type="button"
                  className="flex w-full flex-wrap items-baseline gap-x-2 px-3 py-2 text-left hover:bg-muted/50"
                  onClick={() => onAdd({ kind: "addRow", at: { region: region.id, table: table.id, plan: plan.id } })}
                >
                  <span className="font-medium">{plan.label}</span>
                  <span className="text-muted-foreground">
                    {plan.family} · {insuranceCoverageLabels[plan.coverage]}
                  </span>
                </button>
              </li>
            ))}
          </ul>
        ) : (
          <p className="text-muted-foreground">No plan in this region matches. Create a new plan instead.</p>
        ))}
      {found.length > shownMatches && <p className="text-muted-foreground">{found.length - shownMatches} more match. Keep typing to narrow the list.</p>}
      <div className="flex gap-2">
        <Button size="sm" variant="outline" onClick={() => setCreating(true)}>
          Create new plan
        </Button>
        <Button size="sm" variant="ghost" onClick={onCancel}>
          Cancel
        </Button>
      </div>
    </section>
  )
}

type NewPlanDraft = Omit<NewPlan, "coverage" | "aliases"> & { coverage: Coverage | ""; aliases: string }

function missingForNewPlan(plan: NewPlanDraft) {
  return [
    plan.label.trim() ? "" : "enter the plan name",
    plan.family.trim() ? "" : "enter the company",
    plan.coverage ? "" : "choose the visit type",
    plan.carrierId || !Object.values(plan.doctors).includes("yes") ? "" : "choose the AMD carrier, because a doctor takes this plan",
  ].filter(Boolean)
}

function NewPlanForm({
  region,
  table,
  choices,
  label,
  onAdd,
  onCancel,
}: {
  region: InsuranceRegion
  table: InsuranceOfficeTable
  choices: CarrierChoice[]
  label: string
  onAdd: (edit: AddEdit) => void
  onCancel: () => void
}) {
  const missingID = useId()
  const [plan, setPlan] = useState<NewPlanDraft>(() => ({
    label,
    family: "",
    coverage: "",
    carrierId: "",
    aliases: "",
    doctors: Object.fromEntries(table.doctors.map((doctor) => [doctor, "no"])),
    beforeBooking: "none",
    notice: "",
  }))
  const missing = missingForNewPlan(plan)
  const change = (fields: Partial<NewPlanDraft>) => setPlan((current) => ({ ...current, ...fields }))

  function add() {
    if (missing.length > 0 || !plan.coverage) return
    onAdd({
      kind: "createPlan",
      at: { region: region.id, table: table.id, plan: newPlanID(region, plan.label) },
      plan: { ...plan, label: plan.label.trim(), family: plan.family.trim(), coverage: plan.coverage, aliases: splitAliases(plan.aliases), notice: plan.notice.trim() },
    })
  }

  return (
    <section aria-label="Create new plan" className="flex flex-col gap-4 rounded-lg border bg-card p-3 text-sm">
      <h3 className="font-medium">Create new plan</h3>
      <Field label="Plan name" hint={hints.label}>
        {(control) => <Input {...control} value={plan.label} maxLength={200} onChange={(event) => change({ label: event.target.value })} />}
      </Field>
      <CompanyField region={region} value={plan.family} onChange={(family) => change({ family })} />
      <Field label="Visit type">
        {(control) => (
          <NativeSelect {...control} className="w-full sm:w-60" value={plan.coverage} onChange={(event) => change({ coverage: event.target.value as Coverage | "" })}>
            <NativeSelectOption value="">Choose a visit type</NativeSelectOption>
            {coverageOrder.map((item) => (
              <NativeSelectOption key={item} value={item}>
                {insuranceCoverageLabels[item]}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        )}
      </Field>
      <CarrierField choices={choices} value={plan.carrierId} onChange={(carrierId) => change({ carrierId })} />
      <Field label="Callers also say" hint={hints.aliases}>
        {(control) => <Textarea {...control} value={plan.aliases} onChange={(event) => change({ aliases: event.target.value })} />}
      </Field>
      <DoctorFields
        doctors={table.doctors}
        answer={(doctor) => doctorAnswer(plan, doctor)}
        onChange={(doctor, answer) => change({ doctors: { ...plan.doctors, [doctor]: answer } })}
      />
      <BeforeBookingField value={plan.beforeBooking} onChange={(beforeBooking) => change({ beforeBooking })} />
      <NoticeField value={plan.notice} onChange={(notice) => change({ notice })} />
      {missing.length > 0 && (
        <p id={missingID} className="text-muted-foreground">
          To add this plan, {list.format(missing)}.
        </p>
      )}
      <div className="flex gap-2">
        <Button size="sm" disabled={missing.length > 0} aria-describedby={missing.length > 0 ? missingID : undefined} onClick={add}>
          Add
        </Button>
        <Button size="sm" variant="ghost" onClick={onCancel}>
          Cancel
        </Button>
      </div>
    </section>
  )
}

export function publishMessage(failure: PortalFailure) {
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
