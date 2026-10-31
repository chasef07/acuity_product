"use client"

import { useRef, useState } from "react"
import { ArrowLeftIcon } from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import type { InsuranceCatalog, InsuranceRevision, LocationInsuranceCatalog } from "@/lib/api/generated/types.gen"
import { publishInsuranceCatalog, useInsuranceHistory, useInsuranceRevision } from "@/lib/clients/insurance-catalog"
import type { PortalFailure } from "@/lib/clients/portal-request"
import { formatTimestamp } from "@/lib/format-time"
import { diffCatalogs, type CatalogDiff, type Change } from "@/lib/insurance-catalog"
import { insuranceChangeLabels, insuranceFieldLabels, insuranceProvenanceLabels } from "@/lib/vocabulary"
import { AgentQueryBody } from "./manage-agent-page"

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
  const [selectedID, setSelectedID] = useState("")

  function changed() {
    void history.retry()
    onChanged()
  }

  return (
    <div className="flex flex-col gap-4 p-5 text-sm">
      <AgentQueryBody
        query={history}
        subject="insurance history"
        unavailable="Insurance list history isn't available right now."
        onRetry={history.retry}
        isEmpty={(data) => data.revisions.length === 0}
        emptyTitle="No versions yet."
        emptyDescription="Each publish adds a version here."
      >
        {(data) => {
          const selected = data.revisions.find((row) => row.id === selectedID)
          if (selected) {
            return (
              <RevisionDetail
                key={selected.id}
                practiceID={practiceID}
                locationID={locationID}
                row={selected}
                active={active}
                hasDraft={hasDraft}
                onBack={() => setSelectedID("")}
                onRestored={() => {
                  setSelectedID("")
                  changed()
                }}
                onChanged={changed}
              />
            )
          }
          return (
            <ul className="divide-y overflow-hidden rounded-lg border bg-card">
              {data.revisions.map((row) => (
                <li key={row.id}>
                  <button
                    type="button"
                    className="flex w-full flex-col gap-1 px-3 py-2 text-left hover:bg-muted/50"
                    onClick={() => setSelectedID(row.id)}
                  >
                    <RevisionSummary row={row} active={row.id === active.revision.id} />
                  </button>
                </li>
              ))}
            </ul>
          )
        }}
      </AgentQueryBody>
    </div>
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
  onBack,
  onRestored,
  onChanged,
}: {
  practiceID: string
  locationID: string
  row: InsuranceRevision
  active: LocationInsuranceCatalog
  hasDraft: boolean
  onBack: () => void
  onRestored: () => void
  onChanged: () => void
}) {
  const revision = useInsuranceRevision({ practiceID, locationID, revisionID: row.id })
  const isActive = row.id === active.revision.id
  const office = active.officeNames[active.officeId] ?? active.officeId

  return (
    <div className="flex flex-col gap-4">
      <Button variant="ghost" size="sm" className="w-fit" onClick={onBack}>
        <ArrowLeftIcon data-icon="inline-start" />
        Back to history
      </Button>
      <div className="flex flex-col gap-1">
        <RevisionSummary row={row} active={isActive} />
      </div>
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
                    practiceID={practiceID}
                    locationID={locationID}
                    row={row}
                    catalog={data.catalog}
                    activeRevisionID={active.revision.id}
                    onRestored={onRestored}
                    onChanged={onChanged}
                  />
                ))}
            </>
          )
        }}
      </AgentQueryBody>
    </div>
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
  if (failure.kind === "signedOut" || failure.kind === "unauthenticated") return "Sign in again to restore."
  if (failure.kind === "unauthorized")
    return failure.message ?? "You don't have access to every office this change affects. Nothing changed."
  if (failure.kind === "rejected" && failure.errors?.length) return "Not restored. This version no longer passes the insurance list checks. Nothing changed."
  if (failure.kind === "conflict") return "Someone else changed the insurance list. Check this version against the new list, then restore again."
  if (failure.kind === "unavailable") return "The restore couldn't be checked, so nothing changed. Try again."
  return failure.message ?? "Not restored. Nothing changed. Try again."
}

function RestoreRevision({
  practiceID,
  locationID,
  row,
  catalog,
  activeRevisionID,
  onRestored,
  onChanged,
}: {
  practiceID: string
  locationID: string
  row: InsuranceRevision
  catalog: InsuranceCatalog
  activeRevisionID: string
  onRestored: () => void
  onChanged: () => void
}) {
  const [confirming, setConfirming] = useState(false)
  const [restoring, setRestoring] = useState(false)
  const [failure, setFailure] = useState<PortalFailure>()
  const attempt = useRef<{ payload: string; revisionID: string }>(undefined)

  if (!confirming) {
    return (
      <Button variant="outline" className="w-fit" onClick={() => setConfirming(true)}>
        Restore this version
      </Button>
    )
  }

  async function restore() {
    const publication = {
      practiceId: practiceID,
      locationId: locationID,
      expectedRevisionId: activeRevisionID,
      reason: `Restore ${shortID(row.id)}`,
      provenance: "restore" as const,
      catalog,
    }
    const payload = JSON.stringify(publication)
    if (attempt.current?.payload !== payload) attempt.current = { payload, revisionID: crypto.randomUUID() }
    setRestoring(true)
    setFailure(undefined)
    const outcome = await publishInsuranceCatalog({ ...publication, revisionId: attempt.current.revisionID })
    setRestoring(false)
    if (outcome.ok) {
      onRestored()
      return
    }
    setFailure(outcome.failure)
    if (outcome.failure.kind === "conflict") onChanged()
  }

  return (
    <fieldset disabled={restoring} className="flex flex-col gap-3 rounded-lg border bg-card p-3">
      <p>
        Restoring publishes this version&apos;s whole insurance list as a new version, with the reason &ldquo;Restore {shortID(row.id)}&rdquo;. It
        is checked like any publish, and history is kept.
      </p>
      {failure && (
        <Alert variant="destructive">
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
        </Alert>
      )}
      <Button className="w-fit" onClick={() => void restore()}>
        {restoring && <Spinner data-icon="inline-start" />}
        {restoring ? "Restoring…" : "Restore"}
      </Button>
    </fieldset>
  )
}
