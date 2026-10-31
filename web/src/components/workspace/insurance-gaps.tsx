"use client"

import { useId, useMemo, useRef, useState } from "react"
import { ShieldQuestionMarkIcon } from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import type { InsuranceCatalog, InsuranceGapResolution, InsuranceValidationError, Location, LocationInsuranceCatalog } from "@/lib/api/generated/types.gen"
import { closeInsuranceGap } from "@/lib/clients/agent-calls"
import { publishInsuranceCatalog, useInsuranceCatalog } from "@/lib/clients/insurance-catalog"
import { carrierChoices, officeTables, record, replay, touchedOffices, type InsuranceEdit } from "@/lib/insurance-catalog"
import { callTime, GapCalls, type GapFixProps } from "./call-gaps"
import { AddPlan, officeNames, officeRows, PlanFields, publishMessage } from "./insurance-list"

const list = new Intl.ListFormat("en", { type: "conjunction" })

export function InsuranceGaps(props: { practiceID: string; locationID: string; locations: Location[] }) {
  return <GapCalls gap="insurance" icon={ShieldQuestionMarkIcon} fix={InsuranceFix} {...props} />
}

function InsuranceFix({ practiceID, locationID, officeName, call, published, onPublished, onSavingChange, onClosed }: GapFixProps) {
  const insurance = useInsuranceCatalog({ practiceID, locationID })
  const [edits, setEdits] = useState<InsuranceEdit[]>([])
  const [adding, setAdding] = useState(0)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState("")
  const [problems, setProblems] = useState<InsuranceValidationError[]>([])
  const attempt = useRef<{ payload: string; revisionID: string }>(undefined)
  const errorID = useId()
  const loaded = insurance.status === "ready" ? insurance.data : undefined
  const stale = insurance.status === "ready" && Boolean(insurance.refreshFailure)
  const catalog = useMemo(() => loaded && replay(loaded.catalog, edits), [loaded, edits])
  const added = edits[0] && JSON.stringify(edits[0].at)
  const row = loaded && catalog && added ? officeRows(catalog, loaded.officeId).find((item) => JSON.stringify(item.at) === added) : undefined
  const tables = loaded ? officeTables(loaded.catalog, loaded.officeId) : []
  const names = officeNames(loaded)
  const choices = useMemo(() => (catalog ? carrierChoices(catalog) : []), [catalog])
  const carriers = useMemo(() => new Map((catalog?.carriers ?? []).map((carrier) => [carrier.id, carrier])), [catalog])
  const others = loaded && catalog ? touchedOffices(loaded.catalog, catalog).filter((office) => office !== loaded.officeId).map(names) : []
  const ready = Boolean(loaded?.canEdit) && !stale && row !== undefined && row.plan.label.trim() !== ""

  function busy(value: boolean) {
    setSaving(value)
    onSavingChange(value)
  }

  function pick(next: InsuranceEdit[]) {
    setEdits(next)
    setProblems([])
    setError("")
  }

  async function close(resolution: InsuranceGapResolution) {
    const outcome = await closeInsuranceGap(call.id, resolution)
    if (!outcome.ok && outcome.failure.kind !== "conflict" && outcome.failure.kind !== "rejected") {
      setError(resolution === "ADDED" ? "Added to the insurance list, but this call couldn't be closed. Try again." : "This call couldn't be closed. Try again.")
      return
    }
    onClosed(call.id)
  }

  async function saveAndClose() {
    if (saving || (!published && !ready)) return
    busy(true)
    setError("")
    setProblems([])
    if (!published && loaded && catalog && row) {
      const outcome = await publish(loaded, catalog, row.plan.label.trim())
      if (!outcome.ok) {
        const { failure } = outcome
        if (failure.kind === "conflict") {
          setError("Someone else changed the insurance list. Check the plan and save again.")
          await insurance.retry()
        } else if (failure.kind === "rejected" && failure.errors?.length) {
          setProblems(failure.errors)
          setError("Not published. Fix these problems, then save again.")
        } else {
          setError(publishMessage(failure))
        }
        busy(false)
        return
      }
      onPublished(call.id)
      void insurance.retry()
    }
    await close("ADDED")
    busy(false)
  }

  function publish(current: LocationInsuranceCatalog, next: InsuranceCatalog, label: string) {
    const publication = {
      practiceId: practiceID,
      locationId: locationID,
      expectedRevisionId: current.revision.id,
      reason: `Added “${label}” from a caller on ${callTime(call)}`,
      catalog: next,
    }
    const payload = JSON.stringify(publication)
    if (attempt.current?.payload !== payload) attempt.current = { payload, revisionID: crypto.randomUUID() }
    return publishInsuranceCatalog({ ...publication, revisionId: attempt.current.revisionID })
  }

  async function notNeeded() {
    if (saving) return
    busy(true)
    setError("")
    await close("NOT_NEEDED")
    busy(false)
  }

  return (
    <form
      aria-label="Fix this"
      aria-describedby={error ? errorID : undefined}
      className="flex flex-col gap-3"
      onSubmit={(event) => {
        event.preventDefault()
        void saveAndClose()
      }}
    >
      <h3 className="text-sm font-medium">Fix this</h3>
      {officeName && <p className="-mt-2 text-sm text-muted-foreground">Saves to {officeName}&apos;s insurance list</p>}
      {published ? (
        <p className="text-sm text-muted-foreground">Added to the insurance list.</p>
      ) : insurance.status === "failed" ? (
        <Alert variant="destructive">
          <AlertTitle>The insurance list couldn&apos;t load</AlertTitle>
          <Button type="button" variant="outline" size="sm" className="mt-2 w-fit" onClick={insurance.retry}>
            Try again
          </Button>
        </Alert>
      ) : !loaded ? (
        <p role="status" className="text-sm text-muted-foreground">Loading the insurance list…</p>
      ) : stale ? (
        <Alert variant="destructive">
          <AlertTitle>The insurance list couldn&apos;t reload</AlertTitle>
          <Button type="button" variant="outline" size="sm" className="mt-2 w-fit" onClick={insurance.retry}>
            Try again
          </Button>
        </Alert>
      ) : !loaded.canEdit ? (
        <p className="text-sm text-muted-foreground">You can&apos;t change the insurance list. Ask someone who can to add this plan.</p>
      ) : tables.length === 0 ? (
        <p className="text-sm text-muted-foreground">This office isn&apos;t on the insurance list.</p>
      ) : row ? (
        <>
          <Button type="button" variant="link" size="sm" className="h-auto w-fit p-0" onClick={() => pick([])}>
            Pick a different plan
          </Button>
          <PlanFields
            key={added}
            row={row}
            carriers={carriers}
            choices={choices}
            names={names}
            office={loaded.officeId}
            onEdit={(edit) => setEdits((current) => record(current, edit))}
          />
          {others.length > 0 && <p role="status" className="text-sm">Also changes {list.format(others)}.</p>}
        </>
      ) : (
        <AddPlan
          key={adding}
          region={tables[0].region}
          table={tables[0].table}
          choices={choices}
          onAdd={(edit) => pick([edit])}
          onCancel={() => setAdding((count) => count + 1)}
        />
      )}
      {error && (
        <Alert id={errorID} variant="destructive">
          <AlertDescription>
            {error}
            {problems.length > 0 && (
              <ul className="list-disc pl-4">
                {problems.map((problem) => (
                  <li key={`${problem.region}:${problem.table}:${problem.planId}:${problem.rule}:${problem.message}`}>{problem.message}</li>
                ))}
              </ul>
            )}
          </AlertDescription>
        </Alert>
      )}
      <div className="flex flex-wrap justify-end gap-2">
        {!published && (
          <Button type="button" variant="ghost" disabled={saving} onClick={() => void notNeeded()}>
            Not needed
          </Button>
        )}
        <Button type="submit" disabled={saving || (!published && !ready)}>
          {saving && <Spinner data-icon="inline-start" />}
          {published ? "Close call" : "Save and close"}
        </Button>
      </div>
    </form>
  )
}
