"use client"

import { ChevronDown } from "lucide-react"
import { useState } from "react"

import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Spinner } from "@/components/ui/spinner"
import type { StaffTaskCategory, Task } from "@/lib/api/generated/types.gen"
import { signInRequired } from "@/lib/clients/portal-request"
import { changeTaskCategory } from "@/lib/clients/tasks"
import { taskGroups, taskGroupLabel } from "@/lib/vocabulary"

export function TaskMetadata({ task, onUpdated }: {
  task: Task
  onUpdated: (task: Task) => void
}) {
  return <TaskGroupMenu key={`${task.id}:${task.version}`} task={task} onUpdated={onUpdated} />
}

function TaskGroupMenu({ task, onUpdated }: {
  task: Task
  onUpdated: (task: Task) => void
}) {
  const [pending, setPending] = useState(false)
  const [error, setError] = useState("")

  async function move(category: StaffTaskCategory) {
    if (pending || category === task.category) return
    setPending(true)
    setError("")
    const outcome = await changeTaskCategory(task, category)
    setPending(false)
    if (outcome.ok) {
      onUpdated(outcome.data)
      return
    }
    const { kind } = outcome.failure
    setError(signInRequired(outcome.failure)
      ? "Sign in again to move this Task."
      : kind === "conflict"
        ? "This Task changed. Reopen it to review the latest group before moving."
        : "The Task could not be moved. Try again.")
  }

  return (
    <div role="group" className="border-t pt-2 text-xs" aria-label="Task group">
      <div className="flex items-center justify-between gap-2">
        <p className="min-w-0 flex-1 text-xs leading-5 text-muted-foreground">{taskGroupLabel(task.category)}</p>
        {task.state === "OPEN" && (
          <DropdownMenu>
            <DropdownMenuTrigger render={<Button size="sm" variant="ghost" className="-mr-2 text-xs" disabled={pending} aria-busy={pending || undefined} />}>
              {pending ? <Spinner /> : null}
              Move task <ChevronDown aria-hidden="true" />
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-60">
              <DropdownMenuRadioGroup value={task.category ?? ""} onValueChange={(value) => void move(value as StaffTaskCategory)}>
                <DropdownMenuLabel>Move to group</DropdownMenuLabel>
                {taskGroups.map((group) => (
                  <DropdownMenuRadioItem key={group.value} value={group.value} disabled={pending}>
                    {group.label}
                  </DropdownMenuRadioItem>
                ))}
              </DropdownMenuRadioGroup>
            </DropdownMenuContent>
          </DropdownMenu>
        )}
      </div>
      {error && <p role="alert" className="mt-2 text-destructive">{error}</p>}
    </div>
  )
}
