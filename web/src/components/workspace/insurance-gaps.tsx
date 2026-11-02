"use client"

import { useMemo, useRef, useState } from "react"
import { ShieldQuestionMarkIcon } from "lucide-react"

import { Button } from "@/components/ui/button"
import type { InsuranceCatalog, Location, LocationInsuranceCatalog } from "@/lib/api/generated/types.gen"
import { publishInsuranceCatalog, useInsuranceCatalog } from "@/lib/clients/insurance-catalog"
import { nextAttempt, type PublishAttempt } from "@/lib/drafts"
import { carrierChoices, officeTables, record, replay, touchedOffices, type InsuranceEdit } from "@/lib/insurance-catalog"
import { callTime, GapCalls, GapFixForm, type GapFixProps } from "./call-gaps"
import { AddPlan, officeNames, officeRows, PlanFields, publishMessage } from "./insurance-list"

const list = new Intl.ListFormat("en", { type: "conjunction" })

export function InsuranceGaps(props: { practiceID: string; locationID: string; locations: Location[] }) {
  return <GapCalls gap="insurance" icon={ShieldQuestionMarkIcon} fix={InsuranceFix} {...props} />
}

function InsuranceFix(props: GapFixProps) {
  const { practiceID, locationID, call } = props
  const insurance = useInsuranceCatalog({ practiceID, locationID })
  const [edits, setEdits] = useState<InsuranceEdit[]>([])
  const [adding, setAdding] = useState(0)
  const attempt = useRef<PublishAttempt>(undefined)
  const loaded = insurance.status === "ready" ? insurance.data : undefined
  const stale = insurance.status === "ready" && Boolean(insurance.refreshFailure)
  const catalog = useMemo(() => loaded && replay(loaded.catalog, edits), [loaded, edits])
  const added = edits[0] && JSON.stringify(edits[0].at)
  const row = loaded && catalog && added ? officeRows(catalog, loaded.officeId).find((item) => JSON.stringify(item.at) === added) : undefined
  const label = row?.plan.label.trim() ?? ""
  const tables = loaded ? officeTables(loaded.catalog, loaded.officeId) : []
  const names = officeNames(loaded)
  const choices = useMemo(() => (catalog ? carrierChoices(catalog) : []), [catalog])
  const carriers = useMemo(() => new Map((catalog?.carriers ?? []).map((carrier) => [carrier.id, carrier])), [catalog])
  const others = loaded && catalog ? touchedOffices(loaded.catalog, catalog).filter((office) => office !== loaded.officeId).map(names) : []

  async function publish(current: LocationInsuranceCatalog, next: InsuranceCatalog) {
    const publication = {
      practiceId: practiceID,
      locationId: locationID,
      expectedRevisionId: current.revision.id,
      reason: `Added “${label}” from a caller on ${callTime(call)}`,
      catalog: next,
    }
    attempt.current = nextAttempt(attempt.current, publication)
    const outcome = await publishInsuranceCatalog({ ...publication, revisionId: attempt.current.revisionID })
    if (outcome.ok) return undefined
    const { failure } = outcome
    if (failure.kind === "conflict") {
      await insurance.retry()
      return "Someone else changed the insurance list. Check the plan and save again."
    }
    if (failure.kind === "rejected" && failure.errors?.length) {
      return (
        <>
          Not published. Fix these problems, then save again.
          <ul className="list-disc pl-4">
            {failure.errors.map((problem) => (
              <li key={`${problem.region}:${problem.table}:${problem.planId}:${problem.rule}:${problem.message}`}>{problem.message}</li>
            ))}
          </ul>
        </>
      )
    }
    return publishMessage(failure)
  }

  return (
    <GapFixForm
      gap="insurance"
      fix={props}
      query={insurance}
      publish={loaded?.canEdit && !stale && catalog && label ? () => publish(loaded, catalog) : undefined}
    >
      {(clearError, current) =>
        !current.canEdit ? (
          <p className="text-sm text-muted-foreground">You can&apos;t change the insurance list. Ask someone who can to add this plan.</p>
        ) : tables.length === 0 ? (
          <p className="text-sm text-muted-foreground">This office isn&apos;t on the insurance list.</p>
        ) : row ? (
          <>
            <Button
              type="button"
              variant="link"
              size="sm"
              className="h-auto w-fit p-0"
              onClick={() => {
                setEdits([])
                clearError()
              }}
            >
              Pick a different plan
            </Button>
            <PlanFields
              key={added}
              row={row}
              carriers={carriers}
              choices={choices}
              names={names}
              office={current.officeId}
              onEdit={(edit) => setEdits((edited) => record(edited, edit))}
            />
            {others.length > 0 && <p role="status" className="text-sm">Also changes {list.format(others)}.</p>}
          </>
        ) : (
          <AddPlan
            key={adding}
            region={tables[0].region}
            table={tables[0].table}
            choices={choices}
            onAdd={(edit) => {
              setEdits([edit])
              clearError()
            }}
            onCancel={() => setAdding((count) => count + 1)}
          />
        )
      }
    </GapFixForm>
  )
}
