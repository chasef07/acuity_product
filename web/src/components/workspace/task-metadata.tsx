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
    <div role="group" className="min-w-0 text-xs" aria-label="Task group">
      <div className="flex min-w-0 items-center">
        {task.state === "OPEN" ? (
          <DropdownMenu>
            <DropdownMenuTrigger render={<Button size="xs" variant="ghost" aria-label={`Move task: ${taskGroupLabel(task.category)}`} className="-ml-1.5 h-6 min-w-0 px-1.5 text-xs font-normal text-muted-foreground" disabled={pending} aria-busy={pending || undefined} />}>
              {pending ? <Spinner /> : null}
              <span className="truncate">{taskGroupLabel(task.category)}</span> <ChevronDown aria-hidden="true" />
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
        ) : (
          <p className="min-w-0 leading-5">{taskGroupLabel(task.category)}</p>
        )}
      </div>
      {error && <p role="alert" className="mt-2 text-destructive">{error}</p>}
    </div>
  )
}
