"use client"

import { useState } from "react"
import { CheckCircle2Icon, ArrowUpRightIcon } from "lucide-react"

import { Button } from "@/components/ui/button"
import { portalClient } from "@/lib/api/client"
import { completeTask, completeTaskGroup } from "@/lib/api/generated/sdk.gen"
import type { Task } from "@/lib/api/generated/types.gen"
import { getAccessToken } from "@/lib/auth-client"
import { formatUSPhone } from "@/lib/phone"
import { taskGroupLabel } from "@/lib/task-groups"
import { TaskMetadata } from "./task-metadata"

export function TaskGroupContext({ group, taskRows, canMutate, onSelect, onUpdated }: {
  group: Task
  taskRows: Task[]
  canMutate: boolean
  onSelect: (task: Task) => void
  onUpdated: (task: Task, advance?: boolean) => void
}) {
  // Keep the requests actually shown to staff separate from refreshed membership.
  const [reviewed, setReviewed] = useState(group.groupMembers ?? [group])
  const [pending, setPending] = useState(false)
  const [resolved, setResolved] = useState(false)
  const [error, setError] = useState("")
  const originalIDs = new Set(group.groupMembers?.map((member) => member.id) ?? [group.id])
  const currentRow = taskRows.find((row) => row.category === group.category &&
    (originalIDs.has(row.id) || row.groupMembers?.some((member) => originalIDs.has(member.id))))
  const currentMembers = currentRow ? currentRow.groupMembers ?? [currentRow] : []
  const stale = reviewed.length !== currentMembers.length || reviewed.some((old) =>
    !currentMembers.some((current) => current.id === old.id && current.version === old.version))

  function updated(member: Task) {
    setReviewed((items) => items.flatMap((item) => item.id !== member.id ? [item]
      : member.state === "OPEN" && member.category === group.category ? [member] : []))
    onUpdated(member, member.state === "COMPLETED")
  }

  async function resolve(member?: Task) {
    if (pending || stale || !canMutate || reviewed.length === 0) return
    setPending(true)
    setError("")
    try {
      const token = await getAccessToken()
      if (!token) throw new Error("Sign in again to resolve this Task.")
      const client = portalClient(token)
      const result = member
        ? await completeTask({ client, path: { taskId: member.id }, body: { expectedVersion: member.version } })
        : await completeTaskGroup({ client, path: { taskId: reviewed[0].id }, body: { members: reviewed.map((item) => ({ id: item.id, expectedVersion: item.version })) } })
      if (!result.data) throw new Error(result.response?.status === 409
        ? "This group changed. Reopen it from Tasks to review the latest requests."
        : "Resolution could not be saved. Try again.")
      if (!member || reviewed.length === 1) {
        setResolved(true)
        setReviewed([])
        onUpdated(result.data, true)
      } else {
        updated(result.data)
      }
    } catch (error) {
      setError(error instanceof Error ? error.message : "Resolution could not be saved.")
    } finally {
      setPending(false)
    }
  }

  return (
    <section aria-label="Related Tasks" className="flex h-full min-h-0 flex-1 flex-col overflow-hidden px-5 py-5">
      <header className="shrink-0 pb-4 pr-8">
        <h2 className="text-lg font-semibold leading-snug tracking-[-0.015em]">{taskGroupLabel(group.category)}</h2>
        <p className="mt-1.5 text-xs text-muted-foreground">{formatUSPhone(group.phone)} · {group.locationName}</p>
        <p className="mt-2 text-xs font-medium">{resolved ? "Group resolved" : `${reviewed.length} related Tasks`}</p>
      </header>
      {resolved ? (
        <p role="status" className="flex items-center gap-2 border-t pt-4 text-sm"><CheckCircle2Icon className="size-4 text-success" /> All requests resolved.</p>
      ) : (
        <>
          {stale && <div role="status" className="mb-3 rounded-lg border bg-muted/30 p-3 text-xs">
            {currentMembers.length > 0 ? "Requests changed. Review the latest before resolving." : "This group is no longer in the queue. Reopen a Task from the sidebar."}
            {currentMembers.length > 0 && <Button size="sm" variant="outline" className="mt-2" disabled={pending} onClick={() => { setReviewed([...currentMembers]); setError("") }}>Review latest</Button>}
          </div>}
          {reviewed.length === 0 && <p role="status" className="text-sm text-muted-foreground">No requests remain in this group.</p>}
          <div className="min-h-0 flex-1 divide-y overflow-y-auto border-t">
            {reviewed.map((member) => <article key={member.id} className="space-y-3 py-4" aria-label={`Task: ${member.title}`}>
              <div>
                <h3 className="text-sm font-semibold leading-5">{member.title}</h3>
                <p className="mt-1 text-xs text-muted-foreground">
                  {member.callerName ?? "Caller name not provided"} · {new Date(member.createdAt).toLocaleString(undefined, { month: "short", day: "numeric", hour: "numeric", minute: "2-digit" })}
                  {member.urgency === "high_priority" && <span className="font-medium text-destructive"> · High priority</span>}
                </p>
              </div>
              <p className="whitespace-pre-wrap break-words text-sm leading-5 text-muted-foreground">{member.sourceMessage}</p>
              <div className="flex items-center justify-between gap-2">
                <Button size="sm" variant="ghost" className="-ml-2 text-xs" onClick={() => onSelect(member)}>Open Task & call <ArrowUpRightIcon className="size-3.5" /></Button>
                {canMutate && <Button size="sm" variant="outline" disabled={pending || stale} onClick={() => void resolve(member)}><CheckCircle2Icon /> {member.origin === "APPOINTMENT_REVIEW" ? "Complete verification" : "Complete"}</Button>}
              </div>
              {canMutate && <fieldset disabled={pending || stale} className="min-w-0"><TaskMetadata compact task={member} onUpdated={updated} /></fieldset>}
            </article>)}
          </div>
          {canMutate && reviewed.length > 1 && <footer className="shrink-0 border-t bg-background pt-3 pb-1">
            <Button className="w-full" disabled={pending || stale} onClick={() => void resolve()}><CheckCircle2Icon /> Complete all {reviewed.length} requests</Button>
            <p className="mt-2 text-center text-[11px] leading-4 text-muted-foreground">Completes all {reviewed.length} requests shown. Each keeps its history.</p>
          </footer>}
        </>
      )}
      {error && <p role="alert" className="mt-3 text-xs text-destructive">{error}</p>}
    </section>
  )
}
