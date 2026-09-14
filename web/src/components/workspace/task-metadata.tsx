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
import { portalClient } from "@/lib/api/client"
import { changeTaskCategory } from "@/lib/api/generated/sdk.gen"
import type { StaffTaskCategory, Task } from "@/lib/api/generated/types.gen"
import { getAccessToken } from "@/lib/auth-client"
import { taskGroups, taskGroupLabel } from "@/lib/task-groups"

export function TaskMetadata({ task, onUpdated, compact = false }: {
  task: Task
  onUpdated: (task: Task) => void
  compact?: boolean
}) {
  return <TaskGroupMenu key={`${task.id}:${task.version}`} task={task} onUpdated={onUpdated} compact={compact} />
}

function TaskGroupMenu({ task, onUpdated, compact }: {
  task: Task
  onUpdated: (task: Task) => void
  compact: boolean
}) {
  const [pending, setPending] = useState(false)
  const [error, setError] = useState("")

  async function move(category: StaffTaskCategory) {
    if (pending || category === task.category) return
    setPending(true)
    setError("")
    try {
      const token = await getAccessToken()
      if (!token) throw new Error("Sign in again to move this Task.")
      const result = await changeTaskCategory({
        client: portalClient(token),
        path: { taskId: task.id },
        body: { expectedVersion: task.version, category },
      })
      if (!result.data) throw new Error(result.response?.status === 409
        ? "This Task changed. Reopen it to review the latest group before moving."
        : "The Task could not be moved. Try again.")
      onUpdated(result.data)
    } catch (error) {
      setError(error instanceof Error ? error.message : "The Task could not be moved.")
    } finally {
      setPending(false)
    }
  }

  return (
    <div className={compact ? "text-xs" : "border-t pt-2 text-xs"} aria-label="Task group">
      <div className="flex items-center justify-between gap-2">
        {!compact && <p className="min-w-0 flex-1 text-xs leading-5 text-muted-foreground">{taskGroupLabel(task.category)}</p>}
        {task.state === "OPEN" && (
          <DropdownMenu>
            <DropdownMenuTrigger render={<Button size="sm" variant="ghost" className={compact ? "-ml-2 text-xs" : "-mr-2 text-xs"} disabled={pending} aria-busy={pending || undefined} />}>
              {pending ? <Spinner /> : null}
              Move task <ChevronDown aria-hidden="true" />
            </DropdownMenuTrigger>
            <DropdownMenuContent align={compact ? "start" : "end"} className="w-60">
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
