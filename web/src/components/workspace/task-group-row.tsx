"use client"

import { useState } from "react"
import { Button } from "@/components/ui/button"
import { portalClient } from "@/lib/api/client"
import { completeTask, completeTaskGroup } from "@/lib/api/generated/sdk.gen"
import type { Task } from "@/lib/api/generated/types.gen"
import { getAccessToken } from "@/lib/auth-client"
import { formatUSPhone } from "@/lib/phone"
import { taskGroupLabel } from "@/lib/task-groups"
import { TaskMetadata } from "./task-metadata"

export function TaskGroupRow({ task, onSelect, onUpdated }: { task: Task; onSelect: (task: Task) => void; onUpdated: (task: Task) => void }) {
  const [reviewed, setReviewed] = useState<Task[] | null>(null)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState("")
  const members = task.groupMembers ?? [task]
  const stale = reviewed !== null && (reviewed.length !== members.length || reviewed.some((old) => !members.some((current) => current.id === old.id && current.version === old.version)))
  async function resolve(member?: Task) {
    if (!reviewed || stale) return
    setPending(true); setError("")
    try {
      const token = await getAccessToken()
      if (!token) throw new Error("Sign in again to resolve this Task.")
      const client = portalClient(token)
      const result = member
        ? await completeTask({ client, path: { taskId: member.id }, body: { expectedVersion: member.version } })
        : await completeTaskGroup({ client, path: { taskId: task.id }, body: { members: reviewed.map((item) => ({ id: item.id, expectedVersion: item.version })) } })
      if (!result.data) throw new Error(result.response?.status === 409 ? "This group changed. Refresh and review all requests before resolving." : "Resolution could not be saved. Refresh and try again.")
      setReviewed(null); onUpdated(result.data)
    } catch (error) { setError(error instanceof Error ? error.message : "Resolution could not be saved.") }
    finally { setPending(false) }
  }
  return <li className="rounded-md border border-sidebar-border bg-sidebar p-2" data-testid="task-group-row">
    <button type="button" aria-expanded={reviewed !== null} className="w-full text-left text-xs" onClick={() => { setReviewed(reviewed ? null : [...members]); setError("") }}>
      <span className="block font-medium">{taskGroupLabel(task.category)} · {members.length} {members.length === 1 ? "Task" : "Tasks"}</span>
      <span className="block text-muted-foreground">{formatUSPhone(task.phone)} · {task.locationName}</span>
      <span className="block truncate">{task.title}</span>
    </button>
    {reviewed && <div className="mt-3 space-y-3">
      <p className="text-xs text-muted-foreground">Each request and caller is separate. A shared number is not verified patient identity.</p>
      {stale && <p role="status" className="text-xs">Membership changed. Close and expand this row to review the current requests.</p>}
      {reviewed.map((member) => <article key={member.id} className="space-y-2 border-t pt-2 text-xs" aria-label={`Task: ${member.title}`}>
        <button className="text-left font-medium underline" onClick={() => onSelect(member)}>{member.title}</button>
        <p>{member.callerName ?? "Caller name not provided"} · {member.urgency.replaceAll("_", " ")}</p>
        <p className="text-muted-foreground">Created {new Date(member.createdAt).toLocaleString()} · Source {member.sourceCallId ?? member.callId ?? member.origin}</p>
        <p className="whitespace-pre-wrap break-words">{member.sourceMessage}</p>
        <div className="flex gap-2">
          <Button size="sm" variant="outline" onClick={() => onSelect(member)}>Open Task & call</Button>
          {member.state === "OPEN" && <Button size="sm" disabled={pending || stale} onClick={() => void resolve(member)}>Resolve</Button>}
        </div>
        <TaskMetadata task={member} onUpdated={(updated) => { setReviewed(null); onUpdated(updated) }} />
      </article>)}
      {task.state === "OPEN" && reviewed.length > 1 && <div className="space-y-2 border-t pt-2">
        <p className="text-xs">Resolve all {reviewed.length} requests shown above. This records staff-marked completion.</p>
        <Button size="sm" disabled={pending || stale} onClick={() => void resolve()}>Resolve group</Button>
      </div>}
    </div>}
    {error && <p role="alert" className="mt-2 text-xs text-destructive">{error}</p>}
  </li>
}
