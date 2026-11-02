"use client"

import { AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import type { InsuranceRevision, LocationInsuranceCatalog } from "@/lib/api/generated/types.gen"
import { publishInsuranceCatalog, useInsuranceHistory, useInsuranceRevision } from "@/lib/clients/insurance-catalog"
import { signInRequired, type PortalFailure } from "@/lib/clients/portal-request"
import { formatTimestamp } from "@/lib/format-time"
import { diffCatalogs, type CatalogDiff, type Change } from "@/lib/insurance-catalog"
import { insuranceChangeLabels, insuranceFieldLabels, insuranceProvenanceLabels } from "@/lib/vocabulary"
import { AgentQueryBody } from "./manage-agent-page"
import { RestoreRevision, RevisionHistory } from "./revision-history"

function shortID(id: string) {
  return id.slice(0, 8)
}

export function InsuranceHistory({
  practiceID,
  locationID,
  active,
  hasDraft,
  onChanged,
}: {
  practiceID: string
  locationID: string
  active: LocationInsuranceCatalog
  hasDraft: boolean
  onChanged: () => void
}) {
  const history = useInsuranceHistory({ practiceID, locationID })

  function changed() {
    void history.retry()
    onChanged()
  }

  return (
    <RevisionHistory
      query={history}
      subject="insurance history"
      unavailable="Insurance list history isn't available right now."
      summary={(row) => <RevisionSummary row={row} active={row.id === active.revision.id} />}
      detail={(row, back) => (
        <RevisionDetail
          practiceID={practiceID}
          locationID={locationID}
          row={row}
          active={active}
          hasDraft={hasDraft}
          onRestored={() => {
            back()
            changed()
          }}
          onChanged={changed}
        />
      )}
    />
  )
}

function RevisionSummary({ row, active }: { row: InsuranceRevision; active: boolean }) {
  return (
    <>
      <span className="flex items-center gap-2 font-medium">
        {insuranceProvenanceLabels[row.provenance]}
        <span className="font-normal text-muted-foreground">{shortID(row.id)}</span>
        {active && <Badge variant="secondary">Active</Badge>}
      </span>
      <span className="text-muted-foreground">
        {row.createdBy} · <time dateTime={row.createdAt}>{formatTimestamp(row.createdAt)}</time>
      </span>
      <span>{row.reason}</span>
    </>
  )
}

function RevisionDetail({
  practiceID,
  locationID,
  row,
  active,
  hasDraft,
  onRestored,
  onChanged,
}: {
  practiceID: string
  locationID: string
  row: InsuranceRevision
  active: LocationInsuranceCatalog
  hasDraft: boolean
  onRestored: () => void
  onChanged: () => void
}) {
  const revision = useInsuranceRevision({ practiceID, locationID, revisionID: row.id })
  const isActive = row.id === active.revision.id
  const office = active.officeNames[active.officeId] ?? active.officeId

  return (
    <AgentQueryBody
      query={revision}
      subject="insurance list version"
      unavailable="This version isn't available right now."
      onRetry={revision.retry}
      isEmpty={() => false}
      emptyTitle=""
      emptyDescription=""
    >
      {(data) => {
        if (isActive) return <p className="text-muted-foreground">This is the active version.</p>
        const diff = diffCatalogs(active.catalog, data.catalog, active.officeId)
        const count = diff.plans.length + diff.rows.length + diff.tables.length + diff.elsewhere
        if (count === 0) return <p className="text-muted-foreground">This version matches the active list.</p>
        return (
          <>
            <p className="text-muted-foreground">Restoring this version would make these changes to the active list for {office}.</p>
            <DiffView diff={diff} />
            {active.canEdit &&
              (hasDraft ? (
                <p className="text-muted-foreground">Publish or discard your unpublished changes before restoring.</p>
              ) : (
                <RestoreRevision
                  publication={{
                    practiceId: practiceID,
                    locationId: locationID,
                    expectedRevisionId: active.revision.id,
                    reason: `Restore ${shortID(row.id)}`,
                    provenance: "restore" as const,
                    catalog: data.catalog,
                  }}
                  send={async (publication) => {
                    const outcome = await publishInsuranceCatalog(publication)
                    if (!outcome.ok && outcome.failure.kind === "conflict") onChanged()
                    return outcome
                  }}
                  failed={(failure) => (
                    <>
                      <AlertTitle>{restoreMessage(failure)}</AlertTitle>
                      {failure.kind === "rejected" && failure.errors?.length ? (
                        <AlertDescription>
                          <ul className="list-disc pl-4">
                            {failure.errors.map((error) => (
                              <li key={`${error.region}:${error.table}:${error.planId}:${error.rule}:${error.message}`}>{error.message}</li>
                            ))}
                          </ul>
                        </AlertDescription>
                      ) : null}
                    </>
                  )}
                  onRestored={onRestored}
                >
                  <p>
                    Restoring publishes this version&apos;s whole insurance list as a new version, with the reason &ldquo;Restore{" "}
                    {shortID(row.id)}&rdquo;. It is checked like any publish, and history is kept.
                  </p>
                </RestoreRevision>
              ))}
          </>
        )
      }}
    </AgentQueryBody>
  )
}

function fieldList(fields: (keyof typeof insuranceFieldLabels)[]) {
  return [...new Set(fields.map((field) => insuranceFieldLabels[field]))].join(", ")
}

function DiffView({ diff }: { diff: CatalogDiff }) {
  const groups: { title: string; items: { key: string; change: Change; label: string; fields: string }[] }[] = [
    {
      title: "Plans",
      items: diff.plans.map((item) => ({ key: `${item.region}:${item.plan}`, change: item.change, label: item.label, fields: fieldList(item.fields) })),
    },
    {
      title: "This office's rows",
      items: diff.rows.map((item) => ({ key: JSON.stringify(item.at), change: item.change, label: item.label, fields: fieldList(item.fields) })),
    },
    {
      title: "This office's tables",
      items: diff.tables.map((item) => ({ key: `${item.region}:${item.table}`, change: item.change, label: `${item.table} table`, fields: fieldList(item.fields) })),
    },
  ]
  return (
    <div className="flex flex-col gap-4">
      {groups.map((group) =>
        group.items.length > 0 ? (
          <section key={group.title} aria-label={group.title} className="flex flex-col gap-2">
            <h3 className="font-medium">{group.title}</h3>
            <ul className="divide-y rounded-lg border bg-card">
              {group.items.map((item) => (
                <li key={item.key} className="flex flex-wrap items-baseline gap-x-2 px-3 py-2">
                  <Badge variant={item.change === "removed" ? "destructive" : item.change === "added" ? "default" : "secondary"}>
                    {insuranceChangeLabels[item.change]}
                  </Badge>
                  <span className="font-medium">{item.label}</span>
                  {item.fields && <span className="text-muted-foreground">{item.fields}</span>}
                </li>
              ))}
            </ul>
          </section>
        ) : null,
      )}
      {diff.elsewhere > 0 && (
        <p>
          {diff.elsewhere} more {diff.elsewhere === 1 ? "change" : "changes"} elsewhere in the list.
        </p>
      )}
    </div>
  )
}

function restoreMessage(failure: PortalFailure) {
  if (signInRequired(failure)) return "Sign in again to restore."
  if (failure.kind === "unauthorized")
    return failure.message ?? "You don't have access to every office this change affects. Nothing changed."
  if (failure.kind === "rejected" && failure.errors?.length) return "Not restored. This version no longer passes the insurance list checks. Nothing changed."
  if (failure.kind === "conflict") return "Someone else changed the insurance list. Check this version against the new list, then restore again."
  if (failure.kind === "unavailable") return "The restore couldn't be checked, so nothing changed. Try again."
  return failure.message ?? "Not restored. Nothing changed. Try again."
}
