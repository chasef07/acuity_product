"use client"

import { ChevronDown } from "lucide-react"
import { useId, useState } from "react"

import { Button } from "@/components/ui/button"
import { portalClient } from "@/lib/api/client"
import { changeTaskCategory, setKnowledgeFeedback } from "@/lib/api/generated/sdk.gen"
import type { StaffTaskCategory, Task } from "@/lib/api/generated/types.gen"
import { getAccessToken } from "@/lib/auth-client"
import { taskGroups, taskGroupLabel } from "@/lib/task-groups"

export function TaskMetadata({ task, onUpdated, compact = false }: { task: Task; onUpdated: (task: Task) => void; compact?: boolean }) {
  return <TaskMetadataForm key={`${task.id}:${task.version}`} task={task} onUpdated={onUpdated} compact={compact} />
}

function TaskMetadataForm({ task, onUpdated, compact }: { task: Task; onUpdated: (task: Task) => void; compact: boolean }) {
  const initialCategory = task.category === "billing" ? "other" : task.category ?? "other"
  const [category, setCategory] = useState<StaffTaskCategory>(initialCategory)
  const [moving, setMoving] = useState(false)
  const [flagged, setFlagged] = useState(task.knowledgeFlagged ?? false)
  const [answer, setAnswer] = useState(task.suggestedAnswer ?? "")
  const [pending, setPending] = useState(false)
  const [error, setError] = useState("")
  const movePanelId = useId()
  const feedbackPanelId = useId()
  const feedbackChanged = flagged !== Boolean(task.knowledgeFlagged) || answer !== (task.suggestedAnswer ?? "")

  function cancelMove() {
    setMoving(false)
    setCategory(initialCategory)
    setError("")
  }

  function cancelFeedback() {
    setFlagged(task.knowledgeFlagged ?? false)
    setAnswer(task.suggestedAnswer ?? "")
    setError("")
  }

  async function save(kind: "category" | "feedback") {
    setPending(true)
    setError("")
    try {
      const token = await getAccessToken()
      if (!token) throw new Error("Sign in again to update this Task.")
      const options = { client: portalClient(token), path: { taskId: task.id } }
      const result = kind === "category"
        ? await changeTaskCategory({ ...options, body: { expectedVersion: task.version, category } })
        : await setKnowledgeFeedback({ ...options, body: { expectedVersion: task.version, flagged, suggestedAnswer: answer } })
      if (!result.data) throw new Error(result.response?.status === 409 ? "This Task changed. Refresh and review it before saving." : "The Task could not be updated. Refresh and try again.")
      onUpdated(result.data)
    } catch (error) {
      setError(error instanceof Error ? error.message : "The Task could not be updated.")
    } finally {
      setPending(false)
    }
  }

  const knowledgeToggle = (
    <label className="flex cursor-pointer items-center gap-2 leading-5">
        <input
          type="checkbox"
          aria-label="Flag knowledge-base question"
          className="size-3.5 shrink-0 accent-primary outline-none focus-visible:ring-2 focus-visible:ring-ring/30"
          checked={flagged}
          disabled={pending}
          aria-controls={feedbackPanelId}
          onChange={(event) => setFlagged(event.target.checked)}
        />
        {compact ? "Knowledge base" : "Flag knowledge-base question"}
      </label>
  )

  return (
    <div className={compact ? "space-y-2 text-xs" : "space-y-3 border-t pt-3 text-xs"} aria-label="Task group and knowledge feedback">
      <div className="flex flex-wrap items-center justify-between gap-x-3 gap-y-1">
        {compact ? knowledgeToggle : <p className="min-w-0 flex-1 text-xs font-medium leading-5 text-muted-foreground">{taskGroupLabel(task.category)}</p>}
        {task.state === "OPEN" && (
          <Button
            size="sm"
            variant="ghost"
            className="-mr-1.5 h-7 px-1.5 text-xs"
            aria-expanded={moving}
            aria-controls={movePanelId}
            disabled={pending}
            onClick={() => moving ? cancelMove() : setMoving(true)}
          >
            Move task
            <ChevronDown aria-hidden="true" className={moving ? "rotate-180" : ""} />
          </Button>
        )}
      </div>
      {moving && (
        <div id={movePanelId} className="space-y-2 rounded-lg border bg-muted/30 p-2.5">
          <label className="block font-medium">
            Move to group
            <select
              aria-label="Move to group"
              className="mt-1.5 h-9 w-full min-w-0 rounded-md border bg-background px-2 text-xs outline-none focus-visible:ring-2 focus-visible:ring-ring/30"
              value={category}
              onChange={(event) => setCategory(event.target.value as StaffTaskCategory)}
              disabled={pending}
            >
              {taskGroups.map((group) => <option key={group.value} value={group.value}>{group.label}</option>)}
            </select>
          </label>
          <div className="flex justify-end gap-1.5">
            <Button size="sm" variant="ghost" disabled={pending} onClick={cancelMove}>Cancel</Button>
            <Button size="sm" disabled={pending || category === task.category} onClick={() => void save("category")}>Move</Button>
          </div>
        </div>
      )}
      {!compact && knowledgeToggle}
      {(flagged || task.knowledgeFlagged) && (
        <div id={feedbackPanelId} className="space-y-2 rounded-lg border bg-muted/30 p-2.5">
          {flagged && (
            <label className="block font-medium">
              Suggested answer <span className="font-normal text-muted-foreground">(optional)</span>
              <textarea
                aria-label="Suggested answer (optional)"
                className="mt-1.5 block min-h-20 w-full resize-y rounded-md border bg-background px-2.5 py-2 text-xs font-normal leading-5 outline-none focus-visible:ring-2 focus-visible:ring-ring/30"
                maxLength={2500}
                rows={3}
                value={answer}
                disabled={pending}
                onChange={(event) => setAnswer(event.target.value)}
              />
            </label>
          )}
          <div className="flex justify-end gap-1.5">
            <Button size="sm" variant="ghost" disabled={pending} onClick={cancelFeedback}>Cancel</Button>
            <Button size="sm" disabled={pending || !feedbackChanged} onClick={() => void save("feedback")}>Save feedback</Button>
          </div>
        </div>
      )}
      {error && <p role="alert" className="text-destructive">{error}</p>}
    </div>
  )
}
