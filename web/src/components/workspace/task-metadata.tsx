"use client"

import { useState } from "react"
import { Button } from "@/components/ui/button"
import { portalClient } from "@/lib/api/client"
import { changeTaskCategory, setKnowledgeFeedback } from "@/lib/api/generated/sdk.gen"
import type { StaffTaskCategory, Task } from "@/lib/api/generated/types.gen"
import { getAccessToken } from "@/lib/auth-client"
import { taskGroups, taskGroupLabel } from "@/lib/task-groups"

export function TaskMetadata({ task, onUpdated }: { task: Task; onUpdated: (task: Task) => void }) {
  return <TaskMetadataForm key={`${task.id}:${task.version}`} task={task} onUpdated={onUpdated} />
}

function TaskMetadataForm({ task, onUpdated }: { task: Task; onUpdated: (task: Task) => void }) {
  const [category, setCategory] = useState<StaffTaskCategory>(task.category === "billing" ? "other" : task.category ?? "other")
  const [flagged, setFlagged] = useState(task.knowledgeFlagged ?? false)
  const [answer, setAnswer] = useState(task.suggestedAnswer ?? "")
  const [pending, setPending] = useState(false)
  const [error, setError] = useState("")
  async function save(kind: "category" | "feedback") {
    setPending(true); setError("")
    try {
      const token = await getAccessToken()
      if (!token) throw new Error("Sign in again to update this Task.")
      const options = { client: portalClient(token), path: { taskId: task.id } }
      const result = kind === "category"
        ? await changeTaskCategory({ ...options, body: { expectedVersion: task.version, category } })
        : await setKnowledgeFeedback({ ...options, body: { expectedVersion: task.version, flagged, suggestedAnswer: answer } })
      if (!result.data) throw new Error(result.response?.status === 409 ? "This Task changed. Refresh and review it before saving." : "The Task could not be updated. Refresh and try again.")
      onUpdated(result.data)
    } catch (error) { setError(error instanceof Error ? error.message : "The Task could not be updated.") }
    finally { setPending(false) }
  }
  return <div className="space-y-3 border-t pt-3 text-xs" aria-label="Task group and knowledge feedback">
    <p className="font-medium">{taskGroupLabel(task.category)}</p>
    {task.state === "OPEN" && <div className="flex flex-wrap items-center gap-2">
      <label className="min-w-0 flex-1">Move to group
        <select aria-label="Move to group" className="mt-1 w-full rounded-md border bg-background p-2" value={category} onChange={(event) => setCategory(event.target.value as StaffTaskCategory)} disabled={pending}>
          {taskGroups.map((group) => <option key={group.value} value={group.value}>{group.label}</option>)}
        </select>
      </label>
      <Button size="sm" variant="outline" disabled={pending || category === task.category} onClick={() => void save("category")}>Move</Button>
    </div>}
    <label className="flex items-center gap-2"><input type="checkbox" checked={flagged} disabled={pending} onChange={(event) => setFlagged(event.target.checked)} />Flag knowledge-base question</label>
    <label className="block">Suggested answer (optional)
      <textarea aria-label="Suggested answer (optional)" className="mt-1 min-h-16 w-full rounded-md border bg-background p-2" maxLength={2500} value={answer} disabled={pending} onChange={(event) => setAnswer(event.target.value)} />
    </label>
    <Button size="sm" variant="outline" disabled={pending || (flagged === Boolean(task.knowledgeFlagged) && answer === (task.suggestedAnswer ?? ""))} onClick={() => void save("feedback")}>Save feedback</Button>
    {error && <p role="alert" className="text-destructive">{error}</p>}
  </div>
}
