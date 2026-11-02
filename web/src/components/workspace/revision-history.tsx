"use client"

import { useRef, useState, type ReactNode } from "react"
import { ArrowLeftIcon } from "lucide-react"

import { Alert } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import type { PortalFailure, PortalOutcome } from "@/lib/clients/portal-request"
import type { PortalQuery } from "@/lib/data/queries"
import { nextAttempt, type PublishAttempt } from "@/lib/drafts"
import { AgentQueryBody } from "./manage-agent-page"

export function RevisionHistory<R extends { id: string }>({
  query,
  subject,
  unavailable,
  summary,
  detail,
}: {
  query: PortalQuery<{ revisions: R[] }> & { retry: () => void }
  subject: string
  unavailable: string
  summary: (row: R) => ReactNode
  detail: (row: R, back: () => void, revisions: R[]) => ReactNode
}) {
  const [selectedID, setSelectedID] = useState("")
  const back = () => setSelectedID("")

  return (
    <div className="flex flex-col gap-4 p-5 text-sm">
      <AgentQueryBody
        query={query}
        subject={subject}
        unavailable={unavailable}
        onRetry={query.retry}
        isEmpty={(data) => data.revisions.length === 0}
        emptyTitle="No versions yet."
        emptyDescription="Each publish adds a version here."
      >
        {(data) => {
          const selected = data.revisions.find((row) => row.id === selectedID)
          if (selected) {
            return (
              <div key={selected.id} className="flex flex-col gap-4">
                <Button variant="ghost" size="sm" className="w-fit" onClick={back}>
                  <ArrowLeftIcon data-icon="inline-start" />
                  Back to history
                </Button>
                <div className="flex flex-col gap-1">{summary(selected)}</div>
                {detail(selected, back, data.revisions)}
              </div>
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
                    {summary(row)}
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

export function RestoreRevision<P extends object>({
  publication,
  send,
  ready = true,
  failed,
  onRestored,
  children,
}: {
  publication: P
  send: (publication: P & { revisionId: string }) => Promise<PortalOutcome<unknown>>
  ready?: boolean
  failed: (failure: PortalFailure) => ReactNode
  onRestored: () => void
  children: ReactNode
}) {
  const [confirming, setConfirming] = useState(false)
  const [restoring, setRestoring] = useState(false)
  const [failure, setFailure] = useState<PortalFailure>()
  const attempt = useRef<PublishAttempt>(undefined)

  if (!confirming) {
    return (
      <Button variant="outline" className="w-fit" onClick={() => setConfirming(true)}>
        Restore this version
      </Button>
    )
  }

  async function restore() {
    attempt.current = nextAttempt(attempt.current, publication)
    setRestoring(true)
    setFailure(undefined)
    const outcome = await send({ ...publication, revisionId: attempt.current.revisionID })
    setRestoring(false)
    if (outcome.ok) onRestored()
    else setFailure(outcome.failure)
  }

  return (
    <fieldset disabled={restoring} className="flex flex-col gap-3 rounded-lg border bg-card p-3">
      {children}
      {failure && <Alert variant="destructive">{failed(failure)}</Alert>}
      <Button className="w-fit" disabled={!ready} onClick={() => void restore()}>
        {restoring && <Spinner data-icon="inline-start" />}
        {restoring ? "Restoring…" : "Restore"}
      </Button>
    </fieldset>
  )
}
